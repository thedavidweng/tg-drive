package commands

import (
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/adapters/native/fileperm"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func NewArchiveCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{Use: "archive", Short: "Create and restore client-encrypted age archives"}
	GroupUsage(rt, c)
	keygen := &cobra.Command{Use: "keygen <identity-file>", Short: "Exclusively create a private X25519 identity", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		r := rt.Renderer()
		identity, err := age.GenerateX25519Identity()
		if err != nil {
			return r.Error(err)
		}
		file, err := fileperm.CreatePrivate(args[0])
		if err != nil {
			if os.IsExist(err) {
				return r.Error(apperr.New(apperr.ErrLocalPathExists, "identity file already exists"))
			}
			return r.Error(err)
		}
		_, writeErr := file.WriteString(identity.String() + "\n")
		closeErr := file.Close()
		if writeErr != nil {
			_ = os.Remove(args[0])
			return r.Error(writeErr)
		}
		if closeErr != nil {
			_ = os.Remove(args[0])
			return r.Error(closeErr)
		}
		return r.Success(map[string]string{"identity_file": args[0], "recipient": identity.Recipient().String()})
	}}
	var recipient string
	put := &cobra.Command{Use: "put <local-directory> <remote-file>", Short: "Encrypt names and bytes before uploading one immutable archive", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		r := rt.Renderer()
		if _, err := age.ParseX25519Recipient(recipient); err != nil {
			return r.Error(apperr.Wrap(apperr.ErrUsage, "--recipient requires an age X25519 public recipient", err))
		}
		app, cleanup, err := rt.OpenApp(cmd)
		if err != nil {
			return r.Error(err)
		}
		defer cleanup()
		result, err := app.UploadArchive(cmd.Context(), args[0], args[1], recipient)
		if err != nil {
			return r.Error(err)
		}
		return r.Success(result)
	}}
	put.Flags().StringVar(&recipient, "recipient", "", "age X25519 public recipient (required)")
	var identityFile string
	restore := &cobra.Command{Use: "restore <remote-file> <new-local-directory>", Short: "Verify the entire archive before atomically publishing a new private tree", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		r := rt.Renderer()
		file, err := fileperm.OpenPrivate(identityFile)
		if err != nil {
			return r.Error(apperr.Wrap(apperr.ErrUsage, "read private --identity", err))
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 1024))
		closeErr := file.Close()
		if readErr != nil {
			return r.Error(readErr)
		}
		if closeErr != nil {
			return r.Error(closeErr)
		}

		identity, err := age.ParseX25519Identity(strings.TrimSpace(string(data)))
		if err != nil {
			return r.Error(apperr.Wrap(apperr.ErrUsage, "parse X25519 identity", err))
		}
		app, cleanup, err := rt.OpenApp(cmd)
		if err != nil {
			return r.Error(err)
		}
		defer cleanup()
		result, err := app.RestoreArchive(cmd.Context(), args[0], args[1], identity)
		if err != nil {
			return r.Error(err)
		}
		return r.Success(result)
	}}
	restore.Flags().StringVar(&identityFile, "identity", "", "private X25519 identity file (required)")
	c.AddCommand(keygen, put, restore)
	return c
}
