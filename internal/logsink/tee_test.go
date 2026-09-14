package logsink

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
type deadWriter struct{}

func (deadWriter) Write([]byte) (int, error) {
	return 0, errors.New("write /dev/stderr: bad file descriptor")
}

func TestTeeKeepsTheFileWhenStderrIsDead(t *testing.T) {
	var file bytes.Buffer
	w := tee{file: &file, stderr: deadWriter{}}

	if _, err := w.Write([]byte("the operator must still get this\n")); err != nil {
		t.Fatalf("write reported an error from the ephemeral sink: %v", err)
	}
	if got := file.String(); !strings.Contains(got, "the operator must still get this") {
		t.Fatalf("a dead stderr suppressed the file sink; file has %q", got)
	}
}

// .
// .
func TestTeeReportsAFailingFile(t *testing.T) {
	w := tee{file: deadWriter{}, stderr: &bytes.Buffer{}}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("a failing file sink reported success")
	}
}

// .
// .
// .
// .
// .
func TestInstalledSinkSurvivesADeadStderr(t *testing.T) {
	dir := t.TempDir()

	// .
	// .
	dead, err := os.CreateTemp(t.TempDir(), "deadstderr")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	dead.Close()

	restoreLog, restoreErr := log.Writer(), os.Stderr
	os.Stderr = dead
	t.Cleanup(func() { os.Stderr = restoreErr; log.SetOutput(restoreLog) })

	sink, err := Install(Config{Dir: filepath.Join(dir, "log")})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	t.Cleanup(sink.Close)

	log.Printf("POSTBIRTH: this line is the whole point")

	got, err := os.ReadFile(filepath.Join(sink.Dir(), LiveName))
	if err != nil {
		t.Fatalf("read live log: %v", err)
	}
	if !strings.Contains(string(got), "POSTBIRTH: this line is the whole point") {
		t.Fatalf("live log lost the line to a dead stderr; log holds:\n%s", got)
	}
}
