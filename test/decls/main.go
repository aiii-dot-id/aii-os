package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 || (len(os.Args) == 3 && os.Args[2] != "-pos" && os.Args[2] != "-text") {
		fmt.Fprintln(os.Stderr, "usage: decls <package dir> [-pos|-text]")
		os.Exit(2)
	}
	mode := ""
	if len(os.Args) == 3 {
		mode = os.Args[2]
	}
	out, err := list(os.Args[1], mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decls: %v\n", err)
		os.Exit(1)
	}
	for _, o := range out {
		fmt.Println(o)
	}
}

// list returns the package directory's declarations, sorted. mode is "",
// "-pos" or "-text".
func list(dir, mode string) ([]string, error) {
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		af, err := parser.ParseFile(fset, f, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, d := range af.Decls {
			s, e := fset.Position(d.Pos()), fset.Position(d.End())
			suffix := ""
			switch mode {
			case "-pos":
				suffix = fmt.Sprintf("\t%s:%d-%d", filepath.Base(f), s.Line, e.Line)
			case "-text":
				// the declaration's exact bytes, its doc comment included
				from := d.Pos()
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Doc != nil {
					from = fd.Doc.Pos()
				}
				if gd, ok := d.(*ast.GenDecl); ok && gd.Doc != nil {
					from = gd.Doc.Pos()
				}
				sum := sha256.Sum256(src[fset.Position(from).Offset:fset.Position(d.End()).Offset])
				suffix = "\t" + hex.EncodeToString(sum[:8])
			}
			switch d := d.(type) {
			case *ast.FuncDecl:
				recv := ""
				if d.Recv != nil {
					recv = types(d.Recv.List[0].Type) + "."
				}
				out = append(out, "func "+recv+d.Name.Name+suffix)
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch sp := sp.(type) {
					case *ast.TypeSpec:
						out = append(out, "type "+sp.Name.Name+suffix)
					case *ast.ValueSpec:
						for _, n := range sp.Names {
							out = append(out, d.Tok.String()+" "+n.Name+suffix)
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// types names a receiver's type: the pointer star kept, type parameters
// left out.
func types(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + types(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return types(t.X)
	case *ast.IndexListExpr:
		return types(t.X)
	}
	return "?"
}
