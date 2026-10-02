package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type SidecarState struct {
	Name    string
	Base    string
	Indexed int64
	Rows    int64
}

func (st SidecarState) Ready() bool { return st.Indexed == st.Rows }

type sidecarQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func sidecarState(q sidecarQuerier, name string) (SidecarState, error) {
	e, ok := Sidecars[name]
	if !ok {
		return SidecarState{}, fmt.Errorf("no sidecar named %s", name)
	}
	st := SidecarState{Name: name, Base: e.Base}
	ctx := context.Background()
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+name+"_docsize").Scan(&st.Indexed); err != nil {
		return st, fmt.Errorf("count the index of %s: %w", name, err)
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+e.Content).Scan(&st.Rows); err != nil {
		return st, fmt.Errorf("count the content of %s: %w", name, err)
	}
	return st, nil
}

func rebuildSidecar(q sidecarQuerier, name string) error {
	if _, ok := Sidecars[name]; !ok {
		return fmt.Errorf("no sidecar named %s", name)
	}
	if _, err := q.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO %s(%s) VALUES('rebuild')", name, name)); err != nil {
		return fmt.Errorf("rebuild %s: %w", name, err)
	}
	return nil
}

func checkSidecar(q sidecarQuerier, name string) error {
	if _, ok := Sidecars[name]; !ok {
		return fmt.Errorf("no sidecar named %s", name)
	}
	_, err := q.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO %s(%s, rank) VALUES('integrity-check', 1)", name, name))
	return err
}

func checkSidecarsOn(q sidecarQuerier) []string {
	var failed []string
	for _, name := range SidecarNames() {
		if err := checkSidecar(q, name); err != nil {
			failed = append(failed, name)
		}
	}
	return failed
}

func (s *Store) ensureSidecars(force ...string) []string {
	var q sidecarQuerier = s.w()
	if s.txh != nil {
		q = s.txh
		if s.schemaContext != nil {
			q = boundStatements{contextQueries: s.txh, ctx: s.schemaContext}
		}
	}
	var notes []string
	forced := map[string]bool{}
	for _, name := range force {
		forced[name] = true
	}
	for _, name := range SidecarNames() {
		st, err := sidecarState(q, name)
		if err != nil {
			notes = append(notes, fmt.Sprintf("MEMORY: sidecar %s could not be read — %v; its layer is unavailable", name, err))
			continue
		}
		if st.Ready() && !forced[name] {
			continue
		}
		if err := rebuildSidecar(q, name); err != nil {
			notes = append(notes, fmt.Sprintf("MEMORY: sidecar %s held %d of %d rows and could NOT be rebuilt — %v; its layer is unavailable", name, st.Indexed, st.Rows, err))
			continue
		}
		notes = append(notes, fmt.Sprintf("MEMORY: rebuilt sidecar %s (index held %d of %d rows)", name, st.Indexed, st.Rows))
	}
	return notes
}

func sidecarHousekeeping(q sidecarQuerier) string {
	failed := checkSidecarsOn(q)
	if len(failed) == 0 {
		return ""
	}
	var rebuilt, broken []string
	for _, name := range failed {
		if err := rebuildSidecar(q, name); err != nil {
			broken = append(broken, name+" ("+err.Error()+")")
			continue
		}
		rebuilt = append(rebuilt, name)
	}
	note := ""
	if len(rebuilt) > 0 {
		note += ", sidecars rebuilt after a failed integrity check: " + strings.Join(rebuilt, " ")
	}
	if len(broken) > 0 {
		note += ", sidecars that could NOT be rebuilt: " + strings.Join(broken, " ")
	}
	return note
}
