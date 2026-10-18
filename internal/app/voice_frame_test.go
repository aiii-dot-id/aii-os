package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// frameOf is an observation as a held final keeps it: decoded once.
func frameOf(ev pluginhost.Event) *voiceFrame {
	f := decodeVoiceFrame(ev)
	return &f
}

func TestNoReaderDecodesAnEventsRawAgain(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var decoders []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Unmarshal" {
				return true
			}
			if arg, ok := call.Args[0].(*ast.SelectorExpr); ok && arg.Sel.Name == "Raw" {
				decoders = append(decoders, fset.Position(call.Pos()).String())
			}
			return true
		})
	}
	if len(decoders) != 0 {
		t.Fatalf("an event's Raw is decoded again outside decodeVoiceFrame: %v", decoders)
	}
}
