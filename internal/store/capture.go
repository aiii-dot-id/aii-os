package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
var ErrNotAtBoundary = errors.New("the projection mirror is not at the captured boundary")

// .
// .
// .
// .
const backupStepPages = 1024

// .
// .
// .
// .
// .
type Pin struct {
	conn *sql.Conn
	head MirrorHead
}

// .
// .
type MirrorHead struct {
	Seq  uint64
	Hash string
}

// .
// .
// .
// .
type RuntimeFacts struct {
	LifetimeTicks int64            `json:"lifetime_ticks"`
	LastTurnSeq   int64            `json:"last_turn_seq"`
	Ephemeral     map[string]int64 `json:"ephemeral_rows"`
}

// .
func (f RuntimeFacts) Differs(other RuntimeFacts) string {
	if f.LifetimeTicks != other.LifetimeTicks {
		return fmt.Sprintf("lifetime_ticks %d, want %d", other.LifetimeTicks, f.LifetimeTicks)
	}
	if f.LastTurnSeq != other.LastTurnSeq {
		return fmt.Sprintf("last_turn_seq %d, want %d", other.LastTurnSeq, f.LastTurnSeq)
	}
	for _, name := range EphemeralTables() {
		if f.Ephemeral[name] != other.Ephemeral[name] {
			return fmt.Sprintf("%s holds %d rows, want %d", name, other.Ephemeral[name], f.Ephemeral[name])
		}
	}
	return ""
}

// .
// .
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) PinAt(ctx context.Context, seq uint64, hash string) (*Pin, error) {
	if s.readOnly {
		return nil, fmt.Errorf("pin: a read-only mount is not a state to capture")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("pin: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("pin: begin: %w", err)
	}
	p := &Pin{conn: conn}
	head, err := readMirrorHead(ctx, conn)
	if err != nil {
		p.Release()
		return nil, fmt.Errorf("pin: %w", err)
	}
	// .
	// .
	if head.Hash != hash {
		p.Release()
		return nil, fmt.Errorf("%w: mirror at record %d (%s), record captured at %d (%s)",
			ErrNotAtBoundary, head.Seq, shortHash(head.Hash), seq, shortHash(hash))
	}
	p.head = head
	return p, nil
}

// .
func (p *Pin) Head() MirrorHead { return p.head }

// .
// .
func (p *Pin) Facts(ctx context.Context) (RuntimeFacts, error) {
	return readRuntimeFacts(ctx, p.conn)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (p *Pin) BackupTo(ctx context.Context, dst string) (retErr error) {
	if err := checkNoDatabaseSidecars(dst); err != nil {
		return err
	}
	// .
	// .
	reserved, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("backup destination: %w", err)
	}
	defer func() {
		if retErr != nil {
			removeDatabaseFiles(dst)
		}
	}()
	if err := reserved.Close(); err != nil {
		return err
	}
	return backupConnection(ctx, p.conn, databaseURI(dst))
}

func checkNoDatabaseSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("destination has an existing SQLite sidecar: %s", path+suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// .
// .
func backupConnection(ctx context.Context, conn *sql.Conn, destinationURI string) error {
	return conn.Raw(func(dc any) error {
		src, ok := dc.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("backup: the driver connection offers no online backup")
		}
		b, err := src.NewBackup(destinationURI)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		for {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("backup: %w", errors.Join(err, b.Finish()))
			}
			more, err := b.Step(backupStepPages)
			if err != nil {
				return fmt.Errorf("backup: %w", errors.Join(err, b.Finish()))
			}
			if !more {
				break
			}
		}
		if err := b.Finish(); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		return nil
	})
}

// .
// .
func (p *Pin) Release() {
	if p == nil || p.conn == nil {
		return
	}
	// .
	// .
	p.conn.ExecContext(context.Background(), "ROLLBACK")
	p.conn.Close()
	p.conn = nil
}

// .
// .
// .
// .
// .
func InspectCopy(ctx context.Context, path string) (MirrorHead, RuntimeFacts, error) {
	if err := compressvfs.Register(); err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("inspect: %w", err)
	}
	if _, err := os.Stat(path); err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("inspect: %w", err)
	}
	// .
	// .
	if err := checkNoDatabaseSidecars(path); err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("inspect requires a standalone image: %w", err)
	}
	db, err := sql.Open("sqlite", databaseURI(path)+"?vfs="+compressvfs.Name+"&mode=ro&immutable=1&_pragma=query_only(1)")
	if err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("inspect: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := pragmaClean(ctx, db, "integrity_check", "ok"); err != nil {
		return MirrorHead{}, RuntimeFacts{}, err
	}
	if err := pragmaClean(ctx, db, "foreign_key_check", ""); err != nil {
		return MirrorHead{}, RuntimeFacts{}, err
	}
	head, err := readMirrorHead(ctx, db)
	if err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("inspect: %w", err)
	}
	facts, err := readRuntimeFacts(ctx, db)
	if err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("inspect: %w", err)
	}
	return head, facts, nil
}

// .
// .
// .
func pragmaClean(ctx context.Context, db *sql.DB, pragma, clean string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA "+pragma)
	if err != nil {
		return fmt.Errorf("%s: %w", pragma, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("%s: %w", pragma, err)
	}
	var problems []string
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("%s scan: %w", pragma, err)
		}
		line := strings.TrimSpace(fmt.Sprint(cells...))
		if clean != "" && len(cells) == 1 && fmt.Sprint(cells[0]) == clean {
			continue
		}
		if len(problems) < 5 {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%s: %w", pragma, err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s reports damage in the copy: %s", pragma, strings.Join(problems, "; "))
	}
	return nil
}

func readMirrorHead(ctx context.Context, q queryer) (MirrorHead, error) {
	var (
		evt  ledger.Event
		typ  string
		ring sql.NullInt64
	)
	err := q.QueryRowContext(ctx,
		`SELECT seq, prev, ts, type, ring, content FROM ledger ORDER BY seq DESC LIMIT 1`,
	).Scan(&evt.Seq, &evt.Prev, &evt.Timestamp, &typ, &ring, &evt.Content)
	if err == sql.ErrNoRows {
		return MirrorHead{}, nil
	}
	if err != nil {
		return MirrorHead{}, fmt.Errorf("read the mirror head: %w", err)
	}
	if !ring.Valid {
		return MirrorHead{}, fmt.Errorf("mirror record %d carries no ring — its entry hash cannot be recomputed", evt.Seq)
	}
	evt.Type, evt.Ring = ledger.EventType(typ), int(ring.Int64)
	return MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}, nil
}

func readRuntimeFacts(ctx context.Context, q queryer) (RuntimeFacts, error) {
	f := RuntimeFacts{Ephemeral: map[string]int64{}}
	err := q.QueryRowContext(ctx,
		`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&f.LifetimeTicks)
	if err != nil && err != sql.ErrNoRows {
		return f, fmt.Errorf("read the lived clock: %w", err)
	}
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(turn_seq), 0) FROM conversations`).Scan(&f.LastTurnSeq); err != nil {
		return f, fmt.Errorf("read the last turn: %w", err)
	}
	for _, name := range EphemeralTables() {
		var n int64
		if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+name).Scan(&n); err != nil {
			return f, fmt.Errorf("count %s: %w", name, err)
		}
		f.Ephemeral[name] = n
	}
	return f, nil
}

// .
// .
func removeDatabaseFiles(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		os.Remove(path + suffix)
	}
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func ProveRestore(ctx context.Context, dbCopy, ledgerCopy, scratchDir string) (_ MirrorHead, _ RuntimeFacts, retErr error) {
	if err := os.MkdirAll(scratchDir, 0o700); err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(scratchDir); err != nil && retErr == nil {
			retErr = fmt.Errorf("restore proof: scratch not removed: %w", err)
		}
	}()
	scratch := filepath.Join(scratchDir, "aii.db")
	if err := copyFileSynced(dbCopy, scratch); err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: %w", err)
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if err := copyFileSynced(dbCopy+suffix, scratch+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: copy recovery file %s: %w", suffix, err)
		}
	}
	st, err := New(scratch)
	if err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: the database copy does not open as the runtime opens it: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("restore proof: close: %w", err)
		}
	}()
	if err := st.ReplayFromFile(ledgerCopy); err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: the record copy does not replay over the database copy: %w", err)
	}
	head, err := readMirrorHead(ctx, st.db)
	if err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: %w", err)
	}
	facts, err := readRuntimeFacts(ctx, st.db)
	if err != nil {
		return MirrorHead{}, RuntimeFacts{}, fmt.Errorf("restore proof: %w", err)
	}
	return head, facts, nil
}

func copyFileSynced(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
