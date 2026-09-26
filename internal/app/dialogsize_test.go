//go:build linux

package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A GtkScrolledWindow reports a natural height of nothing: it is content with
// any size and expects to be told one. So a dialog whose content is scrolled
// and which sets no content height presents as a title bar, the first row of
// its form, and nothing else. It is not subtle when you see it and it is
// invisible when reading the code, because the line that is wrong is the line
// that is missing.
//
// It shipped in the world editor and, unnoticed, in the scene direction dialog
// as well. Ten other dialogs happened to set a height and were fine. This is
// the difference between those two groups, written down.
func TestEveryScrolledDialogHasAHeight(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := callsIn(fn.Body)
			if !calls["adw.NewDialog"] || !calls["scrolled"] {
				continue
			}
			checked++
			if calls["SetContentHeight"] || calls["scrolledToFit"] {
				continue
			}
			t.Errorf("%s: %s presents a scrolled page in a dialog with no height, "+
				"so it will render as its title bar and one row. Give the dialog a "+
				"SetContentHeight, or use scrolledToFit to size it to its content.",
				fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
	// If the shape of this code changes so that nothing matches, the test
	// passes by looking at nothing, which is worse than failing.
	if checked < 5 {
		t.Fatalf("only found %d scrolled dialogs; the check is no longer finding them", checked)
	}
}

// callsIn returns the names of the functions and methods called in a body,
// methods by their selector alone so that a receiver's name does not matter.
func callsIn(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			out[fn.Name] = true
		case *ast.SelectorExpr:
			out[fn.Sel.Name] = true
			if pkg, ok := fn.X.(*ast.Ident); ok {
				out[pkg.Name+"."+fn.Sel.Name] = true
			}
		}
		return true
	})
	return out
}
