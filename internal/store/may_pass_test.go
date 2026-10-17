package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// MayPass is what a start asks before it holds a page (internal/app,
// mayPassByItself): a database another connection held past the wait is a
// cause of the moment, and the start ends so that a service starts it again;
// a database that refused for any other reason is not. Asked here of the
// database's own answers, not of words.
func TestMayPassIsADatabaseHeldPastItsWaitAndNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.db")
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.Exec(`CREATE TABLE t (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	held, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := held.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer held.ExecContext(ctx, `ROLLBACK`)

	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetMaxOpenConns(1)
	if _, err := other.Exec(`PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	_, busy := other.Exec(`INSERT INTO t (id) VALUES ('a')`)
	if busy == nil {
		t.Fatal("rig: a write beside a held write transaction must be refused")
	}
	if !MayPass(fmt.Errorf("prepare schema and replay: %w", busy)) {
		t.Fatalf("A DATABASE HELD BY ANOTHER CONNECTION PAST ITS WAIT IS NOT OF THE MOMENT: %v", busy)
	}

	if _, err := held.ExecContext(ctx, `INSERT INTO t (id) VALUES ('b')`); err != nil {
		t.Fatal(err)
	}
	_, twice := held.ExecContext(ctx, `INSERT INTO t (id) VALUES ('b')`)
	if twice == nil {
		t.Fatal("rig: a second row under one key must be refused")
	}
	for _, err := range []error{twice, errors.New("database is locked"), nil} {
		if MayPass(err) {
			t.Errorf("%v is said to pass by itself", err)
		}
	}
}
