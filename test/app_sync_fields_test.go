package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"testing"
)

var appSyncFields = map[string]string{
	// The core: the App's own lifecycle, identity custody and turn gate.
	"bgMu": "core", "bgWG": "core", "stopOnce": "core", "live": "core",
	"restartOnce": "core", "restarting": "core", "wakeMu": "core",
	"birthMu": "core", "keyOrigin": "core", "databaseView": "core",
	"cfgMu": "core (config)", "turnMu": "core (turn gate)",

	"audioOnce": "voice", "voiceSeq": "voice", "voiceModes": "voice", "voiceSessions": "voice",
	"voiceModePub": "voice", "voiceSpeakOffRev": "voice", "voicePending": "voice", "speakerPending": "voice",
	"voiceSafeDropped": "voice", "voiceFanDropped": "voice", "voiceStaleReplies": "voice",
	"speakerWithheldFinals": "voice", "speakerWithheldPartials": "voice", "spokenMu": "voice",
	"speechReserveMu": "voice", "voiceObsMu": "voice", "voiceReplyShown": "voice", "specMu": "voice",
}

// guard:tree
func TestAppTakesOnNoNewLockOrAtomic(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "app", "app.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var app *ast.StructType
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == "App" {
			app, _ = ts.Type.(*ast.StructType)
		}
		return app == nil
	})
	if app == nil {
		t.Fatal("no App struct in internal/app/app.go — the guard is not reading the root")
	}
	have := map[string]bool{}
	for _, field := range app.Fields.List {
		if !isSyncType(field.Type) {
			continue
		}
		for _, name := range field.Names {
			have[name.Name] = true
			if _, ok := appSyncFields[name.Name]; !ok {
				t.Errorf("App.%s is a new lock or atomic on the composition root; it belongs to the domain that owns its state, not App", name.Name)
			}
		}
	}
	var gone []string
	for name, owner := range appSyncFields {
		if !have[name] {
			gone = append(gone, name+" ("+owner+")")
		}
	}
	sort.Strings(gone)
	for _, g := range gone {
		t.Errorf("App.%s has left App — delete its line from appSyncFields", g)
	}
	if len(have) < 20 {
		t.Fatalf("read %d lock or atomic fields on App — the guard is not reading the struct", len(have))
	}
}

// isSyncType reports a sync.X or atomic.X type, generic or pointed to.
func isSyncType(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.StarExpr:
		return isSyncType(x.X)
	case *ast.IndexExpr:
		return isSyncType(x.X)
	case *ast.IndexListExpr:
		return isSyncType(x.X)
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			return pkg.Name == "sync" || pkg.Name == "atomic"
		}
	}
	return false
}
