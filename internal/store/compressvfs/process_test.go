package compressvfs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const helperEnvironment = "AII_COMPRESSION_TEST_CHILD"

// .
// .
func TestVFSProcessHelper(t *testing.T) {
	role := os.Getenv(helperEnvironment)
	if role == "" {
		return
	}
	path := os.Getenv("AII_COMPRESSION_TEST_PATH")
	db := openTestDB(t, path, false)
	if role == "commit" {
		execTest(t, db, "INSERT INTO history VALUES('child committed')")
	} else if role == "crash" {
		execTest(t, db, "PRAGMA cache_size=2")
		execTest(t, db, "BEGIN IMMEDIATE")
		for i := 0; i < 100; i++ {
			execTest(t, db, "INSERT INTO history VALUES(zeroblob(8192))")
		}
	} else {
		t.Fatalf("unknown child role %q", role)
	}
	fmt.Println("ready")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func startChild(t *testing.T, path, role string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		t.Skip("OS-process test requires desktop process topology")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVFSProcessHelper$")
	cmd.Env = append(os.Environ(), helperEnvironment+"="+role, "AII_COMPRESSION_TEST_PATH="+path)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		in.Close()
		if cmd.ProcessState == nil {
			cancel()
			cmd.Wait()
		}
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("child did not reach its boundary: %q %v", line, err)
	}
	return cmd, in
}

func TestVFSCrossProcessWALSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cross-process.db")
	db := openTestDB(t, path, true)
	execTest(t, db, "PRAGMA journal_mode=WAL")
	execTest(t, db, "CREATE TABLE history(value)")
	execTest(t, db, "INSERT INTO history VALUES('parent committed')")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM history").Scan(&n); err != nil || n != 1 {
		t.Fatalf("pin: %d %v", n, err)
	}
	cmd, input := startChild(t, path, "commit")
	if err := tx.QueryRow("SELECT count(*) FROM history").Scan(&n); err != nil || n != 1 {
		t.Fatalf("pinned reader changed: %d %v", n, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM history").Scan(&n); err != nil || n != 2 {
		t.Fatalf("new reader missed commit: %d %v", n, err)
	}
	input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	execTest(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
}

func TestVFSCrashRollsBackUncommittedSpill(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.db")
			db := openTestDB(t, path, true)
			execTest(t, db, "PRAGMA journal_mode="+mode)
			execTest(t, db, "CREATE TABLE history(value)")
			execTest(t, db, "INSERT INTO history VALUES('committed history')")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			cmd, _ := startChild(t, path, "crash")
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("writer unexpectedly exited normally")
			}
			db = openTestDB(t, path, false)
			var n int
			if err := db.QueryRow("SELECT count(*) FROM history").Scan(&n); err != nil || n != 1 {
				t.Fatalf("recovery lost/added rows: %d %v", n, err)
			}
			var check string
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
				t.Fatalf("recovery integrity: %s %v", check, err)
			}
		})
	}
}
