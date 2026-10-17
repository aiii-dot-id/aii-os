package store

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSafeRefusesEveryWriteTheReviewFound(t *testing.T) {
	for _, tc := range []struct {
		what  string
		write func(s *Store, nth string) error
	}{
		{"a message sent", func(s *Store, nth string) error {
			return s.AddOutboxMessage("m"+nth, "operator", "", "while frozen", nil)
		}},
		{"a work session started", func(s *Store, nth string) error {
			return s.StartWorkSession(t.Context(), "w"+nth, "while frozen")
		}},
		{"a skill proposed", func(s *Store, nth string) error {
			_, err := s.ProposeSkill("t"+nth, "d", "e")
			return err
		}},
		{"a ranked recall's reinforcement", func(s *Store, nth string) error {
			return s.RecordMemoryAccess(time.Now(), MemoryRef{Store: "beliefs", ID: "b" + nth})
		}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := testStore(t)
			if err := tc.write(s, "1"); err != nil {
				t.Fatalf("rig: %s fails before any freeze: %v", tc.what, err)
			}
			s.SetFrozen(true, "an integrity failure the operator is reading")
			err := tc.write(s, "2")
			var frozen *FrozenError
			if !errors.As(err, &frozen) {
				t.Fatalf("SAFE DID NOT REFUSE %s: %v", tc.what, err)
			}
			if frozen.Reason != "an integrity failure the operator is reading" {
				t.Errorf("the refusal does not carry SAFE's own reason: %q", frozen.Reason)
			}
			// And it SAYS it: the sentence reaches a person, who needs to
			// know both that the write was refused and why the identity is
			// in the state that refused it.
			if !strings.Contains(err.Error(), "an integrity failure the operator is reading") {
				t.Errorf("the refusal's own words drop the reason: %q", err.Error())
			}
			if !strings.Contains(err.Error(), "queryable") {
				t.Errorf("the refusal does not say the projection is still readable: %q", err.Error())
			}
			// And it goes again when the freeze lifts: SAFE is a posture,
			// not damage.
			s.SetFrozen(false, "")
			if err := tc.write(s, "3"); err != nil {
				t.Errorf("%s still refused after the freeze lifted: %v", tc.what, err)
			}
		})
	}
}

// A frozen projection remains queryable so the operator can inspect the last
// known-good state without allowing it to change during that inspection.
func TestSafeLeavesTheProjectionReadable(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession(t.Context(), "w1", "before the freeze"); err != nil {
		t.Fatal(err)
	}
	s.SetFrozen(true, "reading the evidence")
	// A single-row read...
	got, err := s.WorkSessionByID("w1")
	if err != nil {
		t.Fatalf("SAFE made a single-row read fail: %v", err)
	}
	if got == nil || got.Description != "before the freeze" {
		t.Fatalf("what was written before the freeze cannot be read during it: %+v", got)
	}
	// ...and a MANY-row read, which is a different method on the handle and
	// would be gated separately if anyone gated reads by mistake.
	if _, err := s.OutboxOutcomesSince(0); err != nil {
		t.Fatalf("SAFE made a multi-row read fail: %v", err)
	}
	if _, _, err := s.RecentPlan(); err != nil {
		t.Fatalf("SAFE made a plan read fail: %v", err)
	}
	// ...and a read that goes through the GUARDED handle itself, which is
	// the one a mistake would gate: everything above reaches the pool
	// directly, so none of them would notice if reads were refused here.
	if _, err := s.NextConversation(1000, ""); err != nil {
		t.Fatalf("SAFE refused a read through the guarded handle: %v", err)
	}
}

func TestATransactionOpenWhenSafeArrivesMayFinish(t *testing.T) {
	s := testStore(t)
	tx, err := s.w().Begin()
	if err != nil {
		t.Fatal(err)
	}
	s.SetFrozen(true, "arrived mid-transaction")
	if _, err := tx.Exec(`INSERT INTO work_sessions (id, description, status, state, project_id) VALUES ('t1','in flight','active','','')`); err != nil {
		tx.Rollback()
		t.Fatalf("a transaction open when SAFE arrived was torn in half: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("it could not finish: %v", err)
	}
	// And nothing new begins.
	if _, err := s.w().Begin(); !errors.As(err, new(*FrozenError)) {
		t.Fatalf("a NEW transaction began while frozen: %v", err)
	}
}

func TestAFrozenProjectionClaimsNothing(t *testing.T) {
	s := testStore(t)
	if _, err := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-handle", Source: "time"}); err != nil {
		t.Fatal(err)
	}
	s.SetFrozen(true, "fixture SAFE")
	if item, err := s.ClaimWork(nil, time.Now().UnixMilli()); err != nil || item != nil {
		t.Fatalf("a frozen projection handed out work: %+v %v", item, err)
	}
	s.SetFrozen(false, "")
	if n, err := s.PendingWorkCount(); err != nil || n != 1 {
		t.Fatalf("the row did not stay pending: %d %v", n, err)
	}
	// Thawed, the same row is claimed: the refusal was the freeze.
	if item, err := s.ClaimWork(nil, time.Now().UnixMilli()); err != nil || item == nil || item.DedupKey != "frozen-handle" {
		t.Fatalf("thawed claim: %+v %v", item, err)
	}
}

// rawWrites identifies direct raw-pool writes by syntax, including DML sent
// through a query method. Comments cannot authorize a bypass. The one indirect
// posture write is checked separately against its exact function and caller.
func rawWrites(source string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	dml := regexp.MustCompile(`(?i)^\s*(INSERT|UPDATE|DELETE|REPLACE)\b`)
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !freezeRawDB(method.X) {
			return true
		}
		write := false
		switch method.Sel.Name {
		case "Exec", "ExecContext", "Begin", "BeginTx":
			write = true
		case "Query", "QueryRow", "QueryContext", "QueryRowContext":
			arg := 0
			if strings.HasSuffix(method.Sel.Name, "Context") {
				arg = 1
			}
			if len(call.Args) > arg {
				query := call.Args[arg]
				// The first literal of a concatenated query still names its verb.
				for {
					part, ok := query.(*ast.BinaryExpr)
					if !ok || part.Op != token.ADD {
						break
					}
					query = part.X
				}
				if lit, ok := query.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					text, _ := strconv.Unquote(lit.Value)
					write = dml.MatchString(text)
				}
			}
		}
		if write {
			found = append(found, fmt.Sprintf("%d: %s", fset.Position(call.Pos()).Line, freezeSourceExpr(call)))
		}
		return true
	})
	return found, nil
}

func freezeRawDB(expr ast.Expr) bool {
	field, ok := ast.Unparen(expr).(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "db" {
		return false
	}
	_, ok = ast.Unparen(field.X).(*ast.Ident)
	return ok
}

func freezeSourceExpr(n ast.Node) string {
	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), n); err != nil {
		panic(err) // every node comes from a successfully parsed Go file
	}
	return b.String()
}

// postureWriteSites identifies the raw metadata write and every use of its
// bypass helper. Exact expressions bind the exception to continuity status;
// adding a comment, another raw helper call or a new caller cannot pass it.
func postureWriteSites(source string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "store.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		raw := false
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			raw = fun.Name == "setRuntimeMeta" && len(call.Args) > 0 && freezeRawDB(call.Args[0])
		case *ast.SelectorExpr:
			raw = fun.Sel.Name == "postureMeta"
		}
		if !raw {
			return true
		}
		owner := "<package>"
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || call.Pos() < fn.Pos() || call.End() > fn.End() {
				continue
			}
			owner = fn.Name.Name
			if fn.Recv != nil {
				owner = "(" + freezeSourceExpr(fn.Recv.List[0].Type) + ")." + owner
			}
			break
		}
		found = append(found, owner+": "+freezeSourceExpr(call))
		return true
	})
	return found, nil
}

// Negative controls ensure the guard detects writes even when the production
// source contains no violation. Non-code and guarded operations are controls too.
func TestTheRawHandleGuardCatchesARawWrite(t *testing.T) {
	for _, bad := range []string{
		"\t_, err := s.db.Exec(\"INSERT INTO t VALUES (1)\")",
		"\ttx, err := s.db.Begin()",
		"\t// posture write: a comment cannot authorize a raw write\n\t_, err := s.db.Exec(\"INSERT\")",
		"\terr = s.db.QueryRow(\n\t\t`UPDATE t SET a = 1\n\t\t WHERE id = ? RETURNING a`, id).Scan(&a)",
		"\trows, err := s.db.QueryContext(ctx, \"delete FROM t RETURNING id\")",
		"\t_, err := other.db.\nExecContext(requestCtx, \"INSERT INTO t VALUES (1)\")",
		"\t_, err := (s.db).BeginTx(requestCtx, nil)",
		"\trow := s.db.QueryRowContext(requestCtx, \"UPDATE/* fixture */ t SET a=1 RETURNING a\")",
		"\trows, err := s.db.Query(\"DELETE FROM \" + table + \" RETURNING id\")",
	} {
		if got, err := rawWrites("package store\nfunc fixture() {\n" + bad + "\n}"); err != nil || len(got) != 1 {
			t.Errorf("a raw write went unseen: %q -> %v, %v", bad, got, err)
		}
	}
	for _, ok := range []string{
		"\t// s.db.Exec(\"INSERT\") is not executable code",
		`_ = "s.db.Exec(\"INSERT\")"`,
		"\t_, err := s.w().Exec(\"INSERT INTO t VALUES (1)\")",
		"\trows, err := s.db.Query(\"SELECT 1\")",
		"\terr := s.db.QueryRow(`SELECT updated_seq FROM t WHERE kind = 'update'`).Scan(&n)",
		"\terr := s.db.QueryRow(q, \"update\").Scan(&n)",
		"\terr := tx.QueryRow(`UPDATE t SET a = 1 RETURNING a`).Scan(&a)",
	} {
		if got, err := rawWrites("package store\nfunc fixture() {\n" + ok + "\n}"); err != nil || len(got) != 0 {
			t.Errorf("a guarded write, a read or non-code was reported: %q -> %v, %v", ok, got, err)
		}
	}
}

// Direct writes must use the guarded handle, regardless of comments or layout.
func TestNoWriteTakesTheRawHandle(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("only %d files: this guard is not looking at the store", len(files))
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		writes, err := rawWrites(string(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, where := range writes {
			t.Errorf("%s:%s\n  writes through the raw pool, which SAFE cannot freeze. Use s.w() (or s.h() to\n"+
				"  ride an open transaction); both refuse while the projection is frozen.", name, where)
		}
	}
}

// Only continuity status may use the raw posture-metadata path. Its recorded
// outcome remains writable while identity state remains frozen.
func TestOnlyOnePostureWriteBypassesTheFreeze(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		found, err := postureWriteSites(string(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sites = append(sites, found...)
	}
	want := []string{
		"(*Store).SetContinuityStatus: s.postureMeta(ContinuityStatusKey, string(raw))",
		"(*Store).postureMeta: setRuntimeMeta(s.db, key, value)",
	}
	if !reflect.DeepEqual(sites, want) {
		t.Errorf("raw posture write and its only caller changed:\n got %v\nwant %v", sites, want)
	}
	// And it is the one it is meant to be.
	s := testStore(t)
	s.SetFrozen(true, "an integrity failure")
	if err := s.SetContinuityStatus(ContinuityStatus{Outcome: ContinuitySafe}); err != nil {
		t.Fatalf("the host could not record its own conduct while frozen: %v", err)
	}
	got, ok, err := s.ContinuityStatus()
	if err != nil || !ok || got.Outcome != ContinuitySafe {
		t.Fatalf("what it wrote did not land: %+v %v %v", got, ok, err)
	}
	// While the identity's own state stays frozen beside it.
	if err := s.StartWorkSession(t.Context(), "w9", "while frozen"); !errors.As(err, new(*FrozenError)) {
		t.Errorf("the posture exception did not preserve the typed freeze refusal: %v", err)
	}
}

// The structural exception must fail if its write or its caller moves, duplicates,
// disappears or targets a different key. Comments do not affect the result.
func TestPostureWriteGuardRejectsChangedBypasses(t *testing.T) {
	const valid = `package store
func (s *Store) SetContinuityStatus(st ContinuityStatus) error {
	return s.postureMeta(ContinuityStatusKey, string(raw))
}
func (s *Store) postureMeta(key, value string) error {
	return setRuntimeMeta(s.db, key, value)
}
`
	want := []string{
		"(*Store).SetContinuityStatus: s.postureMeta(ContinuityStatusKey, string(raw))",
		"(*Store).postureMeta: setRuntimeMeta(s.db, key, value)",
	}
	for _, source := range []string{valid, strings.Replace(valid, "return setRuntimeMeta", "// posture write: annotation is irrelevant\nreturn setRuntimeMeta", 1)} {
		got, err := postureWriteSites(source)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("valid bypass was not identified: %v, %v", got, err)
		}
	}
	for name, source := range map[string]string{
		"missing raw write": strings.Replace(valid, "setRuntimeMeta(s.db, key, value)", "setRuntimeMeta(s.w(), key, value)", 1),
		"moved raw write":   strings.Replace(valid, "func (s *Store) postureMeta", "func (s *Store) otherMeta", 1),
		"second raw write":  valid + "func (s *Store) otherMeta() error { return setRuntimeMeta(s.db, key, value) }",
		"package raw write": valid + "var err = setRuntimeMeta(s.db, key, value)",
		"wrong receiver":    strings.Replace(valid, "func (s *Store) postureMeta", "func (s *Other) postureMeta", 1),
		"wrong key":         strings.Replace(valid, "ContinuityStatusKey", "activeProjectKey", 1),
		"different caller":  strings.Replace(valid, "SetContinuityStatus", "SetOtherStatus", 1),
		"second caller":     valid + "func (s *Store) other() error { return s.postureMeta(activeProjectKey, value) }",
		"no caller":         strings.Replace(valid, "s.postureMeta(ContinuityStatusKey, string(raw))", "nil", 1),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := postureWriteSites(source)
			if err != nil {
				t.Fatalf("invalid fixture: %v", err)
			}
			if reflect.DeepEqual(got, want) {
				t.Fatalf("changed bypass escaped the guard: %v", got)
			}
		})
	}
	for name, inspect := range map[string]func(string) ([]string, error){"direct": rawWrites, "posture": postureWriteSites} {
		if _, err := inspect("package store\nfunc broken("); err == nil {
			t.Errorf("%s guard accepted an unreadable source file", name)
		}
	}
}
