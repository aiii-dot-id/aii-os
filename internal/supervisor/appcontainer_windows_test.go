//go:build windows

package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
// .

func buildWallProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "wallprobe.exe")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/wallprobe").CombinedOutput()
	if err != nil {
		t.Fatalf("build wallprobe: %v\n%s", err, out)
	}
	return bin
}

func TestAppContainerWallRefusesWhatItMustAndAdmitsWhatItGrants(t *testing.T) {
	probe := buildWallProbe(t)
	granted := filepath.Dir(probe)
	grantedFile := filepath.Join(granted, "granted.txt")
	if err := os.WriteFile(grantedFile, []byte("readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	ungranted := t.TempDir()
	ungrantedFile := filepath.Join(ungranted, "secret.txt")
	if err := os.WriteFile(ungrantedFile, []byte("not for the container"), 0o600); err != nil {
		t.Fatal(err)
	}
	// .
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()

	control := func(want int32) {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatalf("host loopback positive control: %v", err)
		}
		c.Close()
		deadline := time.Now().Add(time.Second)
		for accepted.Load() < want && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if accepted.Load() != want {
			t.Fatalf("loopback accepted %d connections, expected only %d host controls", accepted.Load(), want)
		}
	}
	control(1)

	cmd := exec.Command(probe)
	cmd.Env = []string{
		"WALL_GRANTED_FILE=" + grantedFile,
		"WALL_UNGRANTED_FILE=" + ungrantedFile,
		"WALL_LOOPBACK=" + ln.Addr().String(),
	}
	ac := &AppContainer{Profile: "aiios.test.wall", GrantRead: []string{granted}}
	l, err := launchContained(cmd, ac, 0)
	if err != nil {
		t.Fatalf("launch under the wall: %v", err)
	}
	defer func() {
		if err := l.contained(); err != nil {
			t.Error(err)
		}
	}()
	l.stdin.Close()
	answers := map[string]string{}
	sawDone := false
	sc := bufio.NewScanner(l.stdout)
	for sc.Scan() {
		line := sc.Text()
		if line == "done" {
			sawDone = true
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			answers[k] = v
		}
	}
	errOut, _ := readAllString(l.stderr)
	state, err := cmd.Process.Wait()
	if err != nil || !state.Success() {
		t.Fatalf("the probe must exit cleanly: %v %v\nstderr: %s\nanswers: %v", err, state, errOut, answers)
	}
	if !sawDone {
		t.Fatalf("the probe did not finish: %v", answers)
	}
	want := map[string]string{
		"token":          "appcontainer",
		"read-granted":   "ok",
		"read-ungranted": "denied",
		"write-temp":     "ok",
		"write-granted":  "denied",
		"child-token":    "appcontainer",
	}
	for k, v := range want {
		got, present := answers[k]
		if !present || got != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got, v, answers)
		}
	}
	control(2)
	// .
	// .
	// .
	// .
	if answers["net-loopback"] != "denied:access" && answers["net-loopback"] != "timeout" {
		t.Errorf("loopback isolation: %q", answers["net-loopback"])
	}
	if answers["net-remote"] != "denied:access" {
		t.Errorf("remote permission denial: %q", answers["net-remote"])
	}
	// .
	var obs []string
	for _, k := range []string{"finalpath-dos", "finalpath-guid", "finalpath-nt", "finalpath-none", "self-job", "taskkill"} {
		obs = append(obs, k+"="+answers[k])
		t.Logf("observation under the wall: %s = %s", k, answers[k])
	}
	if dir := os.Getenv("AII_WALL_EVIDENCE"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "wall-observations.txt"), []byte(strings.Join(obs, "\n")+"\n"), 0o644)
	}
	if !strings.Contains(l.containment.Description, "AppContainer S-1-15-2-") || !strings.Contains(l.containment.Description, "before its first instruction") {
		t.Errorf("the containment line names the mechanism: %q", l.containment)
	}
}

func readAllString(r interface{ Read([]byte) (int, error) }) (string, error) {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String(), nil
		}
	}
}

// .
// .
// .
// .
func TestSupervisedChildRunsUnderTheWallWithStdioAndAudio(t *testing.T) {
	capture, lg := newCapture()
	granted := filepath.Dir(fakechildBin)
	s, err := Start(Spec{
		PluginID: "com.example.walled", Argv: []string{fakechildBin, "session-audio"},
		Env:         []string{"SEV_PLUGIN_SOCKET=stdio:"},
		SessionMode: true, AudioPair: true, ReadyMark: "child-ready", ReadyTimeout: 30 * time.Second,
		Backoff:      Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 1},
		AppContainer: &AppContainer{Profile: "aiios.test.wall", GrantRead: []string{granted}},
		Log:          lg,
	}, nil)
	if err != nil {
		t.Fatalf("start under the wall: %v\n%s", err, capture.String())
	}
	pid := s.Pid()
	if pid <= 0 {
		t.Fatal("no pid")
	}
	if !strings.Contains(capture.String(), "AppContainer S-1-15-2-") {
		t.Fatalf("the activation log names the wall: %s", capture.String())
	}
	// .
	// .
	in, out, ok := s.AudioPair()
	if !ok {
		t.Fatal("a running walled child hands out its audio pair")
	}
	pcm := bytes.Repeat([]byte{3, 4}, 320)
	want := audio.Frame{Kind: audio.KindPCM, Stream: 9, Seq: 1, Start: 100, PCM: pcm}
	if err := audio.WriteFrame(in, want); err != nil {
		t.Fatal(err)
	}
	got, err := audio.ReadFrame(out)
	if err != nil || got.Kind != want.Kind || got.Stream != want.Stream || got.Seq != want.Seq || !bytes.Equal(got.PCM, want.PCM) {
		t.Fatalf("audio echo under the wall: %v %+v", err, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.CloseContext(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	// .
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pid %d survived Close under the wall", pid)
}

func processAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), fmt.Sprintf(" %d ", pid))
}
