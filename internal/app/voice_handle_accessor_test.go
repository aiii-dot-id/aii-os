package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceSessionsAreReadByIDOnlyThroughTheAccessor(t *testing.T) {
	a := &App{}
	h := &voiceHandle{id: "vs-held"}
	a.voiceSessions.Store(h.id, h)
	if got := a.voiceHandle("vs-held"); got != h {
		t.Fatalf("voiceHandle(held) = %p, want %p", got, h)
	}
	if got := a.voiceHandle("vs-never"); got != nil {
		t.Fatalf("voiceHandle(never opened) = %p, want nil", got)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var readers []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Load" {
					return true
				}
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "voiceSessions" {
					readers = append(readers, fset.Position(call.Pos()).String()+" in "+fn.Name.Name)
				}
				return true
			})
		}
	}
	if len(readers) != 1 || !strings.HasSuffix(readers[0], " in voiceHandle") {
		t.Fatalf("voiceSessions is read by id outside voiceHandle(id): %v", readers)
	}
}
