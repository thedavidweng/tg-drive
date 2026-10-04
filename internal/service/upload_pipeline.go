package service

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/thedavidweng/tg-drive/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
	"github.com/thedavidweng/tg-drive/core/manifest"
	"github.com/thedavidweng/tg-drive/core/publisher"
	"github.com/thedavidweng/tg-drive/core/telegram"
)

// The upload pipeline (ADR 0027) is the one path from local files to
// published Telegram messages. Single-file cp is a one-member run; multi-file
// cp, recursive cp, and imports are album runs. A run plans every member
// without writing anything, takes the operation locks of every destination,
// stages pending rows under those locks, then sends in units: one ordinary
// message for a lone member, one media group (ADR 0017) otherwise. Every unit
// follows the same crash-window policy.

// uploadMember is one file a caller asks to publish.
type uploadMember struct {
	localPath string
	// dest is the requested canonical destination; conflict policy may
	// rename it.
	dest string
	pres Presentation
	// humanCaption is source text kept above the rendered caption block
	// (UploadOptions.Caption, and imports); empty renders the block alone.
	humanCaption string
}

// uploadRun configures one pipeline run.
type uploadRun struct {
	members []uploadMember
	policy  ConflictPolicy
	noHash  bool
	// album selects the multi-file contract: members may share media
	// groups, --replace cannot supersede an occupant, and errors name the
	// multi-file remedies. A non-album run is single-file cp.
	album bool
	// lenient drops a failing member (reported in failures) instead of
	// aborting the run before any Telegram write (--continue-on-error).
	lenient bool
	opts    UploadOptions
	// results reports member outcomes to opts.Observer; runUpload sets it.
	results *memberResults
}

// memberResults reports each member's outcome to an observer exactly once,
// keyed by the member's index in the run.
type memberResults struct {
	obs      Observer
	reported map[int]bool
}

func (r *memberResults) report(index int, it Item, status ItemStatus, err error) {
	if r.reported[index] {
		return
	}
	r.reported[index] = true
	r.obs.item(ItemResult{Item: it, Status: status, Err: err})
}

func (r uploadRun) replaces() bool {
	return !r.album && r.policy == ConflictReplace
}

// sentMember is one published member.
type sentMember struct {
	dest          string
	messageID     int
	manifestMsgID int
	size          int64
	hash          string
	resumed       bool
}

// uploadOutcome reports a run. It is returned alongside a send error too, so
// lenient callers can still count skips and per-member failures.
type uploadOutcome struct {
	sent     []sentMember
	albums   []AlbumGroup
	skipped  []string
	failures []albumMemberFailure
	resumed  bool
}

// uploadChannel is the drive channel a run publishes into.
type uploadChannel struct {
	rowID        int64
	tgID         int64
	tgIDStr      string
	manifestChat string
}

// stagedUpload is a planned member; the fields after bigFile are filled
// under the lock once its pending row is staged.
type stagedUpload struct {
	uploadMember
	index   int
	size    int64
	bigFile bool

	hash string
	// rendition is the member's one rendering pass: the caption sent with
	// the media and the tags its publication indexes.
	rendition *publisher.Rendition
	meta      manifest.FileMeta
	fileID    int64
	adopted   bool
	resumed   bool
	// replace is the active row a --replace supersedes.
	replace *sqlitestore.FileRow
}

// runUpload publishes run.members. Conflict detection and pending-row
// refusals happen for every member before any Telegram write, so a strict
// run aborts with nothing sent.
func (a *App) runUpload(ctx context.Context, run uploadRun) (_ *uploadOutcome, err error) {
	run.results = &memberResults{obs: run.opts.Observer, reported: map[int]bool{}}
	// A run that aborts accounts for the members it never finished.
	defer func() {
		if err == nil {
			return
		}
		for i, m := range run.members {
			run.results.report(i, m.item(), ItemFailed, err)
		}
	}()
	cc, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	channelID := cc.rowID
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, err
	}

	out := &uploadOutcome{albums: []AlbumGroup{}}
	type indexedFailure struct {
		index int
		f     albumMemberFailure
	}
	var failures []indexedFailure
	report := func(index int, m uploadMember, err error) error {
		run.results.report(index, m.item(), ItemFailed, err)
		if !run.lenient {
			return err
		}
		failures = append(failures, indexedFailure{index, albumMemberFailure{localPath: m.localPath, err: err}})
		return nil
	}
	finish := func() {
		sort.SliceStable(failures, func(i, j int) bool { return failures[i].index < failures[j].index })
		for _, f := range failures {
			out.failures = append(out.failures, f.f)
		}
	}

	var planned []*stagedUpload
	for i, m := range run.members {
		s, err := a.planUpload(ctx, channelID, run, i, m, active)
		if err != nil {
			if err := report(i, m, err); err != nil {
				return nil, err
			}
			continue
		}
		if s == nil {
			out.skipped = append(out.skipped, m.dest)
			run.results.report(i, m.item(), ItemSkipped, nil)
			continue
		}
		planned = append(planned, s)
	}
	if len(planned) == 0 {
		finish()
		return out, nil
	}

	dests := make([]string, 0, len(planned))
	for _, s := range planned {
		dests = append(dests, s.dest)
	}
	ch := uploadChannel{rowID: cc.rowID, tgID: cc.tgID, tgIDStr: cc.tgIDStr}
	err = a.operate(ctx, cc, dests, func(ctx context.Context) error {
		return a.publishLocked(ctx, run, ch, planned, report, out)
	})
	finish()
	return out, err
}

// planUpload resolves one member's destination against the index without
// writing anything. A nil member with a nil error means --skip-existing
// dropped it.
func (a *App) planUpload(ctx context.Context, channelID int64, run uploadRun, index int, m uploadMember, active []fsmodel.ActivePath) (*stagedUpload, error) {
	size, err := a.checkUploadSource(ctx, m.localPath, m.dest, run.album)
	if err != nil {
		return nil, err
	}
	// A pending row is a failed or crashed upload, not a live file: it is
	// adoptable on retry (or superseded by --replace) instead of wedging the
	// path.
	activeExists, pendingAdoptable, err := a.destOccupancy(ctx, channelID, m.dest)
	if err != nil {
		return nil, err
	}
	remedies := "use --replace, --skip-existing, or --auto-rename"
	if run.album {
		remedies = "use --skip-existing or --auto-rename"
	}
	dest, keep, err := applyUploadPolicy(m.dest, run.policy, activeExists, !run.album, active, remedies)
	if err != nil {
		return nil, err
	}
	if !keep {
		return nil, nil
	}
	// File/dir invariants always apply; the destination itself is excluded
	// when this upload replaces or adopts whatever sits there.
	supersede := run.replaces() || pendingAdoptable
	if err := fsmodel.CheckUploadConflict(dest, uploadCheckSet(active, dest, supersede)); err != nil {
		return nil, err
	}
	s := &stagedUpload{uploadMember: m, index: index, size: size, bigFile: size > telegram.ResumableBigFileBytes}
	s.dest = dest
	return s, nil
}

// checkUploadSource validates a local source and returns its size. dest only
// names the file in the multi-file wording.
func (a *App) checkUploadSource(ctx context.Context, localPath, dest string, album bool) (int64, error) {
	info, err := a.files().Stat(ctx, localPath)
	if err != nil {
		return 0, apperr.New(apperr.ErrLocalNotFound, fmt.Sprintf("local file %q not found", localPath))
	}
	if info.IsDir {
		if album {
			return 0, apperr.New(apperr.ErrUsage, fmt.Sprintf("%q is a directory; album members must be files", localPath))
		}
		return 0, apperr.New(apperr.ErrUsage, "use --recursive for directories")
	}
	if limit := a.uploadLimit(ctx); info.Size > limit {
		if album {
			return 0, apperr.New(apperr.ErrFileTooLarge, fmt.Sprintf("%s exceeds %d bytes", dest, limit))
		}
		return 0, apperr.New(apperr.ErrFileTooLarge, fmt.Sprintf("file exceeds %d bytes", limit))
	}
	return info.Size, nil
}

// publishLocked runs under the locks of every planned destination: it
// stages all pending rows first, then sends unit by unit.
func (a *App) publishLocked(ctx context.Context, run uploadRun, ch uploadChannel, planned []*stagedUpload, report func(int, uploadMember, error) error, out *uploadOutcome) error {
	// Machine records live in the discussion group's comment threads
	// (ADR 0018); fail before staging or uploading anything when it is not
	// linked.
	cc, err := a.channel(ctx)
	if err != nil {
		return err
	}
	manifestChat, err := cc.discussionChat(ctx)
	if err != nil {
		return err
	}
	ch.manifestChat = manifestChat
	thumbs, err := a.readThumbnails(ctx, planned)
	if err != nil {
		return err
	}
	existingSlugs, err := a.loadExistingSlugs(ctx, ch.rowID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var staged []*stagedUpload
	abortStaging := func(err error) error {
		cctx, cancel := cleanupContext(ctx)
		defer cancel()
		for _, done := range staged {
			if !done.adopted {
				_ = a.DB.DiscardUpload(cctx, done.fileID)
			}
		}
		return err
	}
	for _, s := range planned {
		if err := cancelled(ctx); err != nil {
			return abortStaging(err)
		}
		if err := a.stageUpload(ctx, run, ch, s, existingSlugs, now); err != nil {
			if stopsRun(ctx, err) {
				return abortStaging(apperr.Cancelled())
			}
			if err := report(s.index, s.uploadMember, err); err != nil {
				return abortStaging(err)
			}
			continue
		}
		staged = append(staged, s)
	}

	units := uploadUnits(staged)
	for i, unit := range units {
		err := cancelled(ctx)
		var sent []sentMember
		var group *AlbumGroup
		if err == nil {
			// One human caption per batch, on the very first member; a lone
			// member is an ordinary message and always carries its own.
			withCaption := i == 0 || len(unit) == 1
			sent, group, err = a.sendUnit(ctx, run.opts, ch, unit, withCaption, thumbs)
		} else {
			a.discardUnsent(ctx, unit)
		}
		if err != nil {
			for _, s := range unit {
				run.results.report(s.index, s.item(), ItemFailed, err)
			}
			for _, rest := range units[i+1:] {
				a.discardUnsent(ctx, rest)
			}
			return err
		}
		for _, s := range sent {
			out.sent = append(out.sent, s)
			out.resumed = out.resumed || s.resumed
		}
		for _, s := range unit {
			run.results.report(s.index, s.item(), ItemCompleted, nil)
		}
		if group != nil {
			out.albums = append(out.albums, *group)
		}
	}
	return nil
}

// stageUpload resolves the pending row at the member's destination now that
// the content identity is known (adopt on match so a plain retry resumes,
// supersede under --replace, refuse otherwise), renders the caption once,
// and stages the member's pending row.
func (a *App) stageUpload(ctx context.Context, run uploadRun, ch uploadChannel, s *stagedUpload, existingSlugs map[string]string, now string) error {
	if a.hashesUpload(run.noHash, s.size) {
		run.opts.Observer.stage(s.item(), StageHashing)
	}
	hash, err := a.hashUpload(ctx, s.localPath, run.noHash, s.size)
	if err != nil {
		return err
	}
	pending, err := a.lookupPending(ctx, ch.rowID, s.dest)
	if err != nil {
		return err
	}
	adoptFileID := int64(0)
	if pending.rowID > 0 {
		switch {
		case run.replaces():
			// Supersede the pending row and its stale upload state; roll back
			// a media message a crash left recorded but unpublished.
			if pending.msgID.Valid {
				_ = a.TG.DeleteMessage(ctx, ch.tgID, int(pending.msgID.Int64))
			}
			if err := a.DB.DiscardUpload(ctx, pending.rowID); err != nil {
				return apperr.Wrap(apperr.ErrDB, "supersede pending row", err)
			}
		case pending.msgID.Valid:
			return errUnpublishedUpload(s.dest, pending.msgID.Int64)
		case !pending.matches(s.size, hash):
			if run.album {
				return apperr.New(apperr.ErrPathExists,
					fmt.Sprintf("an interrupted upload of different content occupies %q; replace it with single-path td cp --replace or remove it first", s.dest))
			}
			return apperr.New(apperr.ErrPathExists,
				fmt.Sprintf("an interrupted upload of different content occupies %q (use --replace to supersede it)", s.dest))
		default:
			adoptFileID = pending.rowID
		}
	}
	rendition, err := a.renderUpload(s.dest, s.localPath, s.size, hash, now, ch.manifestChat, existingSlugs, s.humanCaption)
	if err != nil {
		return err
	}
	meta := rendition.Meta()
	if run.replaces() {
		target, found, err := a.DB.ActiveByPath(ctx, ch.rowID, s.dest)
		if err != nil {
			return apperr.Wrap(apperr.ErrDB, "lookup replace target", err)
		}
		if found {
			s.replace = &target
		}
	}
	fileID, resumed, err := a.stagePendingRow(ctx, ch.rowID, s.dest, s.localPath, s.size, hash, meta.MIME, now, adoptFileID)
	if err != nil {
		return err
	}
	s.hash, s.rendition, s.meta = hash, rendition, meta
	s.fileID, s.adopted, s.resumed = fileID, adoptFileID > 0, resumed
	return nil
}

// readThumbnails loads each distinct thumbnail once. Photos never name one:
// Presentation.Validate rejects it.
func (a *App) readThumbnails(ctx context.Context, planned []*stagedUpload) (map[string][]byte, error) {
	thumbs := map[string][]byte{}
	for _, s := range planned {
		path := s.pres.ThumbPath
		if path == "" {
			continue
		}
		if _, ok := thumbs[path]; ok {
			continue
		}
		tf, err := a.files().Open(ctx, path)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrLocalNotFound, "open thumbnail", err)
		}
		thumb, err := io.ReadAll(tf)
		_ = tf.Close()
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrLocalNotFound, "read thumbnail", err)
		}
		thumbs[path] = thumb
	}
	return thumbs, nil
}

// uploadUnits splits staged members into send units: at most
// MaxMediaGroupMembers each, never mixing presentation kinds. A Telegram
// media group is uniform, and an imported saved album can hold both photos
// and videos, so a kind change ends the current unit.
func uploadUnits(staged []*stagedUpload) [][]*stagedUpload {
	var out [][]*stagedUpload
	var cur []*stagedUpload
	for _, s := range staged {
		if len(cur) == telegram.MaxMediaGroupMembers || (len(cur) > 0 && s.pres.Kind != cur[0].pres.Kind) {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, s)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// sendUnit sends one unit and completes its publication. A lone member is an
// ordinary message with its own per-file manifest record; a Telegram media
// group needs at least two members, so larger units go out as a group with
// one td-album:v1 inventory. group is nil for a lone member.
func (a *App) sendUnit(ctx context.Context, opts UploadOptions, ch uploadChannel, unit []*stagedUpload, withCaption bool, thumbs map[string][]byte) ([]sentMember, *AlbumGroup, error) {
	threads := opts.threads(a.Cfg)
	reqs := make([]telegram.UploadRequest, 0, len(unit))
	var readers []io.Closer
	defer func() {
		for _, r := range readers {
			_ = r.Close()
		}
	}()
	for i, s := range unit {
		req := telegram.UploadRequest{
			ChannelID:      ch.tgID,
			FileName:       s.meta.DisplayName,
			MIME:           s.meta.MIME,
			Size:           s.size,
			ContentHash:    s.hash,
			Path:           s.localPath,
			Threads:        threads,
			PartSize:       opts.partSizeBytes(a.Cfg),
			ResumableKey:   resumableKey(s.fileID),
			ResumableStore: a.DB,
			Thumb:          thumbs[s.pres.ThumbPath],
			Progress:       uploadProgress(opts.Observer, s.item()),
		}
		if i == 0 && withCaption {
			req.Caption = s.rendition.Caption()
		}
		s.pres.apply(&req)
		if !s.bigFile {
			// Small files stream from a reader; the resumable path reads by
			// offset and never holds a reader for the upload's duration.
			f, err := a.files().Open(ctx, s.localPath)
			if err != nil {
				a.discardUnsent(ctx, unit)
				return nil, nil, err
			}
			readers = append(readers, f)
			req.Reader = f
		}
		reqs = append(reqs, req)
	}

	for _, s := range unit {
		opts.Observer.stage(s.item(), StageUploading)
	}
	group := len(unit) > 1
	var results []telegram.UploadResult
	var err error
	if group {
		results, err = a.TG.UploadMediaGroup(ctx, reqs)
	} else {
		var up *telegram.UploadResult
		if up, err = a.TG.UploadMedia(ctx, reqs[0]); err == nil {
			results = []telegram.UploadResult{*up}
		}
	}
	if err != nil {
		a.discardUnsent(ctx, unit)
		return nil, nil, telegram.MapError(err)
	}

	// Persist message ids before any further Telegram or DB work so a crash
	// or index failure cannot look like "never uploaded" to RepairPending.
	now := time.Now().UTC().Format(time.RFC3339)
	for i, res := range results {
		if err := a.recordPendingMessage(ctx, unit[i].fileID, res.MessageID, now); err != nil {
			return nil, nil, a.abandonUnit(ctx, ch, unit, results, 0, now, err, "could not be recorded")
		}
	}

	for _, s := range unit {
		opts.Observer.stage(s.item(), StagePublishing)
	}
	sent := make([]sentMember, 0, len(unit))
	replyID := 0
	if group {
		req := publisher.AlbumRequest{
			ChannelRowID: ch.rowID,
			ChannelID:    ch.tgID,
			GroupedID:    results[0].GroupedID,
		}
		for i, res := range results {
			m := publisher.AlbumMember{FileID: unit[i].fileID, MessageID: res.MessageID, Rendition: unit[i].rendition}
			if unit[i].replace != nil {
				m.ReplaceFileID = unit[i].replace.ID
			}
			req.Members = append(req.Members, m)
		}
		pubRes, err := a.publisher().PublishAlbum(ctx, req)
		if err != nil {
			// The inventory id is set when it reached Telegram; the
			// rollback deletes it with the media.
			if pubRes != nil {
				replyID = pubRes.InventoryMsgID
			}
			return nil, nil, a.abandonUnit(ctx, ch, unit, results, replyID, now, err, "could not be completed or rolled back")
		}
		replyID = pubRes.InventoryMsgID
		for i, res := range results {
			sent = append(sent, unit[i].sentAs(res.MessageID, replyID))
		}
	} else {
		s := unit[0]
		req := publisher.FileRequest{
			ChannelRowID: ch.rowID,
			ChannelID:    ch.tgID,
			FileID:       s.fileID,
			MessageID:    results[0].MessageID,
			Rendition:    s.rendition,
		}
		if s.replace != nil {
			req.ReplaceFileID = s.replace.ID
		}
		pubRes, err := a.publisher().PublishFile(ctx, req)
		if err != nil {
			// The manifest id is set when the record reached Telegram; the
			// rollback deletes it with the media.
			if pubRes != nil {
				replyID = pubRes.ManifestMsgID
			}
			return nil, nil, a.abandonUnit(ctx, ch, unit, results, replyID, now, err, "could not be completed or rolled back")
		}
		sent = append(sent, s.sentAs(results[0].MessageID, pubRes.ManifestMsgID))
	}

	for _, s := range unit {
		if s.replace != nil {
			a.retireReplacedFile(ctx, ch.rowID, ch.tgID, s.dest, s.meta.DisplayName, s.retireArgs(ch))
		}
	}
	if !group {
		return sent, nil, nil
	}
	out := &AlbumGroup{GroupedID: results[0].GroupedID, ReplyMessageID: replyID}
	for _, s := range sent {
		out.Paths = append(out.Paths, s.dest)
		out.MessageIDs = append(out.MessageIDs, s.messageID)
	}
	return sent, out, nil
}

func (m uploadMember) item() Item {
	return Item{Source: m.localPath, Path: m.dest}
}

// uploadProgress adapts the Telegram client's part confirmations for one
// item to obs.
func uploadProgress(obs Observer, it Item) telegram.UploadProgress {
	if obs.OnProgress == nil {
		return nil
	}
	return func(_ context.Context, st telegram.UploadProgressState) error {
		obs.progress(Progress{
			Item:  it,
			Done:  st.Uploaded,
			Total: st.Total,
			Part:  &UploadPart{FileName: st.FileName, Index: st.Part, Size: st.PartSize},
		})
		return nil
	}
}

func (s *stagedUpload) sentAs(messageID, manifestMsgID int) sentMember {
	return sentMember{
		dest:          s.dest,
		messageID:     messageID,
		manifestMsgID: manifestMsgID,
		size:          s.size,
		hash:          s.hash,
		resumed:       s.resumed,
	}
}

// retireArgs describes the superseded file for retireReplacedFile.
func (s *stagedUpload) retireArgs(ch uploadChannel) uploadLockedArgs {
	return uploadLockedArgs{
		localPath:       s.localPath,
		dest:            s.dest,
		policy:          ConflictReplace,
		size:            s.size,
		channelID:       ch.rowID,
		tgChID:          ch.tgID,
		tgIDStr:         ch.tgIDStr,
		replaceFileID:   s.replace.ID,
		oldMsgID:        s.replace.MessageID,
		oldManifestID:   s.replace.ManifestMsgID,
		oldManifestChat: s.replace.ManifestChat,
		pres:            s.pres,
		humanCaption:    s.humanCaption,
	}
}

// discardUnsent drops the pending rows of members that never reached
// Telegram. Resumable big-file rows survive so a plain retry resumes them.
func (a *App) discardUnsent(ctx context.Context, unit []*stagedUpload) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	for _, s := range unit {
		if !s.bigFile {
			_ = a.DB.DiscardUpload(ctx, s.fileID)
		}
	}
}

// abandonUnit is the crash-window policy for a unit that reached Telegram
// but could not be recorded or published: the whole unit is rolled back
// (inventory and media deleted, rows discarded), and members whose media
// cannot be deleted stay orphaned with their message ids. The error is
// ERR_ORPHANED_UPLOAD exactly when something was orphaned; otherwise the
// rollback was clean and cause is returned.
func (a *App) abandonUnit(ctx context.Context, ch uploadChannel, unit []*stagedUpload, results []telegram.UploadResult, replyID int, now string, cause error, failure string) error {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	if replyID > 0 {
		_ = a.manifestCarrier(ch.manifestChat).Delete(ctx, ch.tgID, replyID)
	}
	orphaned := false
	for i, res := range results {
		if a.abandonUploadedMedia(ctx, ch.tgID, unit[i].fileID, res.MessageID, now) {
			orphaned = true
		}
	}
	if !orphaned {
		return cause
	}
	if len(results) == 1 {
		return apperr.New(apperr.ErrOrphanedUpload,
			fmt.Sprintf("upload reached Telegram message %d but %s; run td repair --orphaned", results[0].MessageID, failure))
	}
	return apperr.New(apperr.ErrOrphanedUpload,
		fmt.Sprintf("album reached Telegram but %s; run td repair --orphaned", failure))
}

func (a *App) recordPendingMessage(ctx context.Context, fileID int64, messageID int, now string) error {
	if err := a.DB.RecordMessage(ctx, fileID, messageID, now); err != nil {
		return apperr.Wrap(apperr.ErrDB, "record uploaded message", err)
	}
	return nil
}

// abandonUploadedMedia deletes one member's media when possible; otherwise
// its pending row is marked orphaned with message_id so RepairPending will
// not re-upload.
func (a *App) abandonUploadedMedia(ctx context.Context, tgChID, fileID int64, messageID int, now string) (orphaned bool) {
	delErr := a.TG.DeleteMessage(ctx, tgChID, messageID)
	if delErr == nil || isMessageGone(delErr) {
		_ = a.DB.DiscardUpload(ctx, fileID)
		return false
	}
	_ = a.DB.MarkOrphaned(ctx, fileID, messageID, now)
	return true
}
