//go:build !linux && !darwin

package commands

import (
	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func NewMountCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{Use: "mount <remote-directory> <local-mountpoint>", Short: "Native FUSE mount (Linux/macOS only)", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, _ []string) error {
		return rt.Renderer().Error(apperr.New(apperr.ErrUsage, "native FUSE mount requires Linux or macOS and an installed FUSE driver"))
	}}
}
