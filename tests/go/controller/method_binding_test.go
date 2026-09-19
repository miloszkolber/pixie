package controller_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/piprotocol"
)

// TestControllerCallSitesAreInHostCatalog binds the controller's host method
// names to the generated host catalog. A rename or typo in a controller call
// site fails here instead of only against a running host.
func TestControllerCallSitesAreInHostCatalog(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "internal", "controller")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "CallPi", "CallPiUntilDone", "CallExtension", "CallExtensionUntilDone":
			default:
				return true
			}
			if len(call.Args) < 2 {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			method := strings.Trim(literal.Value, `"`)
			if !strings.Contains(method, ".") {
				return true
			}
			if _, seen := found[method]; !seen {
				found[method] = name
			}
			return true
		})
	}
	if len(found) == 0 {
		t.Fatal("no literal controller host call sites found; the binding test is not exercising anything")
	}
	for method, file := range found {
		if !piprotocol.CatalogHostOperationSet[method] {
			t.Errorf("controller call %q in %s is absent from the generated host catalog", method, file)
		}
	}
}
