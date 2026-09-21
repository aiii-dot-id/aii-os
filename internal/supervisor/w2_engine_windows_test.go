//go:build windows

package supervisor

import (
	"context"
	"encoding/json"
	"errors"
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
		if os.Getenv("AII_REQUIRED_WINDOWS_QUALIFICATION") == "1" {
			t.Fatal("required Windows qualification inputs missing")
		}
		t.Skip("set AII_WALL_ENGINE and AII_WALL_MODELS (prepared copies) to run this test")
	}
	root := filepath.Dir(carrier)
	readyTimeout := DefaultReadyTimeout
	if configured := os.Getenv("AII_WALL_READY_TIMEOUT"); configured != "" {
		var err error
		readyTimeout, err = time.ParseDuration(configured)
		if err != nil || readyTimeout <= 0 {
			t.Fatalf("invalid AII_WALL_READY_TIMEOUT %q: must be a positive duration", configured)
		}
	}
	rec := map[string]any{
		"host_commit_note": "the supervisor of this test source; see the bound source inventory",
		"carrier":          carrier, "runtime_root": root, "models": models,
		"started_at": time.Now().UTC().Format(time.RFC3339Nano), "ready_timeout_seconds": readyTimeout.Seconds(),
	}
	defer func() {
		if evidence == "" {
			var err error
			evidence, err = os.MkdirTemp("", "w2a-evidence-")
			if err != nil {
				t.Error(err)
				return
			}
		}
		if err := os.MkdirAll(evidence, 0o755); err != nil {
			t.Errorf("wall: evidence dir %s: %v", evidence, err)
		}
		rec["passed"] = !t.Failed()
		raw, _ := json.MarshalIndent(rec, "", "  ")
		path := filepath.Join(evidence, "w2a-launch.json")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = file.Write(raw)
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			t.Errorf("wall: evidence file %s: %v", path, err)
		} else {
			t.Logf("wall: evidence written to %s", path)
		}
	}()
	capture, lg := newCapture()
	const id = "id.aiii.voice.cp1"
	spec := Spec{
		PluginID: id, Argv: []string{carrier},
		Env:         []string{"SEV_PLUGIN_SOCKET=stdio:", "SEV_PLUGIN_ID=" + id, "AII_MODELS_DIR=" + models, "AII_RUNTIME_ROOT=" + root},
		SessionMode: true, AudioPair: true,
		ReadyMark: "event=ready", ReadyTimeout: readyTimeout,
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
		t.Fatalf("wall: the frozen carrier did not reach readiness under the wall: %v\n--- host log ---\n%s", err, capture.String())
	}
	readyIn := time.Since(spawnAt)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	pid := s.Pid()
	rec["containment"] = s.Containment()
	rec["ready_timeout_seconds"] = spec.readyTimeout().Seconds()
	rec["pid"] = pid
	rec["spawn_to_ready_seconds"] = readyIn.Seconds()
	rec["ready_line"] = s.ReadyLine()
	t.Logf("wall: ready after %s (pid %d); ready line %d bytes", readyIn, pid, len(s.ReadyLine()))
	if !strings.Contains(capture.String(), "AppContainer S-1-15-2-") {
		t.Errorf("the activation log must name the wall:\n%s", capture.String())
	}
	// .
	time.Sleep(3 * time.Second)
	tree, err := descendantsOf(pid)
	if err != nil {
		t.Fatal(err)
	}
	var procs []map[string]any
	handles := map[uint32]windows.Handle{}
	defer func() {
		for _, h := range handles {
			windows.CloseHandle(h)
		}
	}()
	for _, p := range append([]processRow{{PID: uint32(pid), Name: filepath.Base(carrier)}}, tree...) {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, p.PID)
		if err != nil {
			t.Fatalf("retain process handle %d: %v", p.PID, err)
		}
		handles[p.PID] = h
		sid, serr := processContainerSID(h)
		if serr != nil || sid != s.Containment().AppContainerSID {
			t.Fatalf("process %d token SID %q differs from launched container: %v", p.PID, sid, serr)
		}
		in, terr := processIsAppContainer(int(p.PID))
		row := map[string]any{"pid": p.PID, "parent": p.Parent, "name": p.Name, "appcontainer": in, "sid": sid}
		if terr != nil {
			row["token_error"] = terr.Error()
		}
		procs = append(procs, row)
		t.Logf("wall: process %d (%s, parent %d): appcontainer=%v err=%v", p.PID, p.Name, p.Parent, in, terr)
		if terr != nil || !in {
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
	// .
	// .
	for pid, h := range handles {
		state, err := windows.WaitForSingleObject(h, 0)
		if err != nil {
			t.Fatalf("retirement observation %d: %v", pid, err)
		}
		if state != windows.WAIT_OBJECT_0 {
			survivors = append(survivors, pid)
		}
	}
	rec["survivors_after_close"] = survivors
	rec["log"] = capture.String()
	if cerr != nil {
		t.Errorf("Close under the wall: %v", cerr)
	}
	if len(survivors) > 0 {
		t.Errorf("the out-of-band kill left processes alive: %v", survivors)
	}
	t.Logf("wall: close took %s; %d descendants; survivors %v", time.Since(closeAt), len(tree), survivors)
}

type processRow struct {
	PID, Parent uint32
	Name        string
}

// .
// .
func descendantsOf(pid int) ([]processRow, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var rows []processRow
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		rows = append(rows, processRow{PID: e.ProcessID, Parent: e.ParentProcessID, Name: windows.UTF16ToString(e.ExeFile[:])})
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
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
	return out, nil
}

func processExists(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code == 259, nil
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

// .
func processContainerSID(h windows.Handle) (string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	var size uint32
	err := windows.GetTokenInformation(token, 31, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", fmt.Errorf("container SID size query: %v", err)
	}
	if size < uint32(unsafe.Sizeof(uintptr(0))) {
		return "", fmt.Errorf("missing container SID")
	}
	buf := make([]byte, size)
	if err := windows.GetTokenInformation(token, 31, &buf[0], size, &size); err != nil {
		return "", err
	}
	sid := *(**windows.SID)(unsafe.Pointer(&buf[0]))
	if sid == nil {
		return "", fmt.Errorf("process has no container SID")
	}
	return sid.String(), nil
}
