// Package readwebdav serves a drive filesystem without exposing mutation methods.
package readwebdav

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path"

	"golang.org/x/net/webdav"
)

func Handler(filesystem func(context.Context) fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		ip, parseErr := netip.ParseAddr(host)
		if err != nil || parseErr != nil || !ip.IsLoopback() {
			http.Error(w, "literal loopback Host required", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		switch r.Method {
		case "OPTIONS":
			w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND")
			w.Header().Set("DAV", "1")
			w.WriteHeader(http.StatusNoContent)
			return
		case "GET", "HEAD", "PROPFIND":
		default:
			w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND")
			http.Error(w, "read-only drive", http.StatusMethodNotAllowed)
			return
		}
		handler := webdav.Handler{FileSystem: readFS{filesystem(r.Context())}, LockSystem: webdav.NewMemLS()}
		handler.ServeHTTP(w, r)
	})
}

type readFS struct{ fs.FS }

func (f readFS) Mkdir(context.Context, string, os.FileMode) error { return fs.ErrPermission }
func (f readFS) RemoveAll(context.Context, string) error          { return fs.ErrPermission }
func (f readFS) Rename(context.Context, string, string) error     { return fs.ErrPermission }
func (f readFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	file, err := http.FS(f.FS).Open(path.Clean("/" + name))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return file.Stat()
}

func (f readFS) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, fs.ErrPermission
	}
	file, err := http.FS(f.FS).Open(path.Clean("/" + name))
	if err != nil {
		return nil, err
	}
	return readFile{file}, nil
}

type readFile struct{ http.File }

func (f readFile) Write([]byte) (int, error) { return 0, fs.ErrPermission }
