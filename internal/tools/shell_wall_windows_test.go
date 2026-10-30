//go:build windows

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// winWall is one identity's home with a kept directory in it (its data
// directory, a secret inside) and a folder outside the home the identity
// was never granted, run through the shell tool with the wall the registry
// would hand it. These tests need an interactive session (the scheduled
// task with an Interactive logon) where programs started at Low integrity
// initialise their libraries, as the installed host runs.
type winWall struct {
	home, data, secret, outside string
	tool                        *ShellTool
}

func newWinWall(t *testing.T) winWall {
	t.Helper()
	if inServiceSession() {
		t.Skip("the shell's wall runs only in an interactive session: run these through the scheduled task with an Interactive logon")
	}
	root := t.TempDir()
	home := filepath.Join(root, "identity-0")
	data := filepath.Join(home, "data")
	outside := filepath.Join(root, "elsewhere")
	for _, d := range []string{data, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(data, "secret.txt")
	if err := os.WriteFile(secret, []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := winWall{home: home, data: data, secret: secret, outside: outside}
	f.tool = &ShellTool{timeout: 60 * time.Second, sandbox: home}
	f.tool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{{ID: "data", Kind: firewall.KindSubstrate, Path: data, Enforced: true}}, boundary: true}, nil
	}
	t.Cleanup(f.tool.endWall)
	return f
}

func (f winWall) run(t *testing.T, cmd string, walled bool) Result {
	t.Helper()
	if !walled {
		prev := f.tool.wall
		f.tool.wall = func() (shellWall, error) { w, _ := prev(); w.boundary = false; return w, nil }
		defer func() { f.tool.wall = prev }()
	}
	res, err := f.tool.Execute(context.Background(), map[string]any{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func ok(res Result, want string) bool {
	return !res.Refused && res.ExitStatus != nil && *res.ExitStatus == 0 && strings.Contains(res.Output, want)
}

// A KEPT FILE IS NOT READ BEHIND THE WALL; the same command reads it with
// the sandbox off (the control half).
func TestOnWindowsTheWallRefusesAKeptFile(t *testing.T) {
	f := newWinWall(t)
	cmd := "Get-Content -LiteralPath '" + f.secret + "'"
	if res := f.run(t, cmd, false); !ok(res, "KEPT SECRET") {
		t.Fatalf("control: unwalled the secret must be read, or its refusal proves nothing: %+v", res)
	}
	res := f.run(t, cmd, true)
	if strings.Contains(res.Output, "KEPT SECRET") || (res.ExitStatus != nil && *res.ExitStatus == 0) {
		t.Fatalf("a kept file was read behind the wall: %+v", res)
	}
	if okLabel, err := fileperm.HasLabel(f.data, fileperm.LabelKept); err != nil || !okLabel {
		lbl, _ := fileperm.LabelOf(f.data)
		t.Fatalf("the kept directory does not carry the kept label as written: %q (%v)", lbl, err)
	}
	if okLabel, err := fileperm.HasLabel(f.home, fileperm.LabelIdentity); err != nil || !okLabel {
		lbl, _ := fileperm.LabelOf(f.home)
		t.Fatalf("the home does not carry the identity's label as written: %q (%v)", lbl, err)
	}
}

// THE HOME IS THE IDENTITY'S TO WRITE behind the wall.
func TestOnWindowsTheWallLetsTheHomeBeWritten(t *testing.T) {
	f := newWinWall(t)
	p := filepath.Join(f.home, "note.txt")
	res := f.run(t, "Set-Content -LiteralPath '"+p+"' -Value written; Get-Content -LiteralPath '"+p+"'", true)
	if !ok(res, "written") {
		t.Fatalf("the home must be writable behind the wall: %+v", res)
	}
}

// A FOLDER OUTSIDE THE HOME AND ITS GRANTS IS NOT WRITTEN behind the wall;
// unwalled, the same write lands (the control half).
func TestOnWindowsTheWallRefusesAWriteOutsideTheHome(t *testing.T) {
	f := newWinWall(t)
	control := filepath.Join(f.outside, "control.txt")
	if res := f.run(t, "Set-Content -LiteralPath '"+control+"' -Value x", false); !ok(res, "") {
		t.Fatalf("control: unwalled the write must land: %+v", res)
	}
	p := filepath.Join(f.outside, "walled.txt")
	f.run(t, "Set-Content -LiteralPath '"+p+"' -Value x", true)
	if _, err := os.Stat(p); err == nil {
		t.Fatalf("a write outside the home landed behind the wall: %s", p)
	}
}

// A KEPT DIRECTORY IN THE HOME IS HELD: behind the wall the shell cannot
// rename it away and put another at its name.
func TestOnWindowsTheWallHoldsAKeptDirectoryInTheHome(t *testing.T) {
	f := newWinWall(t)
	f.run(t, "Rename-Item -LiteralPath '"+f.data+"' -NewName moved", true)
	if _, err := os.Stat(f.data); err != nil {
		t.Fatalf("the kept directory was renamed away behind the wall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "moved")); err == nil {
		t.Fatal("the kept directory was renamed away behind the wall")
	}
}

func TestOnWindowsANestedKeptFileNeedsAProtectedParent(t *testing.T) {
	f := newWinWall(t)
	config := filepath.Join(f.home, "unprotected", "nested", "config.json")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("CONFIG SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{{Path: config, Kind: firewall.KindSubstrate, Enforced: true}}, boundary: true}, nil
	}
	if res := f.run(t, "Get-Content -LiteralPath '"+config+"'", false); !ok(res, "CONFIG SECRET") {
		t.Fatalf("uncontained control could not read config: %+v", res)
	}
	res := f.run(t, "Write-Output ran", true)
	if !res.Refused || !strings.Contains(res.Error, wallKeptInLow) || strings.Contains(res.Output, "ran") {
		t.Fatalf("a nested kept file with a Low-writable parent admitted a shell: %+v", res)
	}
}

func TestOnWindowsReadableParentsDoNotPreserveSecretReplacements(t *testing.T) {
	for _, secret := range []bool{true, false} {
		t.Run(map[bool]string{true: "secret", false: "readable"}[secret], func(t *testing.T) {
			f := newWinWall(t)
			dir := filepath.Join(f.home, "readable")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, "document")
			if err := os.WriteFile(p, []byte("CONTENT"), 0o600); err != nil {
				t.Fatal(err)
			}
			f.tool.wall = func() (shellWall, error) {
				return shellWall{kept: []*firewall.Rule{
					{Path: dir, Kind: firewall.KindSubstrate, ReadOpen: true, Enforced: true},
					{Path: p, Kind: firewall.KindSubstrate, ReadOpen: !secret, Enforced: true},
				}, boundary: true}, nil
			}
			cmd := "Get-Content -LiteralPath '" + p + "'"
			if res := f.run(t, cmd, false); !ok(res, "CONTENT") {
				t.Fatalf("uncontained read failed: %+v", res)
			}
			res := f.run(t, cmd, true)
			if secret {
				if !res.Refused || !strings.Contains(res.Error, wallKeptReadableParent) {
					t.Fatalf("secret replacement could inherit a readable label: %+v", res)
				}
			} else if !ok(res, "CONTENT") {
				t.Fatalf("readable file was refused under its readable parent: %+v", res)
			}
		})
	}
}

func TestOnWindowsNativeParentsPrecedeChildrenWithoutChangingPrecedence(t *testing.T) {
	home := t.TempDir()
	leaf := filepath.Join(home, "config", "nested", "config.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "unrelated-first", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "one"), Enforced: true},
		{ID: "file", Kind: firewall.KindSubstrate, Path: leaf, Enforced: true},
		{ID: "unrelated-last", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "two"), Enforced: true},
	})
	reg := NewRegistry(home, policy, Timeouts{})
	reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{
			{ID: "ignored-duplicate", Kind: firewall.KindSubstrate, Path: leaf, ReadOpen: true, Enforced: true},
			{ID: "parent", Kind: firewall.KindSubstrate, Path: filepath.Dir(leaf), Enforced: true},
			{ID: "grandparent", Kind: firewall.KindSubstrate, Path: strings.ToUpper(filepath.Dir(filepath.Dir(leaf))), Enforced: true},
		}, nil
	})
	w, err := reg.shellWallData()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range keptOnWindows(w.kept) {
		got = append(got, r.ID)
		if r.ReadOpen {
			t.Fatal("an extra duplicate changed the policy's secret into a readable file")
		}
	}
	want := []string{"unrelated-first", "grandparent", "parent", "file", "unrelated-last"}
	if !slices.Equal(got, want) {
		t.Fatalf("native label order %v, want %v", got, want)
	}
	unavailable := errors.New("native places unavailable")
	reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) { return nil, unavailable })
	if _, err := reg.shellWallData(); !errors.Is(err, unavailable) {
		t.Fatalf("a callback failure did not refuse the wall: %v", err)
	}
}

// A running Low child must not gain access when the host replaces config
// files. Checking only after the next shell admission would miss the gap:
// that admission can relabel the leaked replacements before the test reads.
func TestOnWindowsRunningShellCannotReadOrWriteReplacedConfiguration(t *testing.T) {
	f := newWinWall(t)
	dir := filepath.Join(f.home, "config")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{"config.json", "providers.json"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("OLD SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	// A configured secret can also live outside the shell home. Its
	// parent is not Low-writable, but a replacement must preserve the
	// target's no-read-up label at creation rather than inherit readability.
	external := filepath.Join(f.outside, "config.json")
	if err := os.WriteFile(external, []byte("OLD SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, external)
	policy := firewall.DefaultPolicy()
	var files []*firewall.Rule
	for _, p := range paths {
		files = append(files, &firewall.Rule{Path: p, Kind: firewall.KindSubstrate, Enforced: true})
	}
	policy.SetPlaces(files)
	reg := NewRegistry(f.home, policy, Timeouts{})
	// As in the app: the parent is native label machinery, not logical
	// ownership of the docs beside the credentials. The registry appends
	// it after the files; the Windows wall must establish it before them.
	reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{{Path: dir, Kind: firewall.KindSubstrate, Enforced: true}}, nil
	})
	f.tool.wall = reg.shellWallData
	for _, p := range paths {
		if res := f.run(t, "Get-Content -LiteralPath '"+p+"'", false); !ok(res, "OLD SECRET") {
			t.Fatalf("uncontained control could not read config: %+v", res)
		}
	}
	ready, released := filepath.Join(f.home, "ready"), filepath.Join(f.home, "released")
	cmd := "$ErrorActionPreference='Stop'; Set-Content -LiteralPath '" + ready + "' -Value ready; " +
		"while (!(Test-Path -LiteralPath '" + released + "')) { Start-Sleep -Milliseconds 20 }; "
	for _, p := range paths {
		cmd += "try { Get-Content -LiteralPath '" + p + "'; Write-Output 'LEAK' } catch { Write-Output 'READ_DENIED' }; "
		cmd += "try { Set-Content -LiteralPath '" + p + "' -Value changed; Write-Output 'WRITE_ALLOWED' } catch { Write-Output 'WRITE_DENIED' }; "
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	type answer struct {
		result Result
		err    error
	}
	var got answer
	done := make(chan struct{})
	go func() {
		r, err := f.tool.Execute(ctx, map[string]any{"command": cmd})
		got = answer{r, err}
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("contained child ended before the replacement: %+v (%v)", got.result, got.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("contained child never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, p := range paths {
		if published, err := atomicfile.WriteReplace(p, []byte("NEW SECRET"), 0o600); err != nil || !published {
			t.Fatalf("host replacement failed: %v (published %v)", err, published)
		}
	}
	if err := os.WriteFile(released, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	<-done
	if got.err != nil || !ok(got.result, "READ_DENIED") || strings.Count(got.result.Output, "READ_DENIED") != len(paths) ||
		strings.Count(got.result.Output, "WRITE_DENIED") != len(paths) || strings.Contains(got.result.Output, "SECRET") ||
		strings.Contains(got.result.Output, "LEAK") || strings.Contains(got.result.Output, "WRITE_ALLOWED") {
		t.Fatalf("a running Low child reached replacement configuration: %+v (%v)", got.result, got.err)
	}
	for _, p := range paths {
		if raw, err := os.ReadFile(p); err != nil || string(raw) != "NEW SECRET" {
			t.Fatalf("configuration replacement was changed: %q (%v)", raw, err)
		}
	}
}

// A KEPT FILE LYING DIRECTLY IN THE HOME is a layout the wall cannot hold:
// the call is refused, and nothing runs.
func TestOnWindowsAKeptFileDirectlyInTheHomeRefusesTheShell(t *testing.T) {
	f := newWinWall(t)
	kept := filepath.Join(f.home, "config.json")
	if err := os.WriteFile(kept, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{{ID: "config", Kind: firewall.KindSubstrate, Path: kept, Enforced: true}}, boundary: true}, nil
	}
	res := f.run(t, "Write-Output ran", true)
	if !res.Refused || !strings.Contains(res.Error, wallKeptInLow) || strings.Contains(res.Output, "ran") {
		t.Fatalf("a kept file directly in the home must refuse the shell before it runs: %+v", res)
	}
}

// THE TOKEN IS LOW, Administrators deny-only, and starts a process.
func TestTheWalledTokenIsLow(t *testing.T) {
	tok, err := lowToken()
	if err != nil {
		t.Fatal(err)
	}
	defer tok.Close()
	if !isLow(tok) {
		t.Fatal("the walled token is not Low")
	}
}

// IN A SERVICE SESSION THE WALLED SHELL IS REFUSED WITH ITS CAUSE, never run
// unwalled. Only meaningful where this test runs in session 0 (over ssh);
// in an interactive session it is skipped.
func TestInAServiceSessionTheWalledShellIsRefusedWithItsCause(t *testing.T) {
	if !inServiceSession() {
		t.Skip("this proves the session-0 refusal; it runs over ssh, not in a desktop session")
	}
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	res, err := st.Execute(context.Background(), map[string]any{"command": "Write-Output ran"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused || !strings.Contains(res.Error, wallNoSession) || strings.Contains(res.Output, "ran") {
		t.Fatalf("a walled shell in a service session must be refused with its cause: %+v", res)
	}
}

// A CALL IN A FOLDER ON A SHARE IS REFUSED BEHIND THE WALL, with its cause
// and before anything runs: a program at Low integrity reaches no share.
// The sandbox off, the same call runs there (the control half).
func TestOnWindowsTheWallRefusesACallOnAShare(t *testing.T) {
	share := shareOf(t, t.TempDir())
	st := &ShellTool{timeout: 60 * time.Second, sandbox: share}
	st.wall = func() (shellWall, error) { return shellWall{}, nil }
	if res, err := st.Execute(context.Background(), map[string]any{"command": "Write-Output ran"}); err != nil || !ok(res, "ran") {
		t.Fatalf("control: with the sandbox off the call runs on the share: %+v (%v)", res, err)
	}
	if inServiceSession() {
		t.Skip("the walled half runs only in an interactive session")
	}
	st.wall = func() (shellWall, error) { return shellWall{boundary: true}, nil }
	res, err := st.Execute(context.Background(), map[string]any{"command": "Write-Output ran"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused || !strings.Contains(res.Error, wallOnShare) || strings.Contains(res.Output, "ran") {
		t.Fatalf("a walled call on a share must be refused with its cause before it runs: %+v", res)
	}
}

func TestOnShareTellsAShareFromALocalFolder(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Users\x`:           false,
		`\\?\C:\Users\x`:       false,
		`\\.\C:\x`:             false,
		`\\host\share\x`:       true,
		`\\localhost\c$\Users`: true,
		`\\?\UNC\host\share\x`: true,
		`\\wsl$\Ubuntu\home`:   true,
		`//host/share/x`:       true,
	} {
		if got := onShare(path); got != want {
			t.Errorf("onShare(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestOnWindowsWhatTheHostWritesIntoAKeptPlaceIsKept(t *testing.T) {
	f := newWinWall(t)
	f.run(t, "Write-Output labelled", true)
	written := map[string]string{}
	p := filepath.Join(f.data, "written.json")
	if published, err := atomicfile.WriteReplace(p, []byte("ATOMIC SECRET"), 0o600); err != nil || !published {
		t.Fatalf("the atomic write was not published: %v (published %v)", err, published)
	}
	written[p] = "ATOMIC SECRET"
	staging, err := os.MkdirTemp(f.data, ".restore-staging-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "set"), 0o700); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, "set", "restored.json")
	if err := os.WriteFile(staged, []byte("STAGED SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	p = filepath.Join(f.data, "restored.json")
	if err := os.Rename(staged, p); err != nil {
		t.Fatal(err)
	}
	written[p] = "STAGED SECRET"
	aside := filepath.Join(f.data, "set-aside", "before")
	if err := os.MkdirAll(filepath.Dir(aside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, aside); err != nil {
		t.Fatal(err)
	}
	if stray, err := fileperm.Census(f.data, fileperm.LabelKept); err != nil || len(stray) != 0 {
		var labels []string
		for _, p := range stray {
			lbl, _ := fileperm.LabelOf(p)
			labels = append(labels, p+" "+lbl)
		}
		t.Fatalf("the host's writes into the kept place left objects without its label: %q (%v)", labels, err)
	}
	for path, secret := range written {
		res := f.run(t, "Get-Content -LiteralPath '"+path+"'", true)
		if strings.Contains(res.Output, secret) {
			t.Fatalf("the shell read %s, written by the host into a kept place: %+v", path, res)
		}
	}

	outside := filepath.Join(f.home, "made-outside.json")
	if err := os.WriteFile(outside, []byte("MOVED IN"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(f.data, "made-outside.json")
	if err := os.Rename(outside, moved); err != nil {
		t.Fatal(err)
	}
	stray, err := fileperm.Census(f.data, fileperm.LabelKept)
	if err != nil || len(stray) != 1 || !strings.EqualFold(stray[0], moved) {
		t.Fatalf("control: a file made in the home and renamed in must be the census's one find: %v (%v)", stray, err)
	}
	if res := f.run(t, "Get-Content -LiteralPath '"+moved+"'", true); !strings.Contains(res.Output, "MOVED IN") {
		t.Fatalf("control: the shell must read a file that kept the home's label, or the probes above prove nothing: %+v", res)
	}
}

func TestTheWallLabelsNoHomeThatIsNotAFullPath(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	var h shellHolder
	defer h.end()
	for _, home := range []string{"", ".", "identity-0"} {
		if err := h.ensure(home, shellWall{boundary: true}); err == nil || !strings.Contains(err.Error(), wallNoLabel) {
			t.Errorf("home %q: the wall must refuse it, not label it: %v", home, err)
		}
	}
	if err := h.ensure(filepath.Join(cwd, "home"), shellWall{boundary: true, roots: []string{"granted"}}); err == nil {
		t.Error("a granted root that is not a full path must be refused")
	}
	if lbl, err := fileperm.LabelOf(cwd); err != nil || lbl != "" {
		t.Errorf("the working folder was labelled: %q (%v)", lbl, err)
	}
}

// ANOTHER IDENTITY'S KEPT PLACES ARE KEPT, AND ITS HOME STAYS ITS OWN. The
// boundary names a sibling's slot whole; on Windows the wall labels its
// data and config folders kept and holds them, and leaves the slot to the
// sibling's own wall, which labels it Low (its home). Each identity's next
// call relabels nothing of the other's: the sibling still writes its home.
// The control half: unwalled, this identity reads the sibling's secret.
func TestOnWindowsTheWallKeepsAnotherIdentitysKeptPlaces(t *testing.T) {
	f := newWinWall(t)
	sib := filepath.Join(filepath.Dir(f.home), "identity-1")
	sibData, sibConfig := filepath.Join(sib, "data"), filepath.Join(sib, "config")
	for _, d := range []string{sibData, sibConfig} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sibSecret := filepath.Join(sibData, "secret.txt")
	if err := os.WriteFile(sibSecret, []byte("SIBLING SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sibConfigFile := filepath.Join(sibConfig, "config.json")
	if err := os.WriteFile(sibConfigFile, []byte("SIBLING CONFIG\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{
			{ID: "data", Kind: firewall.KindSubstrate, Path: f.data, Enforced: true},
			{ID: firewall.RuleOtherIdentity, Kind: firewall.KindSubstrate, Path: sib, Enforced: true},
		}, boundary: true}, nil
	}
	sibTool := &ShellTool{timeout: 60 * time.Second, sandbox: sib}
	sibTool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{
			{ID: "data", Kind: firewall.KindSubstrate, Path: sibData, Enforced: true},
			{ID: "config", Kind: firewall.KindSubstrate, Path: sibConfig, Enforced: true},
		}, boundary: true}, nil
	}
	t.Cleanup(sibTool.endWall)
	sibRun := func(cmd string) Result {
		res, err := sibTool.Execute(context.Background(), map[string]any{"command": cmd})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	read := "Get-Content -LiteralPath '" + sibSecret + "'"
	if res := f.run(t, read, false); !ok(res, "SIBLING SECRET") {
		t.Fatalf("control: unwalled the sibling's secret must be read, or its refusal proves nothing: %+v", res)
	}
	if res := sibRun("Write-Output started"); !ok(res, "started") {
		t.Fatalf("the sibling's walled shell must run: %+v", res)
	}
	for _, cmd := range []string{read, "Get-Content -LiteralPath '" + sibConfigFile + "'"} {
		if res := f.run(t, cmd, true); strings.Contains(res.Output, "SIBLING") {
			t.Fatalf("this identity read a sibling's kept place behind the wall: %+v", res)
		}
	}
	// The sibling stopped: its own hold is gone, and this identity's holds.
	sibTool.endWall()
	f.run(t, "Rename-Item -LiteralPath '"+sibData+"' -NewName moved", true)
	if _, err := os.Stat(sibData); err != nil {
		t.Fatalf("the sibling's data folder was renamed away behind the wall: %v", err)
	}
	if okLabel, err := fileperm.HasLabel(sib, fileperm.LabelIdentity); err != nil || !okLabel {
		lbl, _ := fileperm.LabelOf(sib)
		t.Fatalf("this identity's wall relabelled the sibling's home: %q (%v)", lbl, err)
	}
	note := filepath.Join(sib, "note.txt")
	if res := sibRun("Set-Content -LiteralPath '" + note + "' -Value mine; Get-Content -LiteralPath '" + note + "'"); !ok(res, "mine") {
		t.Fatalf("the sibling can no longer write its own home after this identity's call: %+v", res)
	}
}

func TestOnWindowsARemovedGrantLosesItsLowLabel(t *testing.T) {
	r, home, outside := writeHome(t, nil)
	granted := filepath.Join(outside, "proj")
	inHome := filepath.Join(home, "inside")
	for _, d := range []string{granted, inHome} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{granted, inHome, home} {
		if err := fileperm.LabelTree(d, fileperm.LabelIdentity, nil); err != nil {
			t.Fatal(err)
		}
	}
	r.SetExtraRoots([]string{granted, inHome})
	r.SetExtraRoots(nil)
	if lbl, err := fileperm.LabelOf(granted); err != nil || lbl != "" {
		t.Errorf("the removed grant still carries %q (%v)", lbl, err)
	}
	if ok, err := fileperm.HasLabel(inHome, fileperm.LabelIdentity); err != nil || !ok {
		t.Errorf("a removed root inside the home lost the Low label it shares with the home (%v)", err)
	}
}

func TestOnWindowsANativeChildStillReadsItsMaterial(t *testing.T) {
	f := newWinWall(t)
	runtime := filepath.Join(f.data, "plugins-runtime")
	if err := os.MkdirAll(runtime, 0o700); err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(runtime, "engine.txt")
	if err := os.WriteFile(engine, []byte("RUNTIME BYTES\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{
			{ID: "data", Kind: firewall.KindSubstrate, Path: f.data, Enforced: true},
			{ID: "runtime", Kind: firewall.KindSubstrate, Path: runtime, Enforced: true, NativeReads: true},
		}, boundary: true}, nil
	}
	if res := f.run(t, "Get-Content -LiteralPath '"+engine+"'", true); !ok(res, "RUNTIME BYTES") {
		t.Fatalf("a native child's material must stay readable at Low: %+v", res)
	}
	f.run(t, "Set-Content -LiteralPath '"+engine+"' -Value changed", true)
	if b, _ := os.ReadFile(engine); string(b) != "RUNTIME BYTES\n" {
		t.Fatalf("a native child's material was changed behind the wall: %q", b)
	}
	if res := f.run(t, "Get-Content -LiteralPath '"+f.secret+"'", true); strings.Contains(res.Output, "KEPT SECRET") {
		t.Fatalf("the data directory around the material was read behind the wall: %+v", res)
	}
	if okLabel, err := fileperm.HasLabel(runtime, fileperm.LabelReadable); err != nil || !okLabel {
		lbl, _ := fileperm.LabelOf(runtime)
		t.Fatalf("the material does not carry the readable label: %q (%v)", lbl, err)
	}
}

func TestOnWindowsAKeptFolderNotMadeYetIsTheHostsToMake(t *testing.T) {
	f := newWinWall(t)
	plugins := filepath.Join(f.home, "plugins")
	f.tool.wall = func() (shellWall, error) {
		return shellWall{kept: []*firewall.Rule{
			{ID: "data", Kind: firewall.KindSubstrate, Path: f.data, Enforced: true},
			{ID: "plugins", Kind: firewall.KindSubstrate, Path: plugins, Enforced: true, NativeReads: true},
		}, boundary: true}, nil
	}
	planted := filepath.Join(plugins, "planted.txt")
	f.run(t, "New-Item -ItemType Directory -Force -Path '"+plugins+"' | Out-Null; Set-Content -LiteralPath '"+planted+"' -Value x", true)
	if _, err := os.Stat(planted); err == nil {
		t.Fatal("the shell planted a file in the plugins' folder")
	}
	if okLabel, err := fileperm.HasLabel(plugins, fileperm.LabelReadable); err != nil || !okLabel {
		lbl, _ := fileperm.LabelOf(plugins)
		t.Fatalf("the host did not make the plugins' folder readable-labelled: %q (%v)", lbl, err)
	}
	f.run(t, "Rename-Item -LiteralPath '"+plugins+"' -NewName moved", true)
	if _, err := os.Stat(plugins); err != nil {
		t.Fatalf("the plugins' folder was renamed away behind the wall: %v", err)
	}
}
