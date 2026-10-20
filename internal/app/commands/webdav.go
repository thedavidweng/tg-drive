package commands

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive/adapters/native/readwebdav"
	apperr "github.com/thedavidweng/tg-drive/core/errors"
)

func NewWebDAVCmd(rt Runtime) *cobra.Command {
	address := "127.0.0.1:8080"
	c := &cobra.Command{Use: "webdav [remote-directory]", Short: "Serve a pinned read-only drive subtree over loopback WebDAV", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r := rt.Renderer()
		bind, err := netip.ParseAddrPort(address)
		if err != nil || !bind.Addr().IsLoopback() {
			return r.Error(apperr.New(apperr.ErrUsage, "WebDAV listen address must be a literal loopback IP and port"))
		}
		app, cleanup, err := rt.OpenApp(cmd)
		if err != nil {
			return r.Error(err)
		}
		defer cleanup()
		remote := "/"
		if len(args) == 1 {
			remote = args[0]
		}
		tree, err := app.OpenReadTree(cmd.Context(), remote)
		if err != nil {
			return r.Error(err)
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return r.Error(err)
		}
		defer func() { _ = listener.Close() }()
		server := &http.Server{Handler: readwebdav.Handler(func(ctx context.Context) fs.FS { return tree.WithContext(ctx) }), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return cmd.Context() }}
		if err := r.Success(map[string]any{"address": listener.Addr().String(), "remote_root": remote, "read_only": true}); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return r.Error(err)
		case <-cmd.Context().Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				return r.Error(err)
			}
			<-done
			return r.Error(apperr.New(apperr.ErrCancelled, "WebDAV stopped"))
		}
	}}
	c.Flags().StringVar(&address, "listen", address, "literal loopback IP:port (port 0 selects an available port)")
	return c
}
