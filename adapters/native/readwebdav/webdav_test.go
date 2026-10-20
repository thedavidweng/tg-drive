package readwebdav

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWebDAVReadOnlyProtocol(t *testing.T) {
	files := fstest.MapFS{"nested/file.txt": &fstest.MapFile{Data: []byte("0123456789"), Mode: 0o444}}
	server := httptest.NewServer(Handler(func(context.Context) fs.FS { return files }))
	defer server.Close()
	for _, test := range []struct {
		method, path, body, rangeHeader string
		code                            int
		want                            string
	}{
		{"GET", "/nested/file.txt", "", "", 200, "0123456789"},
		{"GET", "/nested/file.txt", "", "bytes=6-8", 206, "678"},
		{"HEAD", "/nested/file.txt", "", "", 200, ""},
		{"GET", "/missing", "", "", 404, ""},
		{"PROPFIND", "/nested/", "", "", 207, "file.txt"},
		{"PUT", "/nested/file.txt", "destroy", "", 405, "read-only"},
		{"DELETE", "/nested/file.txt", "", "", 405, "read-only"},
		{"MOVE", "/nested/file.txt", "", "", 405, "read-only"},
		{"MKCOL", "/new", "", "", 405, "read-only"},
		{"LOCK", "/nested/file.txt", "", "", 405, "read-only"},
	} {
		t.Run(test.method+test.path+test.rangeHeader, func(t *testing.T) {
			req, err := http.NewRequest(test.method, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", test.rangeHeader)
			if test.method == "PROPFIND" {
				req.Header.Set("Depth", "1")
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != test.code || !strings.Contains(string(body), test.want) {
				t.Fatalf("code=%d body=%s", res.StatusCode, body)
			}
		})
	}
	if string(files["nested/file.txt"].Data) != "0123456789" {
		t.Fatal("mutation leaked")
	}
}

func TestWebDAVHostAndBrowserBoundary(t *testing.T) {
	files := fstest.MapFS{"page.html": &fstest.MapFile{Data: []byte("<script>fetch('/secret')</script>")}}
	handler := Handler(func(context.Context) fs.FS { return files })
	request := httptest.NewRequest("GET", "http://attacker.example:8080/page.html", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("DNS rebinding Host accepted: %d", response.Code)
	}
	request = httptest.NewRequest("GET", "http://127.0.0.1:8080/page.html", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe browser response: %d %v", response.Code, response.Header())
	}
}
