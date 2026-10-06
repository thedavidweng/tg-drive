//go:build gui

package frontend

import (
	"bytes"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// The PDF preview runs PDFium from the embedded frontend, never a CDN: the
// built assets carry the WASM, and the viewer's code names that very file.
func TestEmbeddedAssetsBundlePDFium(t *testing.T) {
	assets := Assets()
	var wasm []string
	var scripts []string
	err := fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch name := path.Base(p); {
		case strings.HasPrefix(name, "pdfium") && strings.HasSuffix(name, ".wasm"):
			wasm = append(wasm, p)
		case strings.HasSuffix(name, ".js"):
			scripts = append(scripts, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(wasm) != 1 {
		t.Fatalf("want exactly one embedded PDFium WASM, found %v (build the frontend first)", wasm)
	}
	body, err := fs.ReadFile(assets, wasm[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte("\x00asm")) {
		t.Fatalf("%s is not a WebAssembly module", wasm[0])
	}

	name := []byte(path.Base(wasm[0]))
	for _, s := range scripts {
		js, err := fs.ReadFile(assets, s)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(js, name) {
			return
		}
	}
	t.Fatalf("no embedded script loads %s", name)
}
