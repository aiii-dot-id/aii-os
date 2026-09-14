//go:build windows

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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
func TestW2FrozenEngineStartsUnderTheWall(t *testing.T) {
	carrier := filepath.Clean(os.Getenv("AII_WALL_ENGINE"))
	models := filepath.Clean(os.Getenv("AII_WALL_MODELS"))
	evidence := os.Getenv("AII_WALL_EVIDENCE")
	if os.Getenv("AII_WALL_ENGINE") == "" || os.Getenv("AII_WALL_MODELS") == "" {
		t.Skip("set AII_WALL_ENGINE and AII_WALL_MODELS (the voice side's prepared copies) to run W2a")
	}
	root := filepath.Dir(carrier)
	rec := map[string]any{
		"host_commit_note": "the supervisor of this tree; see the landing record",
		"carrier":          carrier, "runtime_root": root, "models": models,
		"started_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	defer func() {
		if evidence == "" {
			evidence = filepath.Join(os.TempDir(), "w2a-evidence")
		}
		if err := os.MkdirAll(evidence, 0o755); err != nil {
			t.Logf("W2a: evidence dir %s: %v", evidence, err)
		}
		raw, _ := json.MarshalIndent(rec, "", "  ")
		path := filepath.Join(evidence, "w2a-launch.json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Logf("W2a: evidence file %s: %v", path, err)
		} else {
			t.Logf("W2a: evidence written to %s", path)
		}
	}()
	capture, lg := newCapture()
	const id = "id.aiii.voice.cp1"
	spec := Spec{
		PluginID: id, Argv: []string{carrier},
		Env:         []string{"SEV_PLUGIN_SOCKET=stdio:", "SEV_PLUGIN_ID=" + id, "AII_MODELS_DIR=" + models},
		SessionMode: true, AudioPair: true,
		ReadyMark: "AII_VOICE_READY", ReadyTimeout: 180 * time.Second,
		Backoff:      Backoff{Initial: time.Second, Max: time.Second, MaxRestarts: 0},
		AppContainer: &AppContainer{Profile: "aiios." + id, GrantRead: []string{root, models}},
		Log:          lg,
	}
	spawnAt := time.Now()
	s, err := Start(spec, nil)
	rec["log"] = capture.String()
	if err != nil {
		rec["start_error"] = err.Error()
		rec["spawn_to_failure_seconds"] = time.Since(spawnAt).Seconds()
		t.Fatalf("W2a: the frozen carrier did not reach readiness under the wall: %v\n--- host log ---\n%s", err, capture.String())
	}
	readyIn := time.Since(spawnAt)
	pid := s.Pid()
	rec["pid"] = pid
	rec["spawn_to_ready_seconds"] = readyIn.Seconds()
	rec["ready_line"] = s.ReadyLine()
	t.Logf("W2a: ready after %s (pid %d); ready line %d bytes", readyIn, pid, len(s.ReadyLine()))
	if !strings.Contains(capture.String(), "AppContainer S-1-15-2-") {
		t.Errorf("the activation log must name the wall:\n%s", capture.String())
	}
	// .
	time.Sleep(3 * time.Second)
	tree := descendantsOf(pid)
	var procs []map[string]any
	for _, p := range append([]processRow{{PID: uint32(pid), Name: filepath.Base(carrier)}}, tree...) {
		in, terr := processIsAppContainer(int(p.PID))
		row := map[string]any{"pid": p.PID, "parent": p.Parent, "name": p.Name, "appcontainer": in}
		if terr != nil {
			row["token_error"] = terr.Error()
		}
		procs = append(procs, row)
		t.Logf("W2a: process %d (%s, parent %d): appcontainer=%v err=%v", p.PID, p.Name, p.Parent, in, terr)
		if terr == nil && !in {
			t.Errorf("descendant %d (%s) runs outside the container", p.PID, p.Name)
		}
	}
	rec["processes"] = procs
	rec["descendants"] = len(tree)
	// .
	closeAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cerr := s.CloseContext(ctx)
	rec["close_error"] = fmt.Sprint(cerr)
	rec["close_seconds"] = time.Since(closeAt).Seconds()
	var survivors []uint32
	deadline := time.Now().Add(10 * time.Second)
	for {
		survivors = survivors[:0]
		for _, p := range append([]processRow{{PID: uint32(pid)}}, tree...) {
			if processExists(int(p.PID)) {
				survivors = append(survivors, p.PID)
			}
		}
		if len(survivors) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	rec["survivors_after_close"] = survivors
	rec["log"] = capture.String()
	if cerr != nil {
		t.Errorf("Close under the wall: %v", cerr)
	}
	if len(survivors) > 0 {
		t.Errorf("the out-of-band kill left processes alive: %v", survivors)
	}
	t.Logf("W2a: close took %s; %d descendants; survivors %v", time.Since(closeAt), len(tree), survivors)
}

type processRow struct {
	PID, Parent uint32
	Name        string
}

// .
// .
func descendantsOf(pid int) []processRow {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var rows []processRow
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		rows = append(rows, processRow{PID: e.ProcessID, Parent: e.ParentProcessID, Name: windows.UTF16ToString(e.ExeFile[:])})
	}
	var out []processRow
	frontier := []uint32{uint32(pid)}
	seen := map[uint32]bool{uint32(pid): true}
	for len(frontier) > 0 {
		next := frontier[:0:0]
		for _, r := range rows {
			for _, f := range frontier {
				if r.Parent == f && !seen[r.PID] {
					seen[r.PID] = true
					out = append(out, r)
					next = append(next, r.PID)
				}
			}
		}
		frontier = next
	}
	return out
}

func processExists(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259
}

// .
// .
func processIsAppContainer(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()
	var flag, n uint32
	if err := windows.GetTokenInformation(token, tokenIsAppContainer, (*byte)(unsafe.Pointer(&flag)), uint32(unsafe.Sizeof(flag)), &n); err != nil {
		return false, err
	}
	return flag != 0, nil
}
