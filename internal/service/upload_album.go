package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	apperr "github.com/thedavidweng/tg-drive/core/errors"
	"github.com/thedavidweng/tg-drive/core/fsmodel"
)

// Album uploads publish several files as native Telegram media groups
// (ADR 0013 / issue #26): one human td:v1 caption on the very first member,
// empty sibling captions, and one td-album:v1 inventory reply per group.
// Sets larger than Telegram's group limit split into consecutive groups of
// MaxMediaGroupMembers.

// AlbumGroup describes one sent media group for the JSON envelope.
type AlbumGroup struct {
	GroupedID      int64    `json:"grouped_id"`
	ReplyMessageID int      `json:"reply_message_id"`
	MessageIDs     []int    `json:"message_ids"`
	Paths          []string `json:"paths"`
}

// AlbumUploadResult reports a multi-file album upload.
type AlbumUploadResult struct {
	Albums     []AlbumGroup `json:"albums"`
	ChannelID  string       `json:"channel_id"`
	Errors     []string     `json:"errors"`
	InviteLink string       `json:"invite_link,omitempty"`
	Resumed    bool         `json:"resumed,omitempty"`
	Skipped    int          `json:"skipped"`
	Uploaded   int          `json:"uploaded"`
}

// albumMemberFailure is one source dropped from a lenient batch plan.
type albumMemberFailure struct {
	localPath string
	err       error
}

func (f albumMemberFailure) String() string {
	return fmt.Sprintf("%s: %v", f.localPath, f.err)
}

// UploadFilesAs uploads multiple local files as native Telegram albums
// sharing one destination directory. The zero Presentation behaves like the
// document default; presentation flags apply uniformly to every member.
// Conflict policies apply per file; --replace is not supported (replace
// individual files with single-path td cp instead).
func (a *App) UploadFilesAs(ctx context.Context, localPaths []string, remoteDir string, policy ConflictPolicy, noHash bool, pres Presentation, opts UploadOptions) (*AlbumUploadResult, error) {
	if err := opts.Validate(policy); err != nil {
		return nil, err
	}
	if len(localPaths) < 2 {
		return nil, apperr.New(apperr.ErrUsage, "album upload requires at least two local files")
	}
	if err := pres.Validate(); err != nil {
		return nil, err
	}
	if policy == ConflictReplace {
		return nil, apperr.New(apperr.ErrUsage,
			"album uploads cannot --replace; replace an existing file with single-path td cp --replace, or remove it first")
	}

	ch, err := a.channel(ctx)
	if err != nil {
		return nil, err
	}
	active, err := a.activePaths(ctx, ch.rowID)
	if err != nil {
		return nil, err
	}
	dir, err := albumDestinationDir(remoteDir, active)
	if err != nil {
		return nil, err
	}

	members := make([]uploadMember, 0, len(localPaths))
	seen := map[string]bool{}
	for _, lp := range localPaths {
		// dir is "/" at the root; avoid joining to "//name".
		join := dir
		if dir != "/" {
			join += "/"
		}
		dest, err := fsmodel.NormalizeCanonicalPath(join + filepath.Base(lp))
		if err != nil {
			return nil, err
		}
		if seen[dest] {
			return nil, apperr.New(apperr.ErrUsage,
				fmt.Sprintf("two source files map to %q; album members need distinct basenames", dest))
		}
		seen[dest] = true
		// The one human caption renders on the first member only
		// (sendUnit); setting it on every member is harmless.
		members = append(members, uploadMember{localPath: lp, dest: dest, pres: pres, humanCaption: opts.Caption})
	}

	out, err := a.runUpload(ctx, uploadRun{members: members, policy: policy, noHash: noHash, album: true, opts: opts})
	if err != nil {
		return nil, err
	}
	data := &AlbumUploadResult{
		Albums:    out.albums,
		ChannelID: ch.tgIDStr,
		Errors:    []string{},
		Resumed:   out.resumed,
		Skipped:   len(out.skipped),
		Uploaded:  len(out.sent),
	}
	if link, err := a.TG.GetInviteLink(ctx, ch.tgID); err == nil {
		data.InviteLink = link
	}
	return data, nil
}

// albumDestinationDir validates the multi-file destination and returns its
// canonical form: "/" anywhere, a path with a trailing slash (directory
// declared), or an existing remote directory named without one. The canonical
// path never carries a trailing slash — fsmodel rejects those.
func albumDestinationDir(remoteDir string, active []fsmodel.ActivePath) (string, error) {
	trimmed := strings.TrimRight(remoteDir, "/")
	if trimmed == "" {
		return "/", nil
	}
	p, err := fsmodel.NormalizeCanonicalPath(trimmed)
	if err != nil {
		return "", err
	}
	if trimmed == remoteDir {
		// No trailing slash: the target must already exist as a remote dir,
		// so a meant-to-be-single-file destination cannot be silently
		// reinterpreted.
		isDir := false
		for _, ap := range active {
			if ap.IsDir && ap.Canonical == p {
				isDir = true
				break
			}
		}
		if !isDir {
			return "", apperr.New(apperr.ErrUsage,
				fmt.Sprintf("multi-file destination %q must be a directory (end the path with /)", remoteDir))
		}
	}
	return p, nil
}
