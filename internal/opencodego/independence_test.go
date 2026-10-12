package opencodego

import (
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Provider-specific transport and wire handling must remain self-contained.
// Reject native provider packages and third-party inference SDKs at the import
// boundary instead of relying only on code-review convention.
func TestSDKImportsOnlyStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := build.Default.Import(path, "", build.FindOnly)
			if err != nil || !pkg.Goroot {
				t.Errorf("%s imports non-standard package %q", name, path)
			}
		}
	}
}
