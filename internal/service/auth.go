package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/fileperm"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
)

// Result structs declare fields in JSON-key alphabetical order: the envelopes
// were historically marshaled from maps, whose keys encoding/json sorts, and
// the byte output must not change.

// AuthUser identifies the logged-in Telegram account.
type AuthUser struct {
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone"`
	UserID      int64  `json:"user_id"`
}

func newAuthUser(u telegram.User) AuthUser {
	phone, _ := config.RedactValue("telegram.phone", u.Phone, false).(string)
	return AuthUser{DisplayName: u.DisplayName, Phone: phone, UserID: u.ID}
}

// AuthLoginResult reports a completed login.
type AuthLoginResult struct {
	AlreadyAuthenticated bool `json:"already_authenticated"`
	AuthUser
}

// AuthLogin performs interactive login.
func (a *App) AuthLogin(ctx context.Context, codeFn telegram.CodeFunc, passwordFn telegram.PasswordFunc, opts telegram.LoginOptions) (*AuthLoginResult, error) {
	if a.Cfg.Telegram.APIID == 0 || a.Cfg.Telegram.APIHash == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram.api_id and telegram.api_hash required")
	}
	phone := a.Cfg.Telegram.Phone
	if phone == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram.phone required")
	}
	res, err := a.TG.Login(ctx, a.Cfg.Telegram.APIID, a.Cfg.Telegram.APIHash, phone, codeFn, passwordFn, opts)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	user := res.User
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.DB.Raw().ExecContext(ctx, `
		insert into accounts(tg_user_id,phone,display_name,created_at,updated_at) values(?,?,?,?,?)
		on conflict(tg_user_id) do update set phone=excluded.phone, display_name=excluded.display_name, updated_at=excluded.updated_at`,
		fmt.Sprintf("%d", user.ID), user.Phone, user.DisplayName, now, now)
	return &AuthLoginResult{AlreadyAuthenticated: res.AlreadyAuthorized, AuthUser: newAuthUser(user)}, nil
}

// AuthStatusResult reports whether a session is logged in; the account
// fields are present only when it is.
type AuthStatusResult struct {
	Authenticated bool `json:"authenticated"`
	*AuthUser
}

// AuthStatus returns authentication status.
func (a *App) AuthStatus(ctx context.Context) (*AuthStatusResult, error) {
	user, ok, err := a.TG.Status(ctx)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if !ok {
		return &AuthStatusResult{Authenticated: false}, nil
	}
	u := newAuthUser(*user)
	return &AuthStatusResult{Authenticated: true, AuthUser: &u}, nil
}

// AuthLogout logs out.
func (a *App) AuthLogout(ctx context.Context) error {
	return a.TG.Logout(ctx)
}

// InitRootResult reports a channel binding. A re-run on an already-bound root
// reports AlreadyInitialized and skips the discussion and scan fields.
type InitRootResult struct {
	AlreadyInitialized  bool   `json:"already_initialized,omitempty"`
	ChannelID           int64  `json:"channel_id"`
	ChannelTitle        string `json:"channel_title"`
	DiscussionChannelID int64  `json:"discussion_channel_id,omitempty"`
	IndexedFiles        *int   `json:"indexed_files,omitempty"`
	LocalRoot           string `json:"local_root"`
	ScanError           string `json:"scan_error,omitempty"`
}

// InitRoot initializes a local root and optionally creates/binds a channel.
func (a *App) InitRoot(ctx context.Context, localRoot, channelTitle, create, bind string) (*InitRootResult, error) {
	user, ok, err := a.TG.Status(ctx)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if !ok {
		return nil, apperr.New(apperr.ErrAuthRequired, "not logged in; run: td auth login")
	}
	// Re-running init on an already-bound root must not mint another channel:
	// channel creation is expensive on a real account and cannot be undone
	// from here.
	if create != "" {
		if existing := a.findRootBinding(ctx, user.ID, localRoot); existing != nil {
			return &InitRootResult{
				AlreadyInitialized: true,
				ChannelID:          existing.ID,
				ChannelTitle:       existing.Title,
				LocalRoot:          localRoot,
			}, nil
		}
	}
	if abs, err := filepath.Abs(localRoot); err == nil {
		localRoot = abs
	}
	var ch *telegram.Channel
	switch {
	case create != "":
		ch, err = a.TG.CreateChannel(ctx, create)
	case bind != "":
		ch, err = a.TG.ResolveChannel(ctx, bind)
	case channelTitle != "":
		ch, err = a.TG.ResolveChannel(ctx, channelTitle)
		if err != nil {
			ch, err = a.TG.CreateChannel(ctx, channelTitle)
		}
	default:
		return nil, apperr.New(apperr.ErrUsage, "channel title or --create-channel/--bind-channel required")
	}
	if err != nil {
		return nil, telegram.MapError(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var accountID int64
	_ = a.DB.Raw().QueryRowContext(ctx, `select id from accounts where tg_user_id=?`, fmt.Sprintf("%d", user.ID)).Scan(&accountID)
	if accountID == 0 {
		res, err := a.DB.Raw().ExecContext(ctx, `insert into accounts(tg_user_id,phone,display_name,created_at,updated_at) values(?,?,?,?,?)`,
			fmt.Sprintf("%d", user.ID), user.Phone, user.DisplayName, now, now)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "insert account", err)
		}
		accountID, _ = res.LastInsertId()
	}
	// RETURNING covers both upsert branches (fresh insert and conflict
	// update) with the row id, so no re-select is needed.
	var channelRowID int64
	err = a.DB.Raw().QueryRowContext(ctx, `
		insert into channels(account_id,tg_channel_id,access_hash,title,root_local_path,root_remote_path,strategy,created_at,updated_at)
		values(?,?,?,?,?,?,'single',?,?)
		on conflict(account_id, tg_channel_id) do update set title=excluded.title, access_hash=excluded.access_hash, root_local_path=excluded.root_local_path, updated_at=excluded.updated_at
		returning id`,
		accountID, fmt.Sprintf("%d", ch.ID), fmt.Sprintf("%d", ch.AccessHash), ch.Title, localRoot, "/", now, now).Scan(&channelRowID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "insert channel", err)
	}
	group, err := a.TG.EnsureDiscussionGroup(ctx, ch.ID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if err := a.DB.SetDiscussionGroup(ctx, channelRowID, fmt.Sprintf("%d", group.ID), fmt.Sprintf("%d", group.AccessHash), group.Title); err != nil {
		return nil, err
	}
	out := &InitRootResult{
		ChannelID:           ch.ID,
		ChannelTitle:        ch.Title,
		LocalRoot:           localRoot,
		DiscussionChannelID: group.ID,
	}
	// Binding an existing drive rebuilds its index here. A failed scan
	// leaves a usable binding, so report it instead of failing init.
	if scanned, err := a.initScan(ctx, ch.ID); err != nil {
		out.ScanError = err.Error()
	} else {
		out.IndexedFiles = &scanned.Active
	}
	return out, nil
}

// initScan full-scans the channel just bound; without pinning the selector,
// Scan would pick App.Channel or the first channel in the DB.
func (a *App) initScan(ctx context.Context, tgChannelID int64) (*ScanResult, error) {
	return a.Scan(WithChannel(ctx, strconv.FormatInt(tgChannelID, 10)), ScanOptions{Full: true})
}

// findRootBinding returns the channel already bound to localRoot for the
// given account, comparing absolute paths.
func (a *App) findRootBinding(ctx context.Context, tgUserID int64, localRoot string) *telegram.Channel {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return nil
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select c.tg_channel_id, c.title, c.root_local_path
		from channels c join accounts a on a.id = c.account_id
		where a.tg_user_id = ?`, fmt.Sprintf("%d", tgUserID))
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var idStr, title, root string
		if rows.Scan(&idStr, &title, &root) != nil {
			continue
		}
		absStored, err := filepath.Abs(root)
		if err != nil || absStored != absRoot {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		return &telegram.Channel{ID: id, Title: title}
	}
	return nil
}

// ShareResult carries the invite link for a shared path.
type ShareResult struct {
	Channel    string `json:"channel"`
	Hashtag    string `json:"hashtag"`
	InviteLink string `json:"invite_link"`
	Path       string `json:"path"`
}

// Share returns an invite link and an optional legacy hashtag for a path.
func (a *App) Share(ctx context.Context, remotePath string) (*ShareResult, error) {
	p, err := fsmodel.NormalizeCanonicalPath(remotePath)
	if err != nil {
		return nil, err
	}
	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	link, err := a.TG.GetInviteLink(ctx, ch.tgID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	var tag string
	_ = a.DB.Raw().QueryRowContext(ctx, `
		select pt.tag from path_tags pt join files f on f.id=pt.file_id
		where f.channel_id=? and f.canonical_path=? and f.status='active' order by pt.depth desc limit 1`, ch.rowID, p).Scan(&tag)
	if tag == "" {
		existing, err := a.loadExistingSlugs(ctx, ch.rowID)
		if err != nil {
			return nil, err
		}
		// Share targets a directory subtree: include a synthetic leaf so the
		// chain covers the full shared path, then take the deepest tag.
		chainPath := p
		if chainPath != "/" {
			chainPath += "/_"
		}
		tags, _, _ := pathcodec.GenerateChain(chainPath, existing)
		if len(tags) > 0 {
			tag = tags[len(tags)-1]
		}
	}
	var title string
	_ = a.DB.Raw().QueryRowContext(ctx, `select title from channels where id=?`, ch.rowID).Scan(&title)
	return &ShareResult{Channel: title, Hashtag: tag, InviteLink: link, Path: p}, nil
}

// DoctorResult maps each capability check to pass/warn/fail/unknown. The
// capability fields are present only when Telegram answered the probe.
type DoctorResult struct {
	Checks         map[string]string `json:"checks"`
	DiscussionOK   *bool             `json:"discussion_ok,omitempty"`
	Hints          map[string]string `json:"hints,omitempty"`
	MaxUploadBytes *int64            `json:"max_upload_bytes,omitempty"`
	SavedDeleteOK  *bool             `json:"saved_delete_ok,omitempty"`
	SavedHistoryOK *bool             `json:"saved_history_ok,omitempty"`
}

// Doctor runs capability checks.
func (a *App) Doctor(ctx context.Context) (*DoctorResult, error) {
	checks := map[string]string{}
	hints := map[string]string{}
	out := &DoctorResult{}

	if a.Cfg.Telegram.APIID != 0 && a.Cfg.Telegram.APIHash != "" {
		checks["config"] = "pass"
	} else {
		checks["config"] = "warn"
		hints["config"] = "telegram.api_id / api_hash not set; run: td auth setup"
	}
	if _, err := os.Stat(a.Cfg.Storage.SessionPath); err == nil {
		checks["session_file"] = "pass"
	} else {
		checks["session_file"] = "warn"
		hints["session_file"] = fmt.Sprintf("no session file at %s; run: td auth login", config.DisplayPath(a.Cfg.Storage.SessionPath))
	}
	if loose := a.loosePrivateFiles(); len(loose) == 0 {
		checks["file_permissions"] = "pass"
	} else {
		checks["file_permissions"] = "warn"
		hints["file_permissions"] = "readable by other users: " + strings.Join(loose, ", ") + "; restrict them to your account (chmod 600 on POSIX)"
	}
	checks["caption_counter"] = captionCounterSelfTest()
	checks["path_codec"] = pathCodecSelfTest()

	user, ok, err := a.TG.Status(ctx)
	switch {
	case err != nil:
		checks["auth"] = "fail"
		hints["auth"] = "could not reach Telegram; check network, then run: td auth login"
	case ok:
		checks["auth"] = "pass"
	default:
		checks["auth"] = "fail"
		hints["auth"] = "not logged in; run: td auth login"
	}
	_ = user

	if a.DB != nil {
		mode, err := a.DB.JournalMode(ctx)
		switch {
		case err != nil:
			checks["db"] = "fail"
			checks["db_wal"] = "unknown"
			hints["db"] = fmt.Sprintf("database error at %s; check storage.db_path", config.DisplayPath(a.Cfg.Storage.DBPath))
		case strings.EqualFold(mode, "wal"):
			checks["db"] = "pass"
			checks["db_wal"] = "pass"
		default:
			checks["db"] = "pass"
			checks["db_wal"] = "fail"
			hints["db_wal"] = fmt.Sprintf("journal_mode is %q, not WAL; concurrent td processes may hit lock errors (network filesystems often refuse WAL)", mode)
		}
	} else {
		checks["db"] = "unknown"
		checks["db_wal"] = "unknown"
	}

	ch, chErr := a.channel(ctx)
	if chErr != nil {
		checks["channel"] = "fail"
		hints["channel"] = "no channel bound; run: td init <local-root> --create-channel"
		checks["upload"] = "unknown"
		checks["delete"] = "unknown"
		checks["invite_link"] = "unknown"
		checks["edit_old_caption"] = "unknown"
		checks["file_size_limit"] = "unknown"
		checks["history_read"] = "unknown"
		// Saved Messages is independent of the bound drive channel. Probe it
		// with a zero channel id so `td doctor` can still report whether
		// `td import saved` is available before `td init`.
		if caps, err := a.TG.Doctor(ctx, 0); err != nil || caps == nil {
			checks["saved_history"] = "unknown"
			checks["saved_delete"] = "unknown"
		} else {
			checks["saved_history"] = boolCheck(caps.SavedHistoryOK)
			checks["saved_delete"] = boolCheck(caps.SavedDeleteOK)
			out.SavedHistoryOK = &caps.SavedHistoryOK
			out.SavedDeleteOK = &caps.SavedDeleteOK
		}
	} else {
		checks["channel"] = "pass"
		tgChID := ch.tgID
		// One-message page: proves td scan can page the channel history
		// without walking it.
		if _, err := a.TG.History(ctx, tgChID, 0, 1); err != nil {
			checks["history_read"] = "fail"
			hints["history_read"] = "cannot read channel history, so td scan cannot rebuild the index: " + telegram.MapError(err).Error()
		} else {
			checks["history_read"] = "pass"
		}
		caps, err := a.TG.Doctor(ctx, tgChID)
		if err != nil {
			checks["upload"] = "unknown"
			checks["saved_history"] = "unknown"
			checks["saved_delete"] = "unknown"
		} else {
			checks["upload"] = boolCheck(caps.UploadOK)
			checks["delete"] = boolCheck(caps.DeleteOK)
			checks["invite_link"] = boolCheck(caps.InviteLinkOK)
			checks["edit_old_caption"] = boolCheck(caps.EditOldCaptionOK)
			checks["discussion"] = boolCheck(caps.DiscussionOK)
			checks["saved_history"] = boolCheck(caps.SavedHistoryOK)
			checks["saved_delete"] = boolCheck(caps.SavedDeleteOK)
			if !caps.DiscussionOK {
				hints["discussion"] = "no linked discussion group; machine records need it (ADR 0018); run: td channels link-discussion"
			}
			if !caps.SavedHistoryOK {
				hints["saved_history"] = "Saved Messages history is unavailable; check authentication and Telegram access"
			}
			if !caps.SavedDeleteOK {
				hints["saved_delete"] = "--delete-source is unavailable for Saved Messages"
			}
			out.MaxUploadBytes = &caps.MaxUploadBytes
			out.DiscussionOK = &caps.DiscussionOK
			out.SavedHistoryOK = &caps.SavedHistoryOK
			out.SavedDeleteOK = &caps.SavedDeleteOK
			switch {
			case caps.MaxUploadBytes >= a.Cfg.Limits.FreeUploadBytes:
				checks["file_size_limit"] = "pass"
			default:
				checks["file_size_limit"] = "fail"
				hints["file_size_limit"] = "upload limit below the expected free tier; check account status"
			}
			_, _ = a.DB.Raw().ExecContext(ctx, `update channels set updated_at=? where id=?`, time.Now().UTC().Format(time.RFC3339), ch.rowID)
		}
	}
	out.Checks = checks
	out.Hints = hints
	return out, nil
}

// loosePrivateFiles lists td's private files that other users can read,
// in display form.
func (a *App) loosePrivateFiles() []string {
	paths := []string{a.ConfigPath, a.Cfg.Storage.SessionPath, a.Cfg.Storage.DBPath}
	if p := a.Cfg.Storage.DBPath; p != "" {
		paths = append(paths, p+"-wal", p+"-shm")
	}
	var loose []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if ok, err := fileperm.IsPrivate(p); err == nil && !ok {
			loose = append(loose, config.DisplayPath(p))
		}
	}
	return loose
}

func boolCheck(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// captionCounterSelfTest verifies UTF-16 code unit counting on fixed vectors.
func captionCounterSelfTest() string {
	vectors := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 2},
		{"📷", 2},
		{"a📷b", 4},
	}
	for _, v := range vectors {
		if manifest.UTF16Units(v.s) != v.want {
			return "fail"
		}
	}
	return "pass"
}

// pathCodecSelfTest verifies slug generation stays inside the Telegram-safe
// hashtag charset on fixed vectors.
func pathCodecSelfTest() string {
	for _, p := range []string{"/My Photos/2024/x.jpg", "/图片/2024/x.jpg", "/emoji/📷/x.jpg", "/a_b/c/x.txt"} {
		tags, _, err := pathcodec.GenerateChain(p, map[string]string{})
		if err != nil {
			return "fail"
		}
		for _, tag := range tags {
			if !strings.HasPrefix(tag, "#td_") {
				return "fail"
			}
			for _, r := range tag[1:] {
				safe := r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
				if !safe {
					return "fail"
				}
			}
		}
	}
	return "pass"
}

// PathCodecDoctorResult reports the path codec self-test and stored slug
// verification.
type PathCodecDoctorResult struct {
	CorruptRows  int    `json:"corrupt_rows"`
	DBCheck      string `json:"db_check"`
	DBRows       int    `json:"db_rows"`
	FixedVectors string `json:"fixed_vectors"`
}

// PathCodecDoctor runs the path codec self-test and verifies stored slug
// mappings against the current database.
func (a *App) PathCodecDoctor(ctx context.Context) (*PathCodecDoctorResult, error) {
	out := &PathCodecDoctorResult{FixedVectors: pathCodecSelfTest()}
	if a.DB == nil {
		out.DBCheck = "unknown"
		return out, nil
	}

	rows, err := a.DB.Raw().QueryContext(ctx, `select parent_canonical_path, segment, slug, hash_len from path_segment_slugs`)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "query path_segment_slugs", err)
	}
	defer func() { _ = rows.Close() }()

	total, corrupt := 0, 0
	for rows.Next() {
		var parent, segment, slug string
		var hashLen int
		if err := rows.Scan(&parent, &segment, &slug, &hashLen); err != nil {
			corrupt++
			continue
		}
		total++
		if pathcodec.SegmentSlug(segment, hashLen) != slug {
			corrupt++
		}
	}
	_ = rows.Err()

	out.DBRows = total
	out.CorruptRows = corrupt
	if corrupt == 0 {
		out.DBCheck = "pass"
	} else {
		out.DBCheck = "fail"
	}
	return out, nil
}
