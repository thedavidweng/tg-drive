package commands

import (
	"fmt"
	"os"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/transfer"
)

// File transfer commands (cp/get/mv/rm/share) and their shared
// conflict-policy and presentation flag helpers.

func conflictPolicy(replace, skip, autoRename bool) (service.ConflictPolicy, error) {
	n := 0
	if replace {
		n++
	}
	if skip {
		n++
	}
	if autoRename {
		n++
	}
	if n > 1 {
		return "", apperr.New(apperr.ErrFlagConflict, "only one conflict flag allowed")
	}
	if replace {
		return service.ConflictReplace, nil
	}
	if skip {
		return service.ConflictSkip, nil
	}
	if autoRename {
		return service.ConflictRename, nil
	}
	return service.ConflictFail, nil
}

// presentationFlags assembles the typed-upload flags into a Presentation,
// rejecting invalid values and combinations up front so usage errors never
// depend on app context.
func presentationFlags(kind string, duration float64, width, height int, streaming bool, thumb string) (service.Presentation, error) {
	pres := service.Presentation{
		Kind:              kind,
		DurationSeconds:   duration,
		Width:             width,
		Height:            height,
		SupportsStreaming: streaming,
		ThumbPath:         thumb,
	}
	if err := pres.Validate(); err != nil {
		return service.Presentation{}, err
	}
	return pres, nil
}

// presentationFlagsSet reports whether any typed-upload flag was passed
// explicitly, so they cannot silently no-op on --recursive uploads.
func presentationFlagsSet(c *cobra.Command) bool {
	for _, name := range []string{"as", "duration", "width", "height", "streaming", "thumb"} {
		if c.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func NewCpCmd(rt Runtime) *cobra.Command {
	var recursive, replace, skip, autoRename, noHash, continueOnError, includeEmptyDirs bool
	var uploadThreads, uploadPartSizeKB int
	var confirm, dryRun, events bool
	var asKind, thumbPath string
	var duration float64
	var width, height int
	var streaming bool
	c := &cobra.Command{
		Use:   "cp <local> [local...] <remote-path>",
		Short: "Upload local file, directory, or multi-file album",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			policy, err := conflictPolicy(replace, skip, autoRename)
			if err != nil {
				return r.Error(err)
			}
			pres, err := presentationFlags(asKind, duration, width, height, streaming, thumbPath)
			if err != nil {
				return r.Error(err)
			}
			if dryRun {
				plan := service.PlanUpload(args[:len(args)-1], args[len(args)-1], policy)
				if events {
					return r.Event("cp.dry-run", plan)
				}
				return r.Success(plan)
			}
			opts := service.UploadOptions{ConfirmReplace: confirm}
			if err := opts.Validate(policy); err != nil {
				return r.Error(err)
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if cmd.Flags().Changed("upload-threads") {
				opts.Threads = uploadThreads
			}
			if cmd.Flags().Changed("upload-part-size-kb") {
				opts.PartSizeKB = uploadPartSizeKB
			}
			if events {
				opts.Observer.OnProgress = func(p service.Progress) {
					if p.Part == nil {
						return
					}
					_ = r.Event("cp.progress", telegram.UploadProgressState{
						FileName: p.Part.FileName,
						Part:     p.Part.Index,
						PartSize: p.Part.Size,
						Uploaded: p.Done,
						Total:    p.Total,
					})
				}
			}
			if len(args) > 2 {
				if recursive {
					return r.Error(apperr.New(apperr.ErrUsage, "--recursive accepts exactly one source directory"))
				}
				if includeEmptyDirs {
					return r.Error(apperr.New(apperr.ErrUsage, "--include-empty-dirs requires --recursive"))
				}
				data, err := app.UploadFilesAs(cmd.Context(), args[:len(args)-1], args[len(args)-1], policy, noHash, pres, opts)
				if err != nil {
					return r.Error(err)
				}
				if events {
					return r.Event("cp", data)
				}
				if !rt.JSON() {
					_ = r.SuccessLine("uploaded %v files in %v album(s)", data.Uploaded, len(data.Albums))
					if data.InviteLink != "" {
						return r.SuccessLine("invite: %s", data.InviteLink)
					}
					return nil
				}
				return r.Success(data)
			}
			if recursive {
				if presentationFlagsSet(cmd) {
					return r.Error(apperr.New(apperr.ErrUsage, "presentation flags apply to single-file uploads only"))
				}
				data, err := app.UploadRecursive(cmd.Context(), args[0], args[1], policy, continueOnError, noHash, includeEmptyDirs, opts)
				if err != nil {
					return r.Error(err)
				}
				if events {
					return r.Event("cp", data)
				}
				if !rt.JSON() {
					_ = r.SuccessLine("uploaded %v files (%v skipped, %v failed)", data.Uploaded, data.Skipped, data.Failed)
					if data.InviteLink != "" {
						return r.SuccessLine("invite: %s", data.InviteLink)
					}
					return nil
				}
				return r.Success(data)
			}
			if includeEmptyDirs {
				return r.Error(apperr.New(apperr.ErrUsage, "--include-empty-dirs requires --recursive"))
			}
			var observer transfer.Observer
			if events {
				observer.OnStage = func(t transfer.Transfer) { _ = r.Event("transfer.stage", t) }
			}
			manager := transfer.New(app, transfer.Options{FrontEnd: transfer.FrontEndCLI, Observer: observer})
			handle, err := manager.SubmitUpload(cmd.Context(), transfer.Upload{
				Source: args[0], Dest: args[1], Policy: policy, NoHash: noHash, Presentation: pres, Options: opts,
			})
			if err != nil {
				return r.Error(err)
			}
			data, err := handle.Wait()
			if err != nil {
				return r.Error(err)
			}
			if events {
				return r.Event("cp", data)
			}
			if !rt.JSON() {
				if data.Skipped {
					return r.SuccessLine("skipped %s (already exists; use --replace to overwrite)", data.Path)
				}
				verb := "uploaded"
				if data.Resumed {
					verb = "resumed upload of"
				}
				_ = r.SuccessLine("%s %s (%s)", verb, data.Path, humanSize(data.Size))
				if data.InviteLink != "" {
					return r.SuccessLine("invite: %s", data.InviteLink)
				}
				return nil
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVarP(&recursive, "recursive", "r", false, "upload directory recursively")
	c.Flags().BoolVar(&replace, "replace", false, "replace existing remote file")
	c.Flags().BoolVar(&skip, "skip-existing", false, "skip existing remote file")
	c.Flags().BoolVar(&autoRename, "auto-rename", false, "auto rename on conflict")
	c.Flags().BoolVar(&noHash, "no-hash", false, "skip content hash")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue on upload errors")
	c.Flags().BoolVar(&includeEmptyDirs, "include-empty-dirs", false, "include empty directories (unsupported in V1)")
	c.Flags().IntVar(&uploadThreads, "upload-threads", 0, "parallel upload goroutines (0 uses config)")
	c.Flags().IntVar(&uploadPartSizeKB, "upload-part-size-kb", 0, "upload part size in KB (0 uses config)")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm destructive operations such as --replace")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "preview the upload plan without executing")
	c.Flags().BoolVar(&events, "events", false, "emit NDJSON progress events during upload")
	c.Flags().StringVar(&asKind, "as", "", "present the upload as photo, video, or document (default document)")
	c.Flags().Float64Var(&duration, "duration", 0, "video duration in seconds (with --as video)")
	c.Flags().IntVar(&width, "width", 0, "video width in pixels (with --as video)")
	c.Flags().IntVar(&height, "height", 0, "video height in pixels (with --as video)")
	c.Flags().BoolVar(&streaming, "streaming", false, "mark the video as streamable (with --as video)")
	c.Flags().StringVar(&thumbPath, "thumb", "", "JPEG file to attach as the upload thumbnail")
	return c
}

func NewGetCmd(rt Runtime) *cobra.Command {
	var recursive, replace, skip, autoRename, continueOnError bool
	c := &cobra.Command{
		Use:   "get <remote-path> <local-dest>",
		Short: "Download remote file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			policy, err := conflictPolicy(replace, skip, autoRename)
			if err != nil {
				return r.Error(err)
			}
			if recursive {
				data, err := app.DownloadRecursive(cmd.Context(), args[0], args[1], policy, continueOnError, service.DownloadOptions{})
				if err != nil {
					return r.Error(err)
				}
				if !rt.JSON() {
					_ = r.SuccessLine("downloaded %s -> %s (%v files, %v skipped, %v failed)", args[0], args[1], data.Downloaded, data.Skipped, data.Failed)
					return nil
				}
				return r.Success(data)
			}
			res, err := app.DownloadFile(cmd.Context(), args[0], args[1], policy, service.DownloadOptions{})
			if err != nil {
				return r.Error(err)
			}
			if !rt.JSON() {
				if res.Skipped {
					return r.SuccessLine("skipped %s (already exists; use --replace to overwrite)", res.Dest)
				}
				return r.SuccessLine("downloaded %s -> %s (%s)", res.Path, res.Dest, humanSize(res.Size))
			}
			return r.Success(res)
		},
	}
	c.Flags().BoolVarP(&recursive, "recursive", "r", false, "download directory recursively")
	c.Flags().BoolVar(&replace, "replace", false, "replace existing local file")
	c.Flags().BoolVar(&skip, "skip-existing", false, "skip existing local file")
	c.Flags().BoolVar(&autoRename, "auto-rename", false, "auto rename on conflict")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue on download errors")
	return c
}

func NewMvCmd(rt Runtime) *cobra.Command {
	var confirm, dryRun bool
	c := &cobra.Command{
		Use:   "mv <remote-from> <remote-to>",
		Short: "Move or rename remote file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if dryRun {
				return r.Success(map[string]any{"would_move": args[0], "to": args[1]})
			}
			opts := service.MoveOptions{Confirm: confirm}
			if err := opts.Validate(); err != nil {
				return r.Error(err)
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.MoveFile(cmd.Context(), args[0], args[1], opts); err != nil {
				return r.Error(err)
			}
			if !rt.JSON() {
				return r.SuccessLine("moved %s -> %s", args[0], args[1])
			}
			return r.Success(map[string]string{"from": args[0], "to": args[1]})
		},
	}
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm the move")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "preview what would be moved")
	return c
}

func NewRmCmd(rt Runtime) *cobra.Command {
	var tombstone, allowStaleManifest, confirm, dryRun bool
	c := &cobra.Command{
		Use:   "rm <remote-path>",
		Short: "Delete remote file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if dryRun {
				return r.Success(map[string]any{"would_delete": args[0], "tombstone": tombstone})
			}
			opts := service.DeleteOptions{
				Tombstone:          tombstone,
				AllowStaleManifest: allowStaleManifest,
				Confirm:            confirm,
			}
			if err := opts.Validate(); err != nil {
				return r.Error(err)
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.DeleteFile(cmd.Context(), args[0], opts)
			if err != nil {
				return r.Error(err)
			}
			if !rt.JSON() {
				if data.StaleManifest {
					fmt.Fprintln(os.Stderr, "warning: manifest reply could not be redacted and remains on Telegram")
				}
				if data.Mode == "tombstone" {
					return r.SuccessLine("tombstoned %s (hidden from td ls; the Telegram message is kept with a tombstone caption)", args[0])
				}
				return r.SuccessLine("deleted %s", args[0])
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&tombstone, "tombstone", false, "tombstone instead of deleting the Telegram message")
	c.Flags().BoolVar(&allowStaleManifest, "allow-stale-manifest", false, "do not fail when the manifest reply cannot be redacted")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm the deletion")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "preview what would be deleted")
	return c
}

func NewShareCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "share [remote-path]",
		Short: "Share invite link and optional legacy hashtag filter",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			data, err := app.Share(cmd.Context(), p)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if data.Channel != "" {
				_, _ = fmt.Fprintf(out, "Channel: %s\n", data.Channel)
			}
			_, _ = fmt.Fprintf(out, "Invite: %v\n", data.InviteLink)
			if data.Hashtag != "" {
				_, _ = fmt.Fprintf(out, "Legacy filter: %s\n", data.Hashtag)
				_, _ = fmt.Fprintln(out, "\nThis filter only matches legacy posts that still carry path hashtags. New posts use human-only captions; open the channel or use td ls/tree to browse.")
			} else {
				_, _ = fmt.Fprintln(out, "\nOpen the channel from the invite link; it contains everything shared here.")
			}
			return nil
		},
	}
}
