package core

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Core is the domain layer: adapters, storage drivers, gotd/td, and the CLI
// framework depend on it, never the other way around.
var forbiddenImports = []string{
	"database/sql",
	"net/http",
	"prefix:github.com/gotd/td/",
	"modernc.org/sqlite",
	"github.com/spf13/cobra",
	"prefix:github.com/thedavidweng/tg-drive/adapters/",
	"prefix:github.com/thedavidweng/tg-drive/internal/",
}

func TestCorePackagesAvoidForbiddenImports(t *testing.T) {
	root := "."
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "testdata" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbiddenImports {
				if strings.HasPrefix(bad, "prefix:") {
					if strings.HasPrefix(p, strings.TrimPrefix(bad, "prefix:")) {
						t.Errorf("%s imports forbidden package %s", path, p)
					}
					continue
				}
				if p == bad {
					t.Errorf("%s imports forbidden package %s", path, bad)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
