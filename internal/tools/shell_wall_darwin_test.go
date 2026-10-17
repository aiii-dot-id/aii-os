//go:build darwin && !ios

package tools

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// wallFixture is a home with the places a stock layout keeps, a sibling
// slot, and a Registry whose shell is walled from all of them.
type wallFixture struct {
	reg     *Registry
	home    string
	sibling string
}

func newWallFixture(t *testing.T) wallFixture {
	t.Helper()
	if _, err := os.Stat(sandboxExec); err != nil {
		t.Skipf("no %s on this host: %v", sandboxExec, err)
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	data := filepath.Join(home, "data")
	sibling := filepath.Join(base, "sibling")
	files := map[string]string{
		filepath.Join(data, "identity.sec"):            "KEPT SECRET\n",
		filepath.Join(data, "snapshot.key"):            "KEPT SECRET\n",
		filepath.Join(data, "credentials", "a.json"):   "KEPT SECRET\n",
		filepath.Join(home, "config.json"):             "KEPT SECRET\n",
		filepath.Join(home, "providers.json"):          "KEPT SECRET\n",
		filepath.Join(sibling, "data", "identity.sec"): "KEPT SECRET\n",
		filepath.Join(data, "ui", "frame.html"):        "OPEN FRAME\n",
		filepath.Join(home, "notes.md"):                "a note of the home\n",
	}
	for f, body := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A kept place not made yet: the updater's beside-the-program files, by
	// a stem, and a plain place no file stands at.
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Except: []string{filepath.Join(data, "ui")}, Reason: "kept", Enforced: true},
		{ID: "sub.config", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "config.json"), Reason: "kept", Enforced: true},
		{ID: "sub.providers", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "providers.json"), Reason: "kept", Enforced: true},
		{ID: "sub.later", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "later"), Reason: "kept", Enforced: true},
	})
	reg := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})
	// The places the boundary alone keeps: the sibling slot. Asked at each
	// call, as the app wires it.
	reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: sibling, Shown: "other identities' slots", Reason: "kept", Enforced: true}}, nil
	})
	return wallFixture{reg: reg, home: home, sibling: sibling}
}

// refusedByKernel asserts the command ran (the word check did not refuse
// it) and the kernel wall blocked it: no kept secret, a non-zero exit.
func refusedByKernel(t *testing.T, f wallFixture, name, cmd string) {
	t.Helper()
	res := modelCall(t, f.reg, "shell", map[string]any{"command": cmd})
	if res.Refused {
		t.Fatalf("%s: the WORD check refused it (%q); craft a command it cannot see so the WALL is what is tested", name, res.Error)
	}
	if strings.Contains(res.Output, "KEPT SECRET") {
		t.Fatalf("%s: the kept secret was read behind the wall:\n%s", name, res.Output)
	}
	if res.ExitStatus != nil && *res.ExitStatus == 0 {
		t.Fatalf("%s: the command behind the wall exited 0 — it was not refused:\n%s", name, res.Output)
	}
	if !strings.Contains(res.Output, "Operation not permitted") {
		t.Fatalf("%s: expected 'Operation not permitted', got:\n%s", name, res.Output)
	}
}

// allowedByWall asserts a control command ran and succeeded behind the wall.
func allowedByWall(t *testing.T, f wallFixture, name, cmd, want string) {
	t.Helper()
	res := modelCall(t, f.reg, "shell", map[string]any{"command": cmd})
	if res.Refused {
		t.Fatalf("%s: a control was refused by the word check: %q", name, res.Error)
	}
	if res.ExitStatus == nil || *res.ExitStatus != 0 {
		t.Fatalf("%s: a control failed behind the wall (exit %v):\n%s", name, res.ExitStatus, res.Output)
	}
	if want != "" && !strings.Contains(res.Output, want) {
		t.Fatalf("%s: a control did not produce %q:\n%s", name, want, res.Output)
	}
}

func TestOnAMacTheShellCannotReadAKeptFileHoweverItIsNamed(t *testing.T) {
	f := newWallFixture(t)
	// A file inside the kept data directory, by a path built from halves.
	refusedByKernel(t, f, "built string, data file", `d=da; e=ta; n=iden; m=tity.sec; cat "$d$e/$n$m"`)
	// config.json and providers.json, kept as files, by a variable.
	refusedByKernel(t, f, "variable, config", `p=conf; q=ig.json; cat "$p$q"`)
	refusedByKernel(t, f, "printf-built, providers", `cat "$(printf '%s%s' provider s.json)"`)
	// The snapshot key and a credential inside data.
	refusedByKernel(t, f, "built string, snapshot", `a=snap; b=shot.key; cat "$(printf 'da')ta/$a$b"`)
	refusedByKernel(t, f, "printf, credential", `cat "$(printf 'da')ta/$(printf 'cre')dentials/a.json"`)
	// A child interpreter opening the file.
	refusedByKernel(t, f, "python open", `python3 -c 'print(open("da"+"ta/iden"+"tity.sec").read())'`)
	// A path the word check cannot see at all: base64, decoded in the shell.
	refusedByKernel(t, f, "base64-decoded path", `cat "$(echo ZGF0YS9pZGVudGl0eS5zZWM= | base64 -D)"`)
	// find -exec, which names no kept path on the command line the word
	// check sees for the file itself.
	refusedByKernel(t, f, "find -exec", `find . -name 'iden*.sec' -exec cat {} \;`)
	// A symlink made in the home pointing at a kept place.
	refusedByKernel(t, f, "symlink in home", `ln -s "$(printf 'da')ta" lk; n=iden; m=tity.sec; cat "lk/$n$m"`)
	// Every control: what is open, and everyday tools.
	allowedByWall(t, f, "home note", `cat notes.md`, "a note of the home")
	allowedByWall(t, f, "data/ui exception", `cat data/ui/frame.html`, "OPEN FRAME")
	allowedByWall(t, f, "write home", `echo hi > out.txt && cat out.txt`, "hi")
}

// ROW 5: with the sandbox off, the kernel keeps refusing the kept places —
// the substrate and the places the boundary alone kept (a sibling slot) —
// even where the word check now lets an outside path through. The profile
// is the same list on or off.
func TestOnAMacWithTheSandboxOffTheKeptPlacesAreStillRefused(t *testing.T) {
	f := newWallFixture(t)
	f.reg.SetSandboxOff(true)
	if !f.reg.SandboxOff() {
		t.Fatal("the sandbox did not turn off")
	}
	// A substrate file, by a built name (the word check's name rule would
	// catch the plain spelling even off).
	refusedByKernel(t, f, "config off", `p=conf; q=ig.json; cat "$p$q"`)
	// The sibling slot, by its absolute path. With the sandbox off the word
	// check lets an outside absolute path through; the kernel wall does not.
	sib := f.sibling
	refusedByKernel(t, f, "sibling off", `h='`+sib+`'; n=iden; m=tity.sec; cat "$h/data/$n$m"`)
	// A control: with the boundary off, a read of a file that is open
	// outside the home works (row 2 is not in this commit; the boundary
	// does not refuse writes here).
	outside := filepath.Join(t.TempDir(), "open.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE OK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowedByWall(t, f, "outside read, off", `cat '`+outside+`'`, "OUTSIDE OK")
}

func TestOnAMacTheShellReadsAKeptFileByASecondSpelling(t *testing.T) {
	f := newWallFixture(t)
	resolved, err := filepath.EvalSymlinks(f.home)
	if err != nil {
		t.Fatal(err)
	}
	// An upper-cased spelling of the data file (APFS folds case), built so
	// the word check sees no kept name.
	refusedByKernel(t, f, "upper-cased", `d=DA; e=TA; n=IDEN; m=TITY.SEC; cat "$d$e/$n$m"`)
	// The system data-volume firmlink form of config.json — the mount point
	// is read from the system (dataVolume, statfs), never spelled here — built
	// so the word check sees no kept name. On a host where the home is on the
	// boot volume there is no second mount point and this spelling is the
	// first: skip, saying so.
	if dataVolume == "" {
		t.Skip("the home is on the boot volume: no second data-volume spelling here")
	}
	refusedByKernel(t, f, "data-volume firmlink spelling",
		`h='`+dataVolume+resolved+`'; n=conf; m=ig.json; cat "$h/$n$m"`)
}

func TestOnAMacTheShellCannotReachAPlaceNotMadeYet(t *testing.T) {
	f := newWallFixture(t)
	// "later" is a kept place no file stands at. Creating it, or any use of
	// it, is refused.
	refusedByKernel(t, f, "mkdir a place not made", `l=lat; m=er; mkdir "$l$m" && echo ok`)
	refusedByKernel(t, f, "write a place not made", `l=lat; m=er; echo x > "$l$m"`)
}

func TestOnAMacAKeptPlaceIsNotMovedOutFromUnderItsRule(t *testing.T) {
	f := newWallFixture(t)
	// Renaming the data directory, or an ancestor, would move the place out
	// from under its rule; the ancestor lines refuse it.
	refusedByKernel(t, f, "mv the data dir", `d=da; e=ta; mv "$d$e" d2 && cat d2/iden*.sec`)
}

func TestOnAMacRowThreeRefusesOpenAndLaunchd(t *testing.T) {
	f := newWallFixture(t)
	// open (LaunchServices) is refused; its program runs nothing on the
	// desktop.
	if res := modelCall(t, f.reg, "shell", map[string]any{"command": `echo hi > open-me.txt && open -g -j open-me.txt`}); res.ExitStatus == nil || *res.ExitStatus == 0 {
		t.Fatalf("open behind the wall should fail: exit %v\n%s", res.ExitStatus, res.Output)
	}
	// launchctl submit and bootstrap are refused by the kernel.
	for _, c := range []string{
		`launchctl submit -l id.aiii.walltest.submit -- /usr/bin/true`,
		`launchctl bootstrap gui/$(id -u) /nonexistent.plist`,
	} {
		if res := modelCall(t, f.reg, "shell", map[string]any{"command": c}); res.ExitStatus != nil && *res.ExitStatus == 0 {
			t.Fatalf("launchd job creation behind the wall should fail: %q\n%s", c, res.Output)
		}
	}
	// No such job exists afterwards: launchctl print exits non-zero for a
	// job that is not there (its "Could not find service" message echoes the
	// name, so the exit status, not the text, is what says so).
	if err := exec.Command("launchctl", "print", "gui/"+itoa(os.Getuid())+"/id.aiii.walltest.submit").Run(); err == nil {
		t.Fatal("a launchd job was created behind the wall")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestOnAMacTheShellCannotAskSpotlight(t *testing.T) {
	f := newWallFixture(t)
	mdlsHome := `mdls -name kMDItemFSName notes.md`
	mdfindSystem := `mdfind "kMDItemCFBundleIdentifier == 'com.apple.calculator'"`

	// Red proof first: the profile this call would get, minus the Spotlight
	// line, answers both — so the refusals below are that line's doing.
	w, err := f.reg.shellWallData()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := shellProfile(f.home, w, userTempDirs())
	if err != nil {
		t.Fatal(err)
	}
	var without []string
	for _, l := range strings.Split(profile, "\n") {
		if !strings.Contains(l, "com.apple.metadata.mds") {
			without = append(without, l)
		}
	}
	if len(without) == len(strings.Split(profile, "\n")) {
		t.Fatalf("the profile has no Spotlight line:\n%s", profile)
	}
	run := func(p, cmd string) string {
		c := exec.Command(sandboxExec, "-p", p, "--", "/bin/bash", "-c", cmd)
		c.Dir, c.Env = f.home, shellEnv(f.home)
		out, _ := c.CombinedOutput()
		return string(out)
	}
	if out := run(strings.Join(without, "\n"), mdlsHome); !strings.Contains(out, `kMDItemFSName = "notes.md"`) {
		t.Skipf("Spotlight does not answer mdls on this host even without the line (%q): nothing to prove", out)
	}
	if out := run(strings.Join(without, "\n"), mdfindSystem); !strings.Contains(out, "Calculator.app") {
		t.Skipf("Spotlight does not answer mdfind on this host even without the line (%q): nothing to prove", out)
	}

	// Behind the wall, on and off, through the real shell tool.
	for _, off := range []bool{false, true} {
		f.reg.SetSandboxOff(off)
		if res := modelCall(t, f.reg, "shell", map[string]any{"command": mdlsHome}); res.Refused ||
			strings.Contains(res.Output, `kMDItemFSName = "notes.md"`) || res.ExitStatus == nil || *res.ExitStatus == 0 {
			t.Errorf("sandbox off=%v: mdls answered behind the wall: %+v", off, res)
		}
		if res := modelCall(t, f.reg, "shell", map[string]any{"command": mdfindSystem}); res.Refused || strings.Contains(res.Output, "Calculator.app") {
			t.Errorf("sandbox off=%v: mdfind answered behind the wall: %+v", off, res)
		}
		// Against a kept file: nothing of it comes back.
		for _, cmd := range []string{
			`p=conf; q=ig.json; mdls -name kMDItemFSName -name kMDItemTextContent "$p$q"`,
			`mdfind -onlyin "$(printf 'da')ta" "kMDItemFSName == 'iden*'"`,
		} {
			res := modelCall(t, f.reg, "shell", map[string]any{"command": cmd})
			if res.Refused || strings.Contains(res.Output, "KEPT SECRET") || strings.Contains(res.Output, "identity.sec") ||
				strings.Contains(res.Output, `kMDItemFSName = "config.json"`) {
				t.Errorf("sandbox off=%v: Spotlight said something of a kept file (%q): %+v", off, cmd, res)
			}
		}
	}
	f.reg.SetSandboxOff(false)
	// Ordinary tools still run behind the same profile.
	allowedByWall(t, f, "after spotlight", `ls && python3 -c 'print(6*7)'`, "42")
}

// ROW 12: the shell is never run bare. If the places that must stay closed
// cannot be named, or the profile cannot be written (a place Seatbelt's
// string reader cannot spell), the call is refused with the row-12 words
// and the typed code, and nothing runs.
func TestOnAMacTheShellDoesNotRunWithoutItsWall(t *testing.T) {
	// (a) The outer places cannot be named: the shell is refused, nothing
	// ran, the code is typed.
	f := newWallFixture(t)
	f.reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return nil, errOuterUnnamed
	})
	res := modelCall(t, f.reg, "shell", map[string]any{"command": "echo should-not-run"})
	if !res.Refused || res.ReasonCode != ReasonShellWallUnavailable {
		t.Fatalf("the shell must be refused when its wall cannot be built: %+v", res)
	}
	if strings.Contains(res.Output, "should-not-run") || res.ExitStatus != nil {
		t.Fatalf("nothing should have run: %+v", res)
	}
	if !strings.Contains(res.Error, "the shell did not run: on this system it runs only behind the operating system's wall") {
		t.Fatalf("the refusal must carry the row-12 words: %q", res.Error)
	}

	// (b) The profile cannot be written: a kept place whose path Seatbelt
	// cannot spell. The shell is refused, nothing ran.
	g := newWallFixture(t)
	g.reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{{ID: "sub.bad", Kind: firewall.KindSubstrate, Path: "/h/bad\xffname", Reason: "kept", Enforced: true}}, nil
	})
	res = modelCall(t, g.reg, "shell", map[string]any{"command": "echo should-not-run"})
	if !res.Refused || res.ReasonCode != ReasonShellWallUnavailable || res.ExitStatus != nil {
		t.Fatalf("an unspellable kept place must refuse the shell with nothing run: %+v", res)
	}
}

var errOuterUnnamed = &outerErr{}

type outerErr struct{}

func (*outerErr) Error() string {
	return "the account's other identities cannot be named from the machine service's state"
}

// ROW 6: the profile carries no exemption for the account it runs as — it is
// the same text whatever the uid, so an identity run as root is walled too.
// (Root itself cannot be exercised here: no sudo on this host.)
func TestOnAMacTheProfileHasNoRootExemption(t *testing.T) {
	p, err := firewall.ShellProfile(firewall.ShellWallInput{Kept: []*firewall.Rule{
		{ID: "sub.data", Path: "/h/data"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p, "no-sandbox") || strings.Contains(p, "uid") || strings.Contains(p, "getuid") {
		t.Fatalf("the profile must carry no exemption for the account it runs as:\n%s", p)
	}
	if !strings.Contains(p, `(deny file-read* file-write* (subpath "/h/data"))`) {
		t.Fatalf("the kept place must be denied unconditionally:\n%s", p)
	}
}

// RED PROOF: the same evasions, with no wall (a ShellTool built by hand has
// none), read the kept secret. So each refusal above is the wall's doing,
// not the command's.
func TestWithoutTheWallTheSameEvasionsReadTheSecret(t *testing.T) {
	if _, err := os.Stat(sandboxExec); err != nil {
		t.Skipf("no %s: %v", sandboxExec, err)
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "identity.sec"), []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No wall closure: behindWall builds a profile that keeps nothing.
	st := &ShellTool{timeout: 30 * time.Second, sandbox: home}
	res, err := st.Execute(context.Background(), map[string]any{"command": `d=da; e=ta; n=iden; m=tity.sec; cat "$d$e/$n$m"`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "KEPT SECRET") {
		t.Fatalf("without the wall the built-string read should leak the secret, so the wall is what stops it; got:\n%s", res.Output)
	}
}

func TestOnAMacAKeptPlaceNamedThroughALinkIsKept(t *testing.T) {
	f := newWallFixture(t)
	real := filepath.Join(t.TempDir(), "real-slot")
	if err := os.MkdirAll(filepath.Join(real, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "data", "identity.sec"), []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-to-slots")
	if err := os.Symlink(filepath.Dir(real), link); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(link, "real-slot")
	f.reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: named, Shown: "other identities' slots", Reason: "kept", Enforced: true}}, nil
	})
	for _, off := range []bool{false, true} {
		f.reg.SetSandboxOff(off)
		refusedByKernel(t, f, "by its real path", `h='`+real+`'; n=iden; m=tity.sec; cat "$h/data/$n$m"`)
		refusedByKernel(t, f, "by the link", `h='`+named+`'; n=iden; m=tity.sec; cat "$h/data/$n$m"`)
	}
}

func TestOnAMacAnExceptionReplacedByALinkOpensNothing(t *testing.T) {
	f := newWallFixture(t)
	data := filepath.Join(f.home, "data")
	for _, target := range []string{"/", f.sibling} {
		for _, off := range []bool{false, true} {
			f.reg.SetSandboxOff(off)
			// The target in hex, so the word check, which refuses an outside
			// path it can see, passes the command to the kernel.
			plant := `python3 -c 'import os,shutil,sys; p="da"+"ta/ui"; shutil.rmtree(p); os.symlink(bytes.fromhex(sys.argv[1]).decode(), p); print("planted")' ` + hex.EncodeToString([]byte(target))
			res := modelCall(t, f.reg, "shell", map[string]any{"command": plant})
			if fi, err := os.Lstat(filepath.Join(data, "ui")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("inconclusive (off=%v, link to %s): the link was not planted: %s%s", off, target, res.Error, res.Output)
			}
			res = modelCall(t, f.reg, "shell", map[string]any{"command": `p=conf; q=ig.json; cat "$p$q"; d=da; e=ta; n=iden; m=tity.sec; cat "$d$e/$n$m"; s='` + f.sibling + `'; cat "$s/data/$n$m"; cat "$d$e/ui/data/$n$m"`})
			if strings.Contains(res.Output, "KEPT SECRET") {
				t.Errorf("off=%v, data/ui a link to %s: a kept secret was read on the next call:\n%s", off, target, res.Output)
			}
			os.Remove(filepath.Join(data, "ui"))
			if err := os.MkdirAll(filepath.Join(data, "ui"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestOnAMacALinkOnAKeptPlacesNamedPathIsHeld(t *testing.T) {
	f := newWallFixture(t)
	real := filepath.Join(t.TempDir(), "real-slots")
	if err := os.MkdirAll(filepath.Join(real, "slot", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(f.home, "links") // in the home: writable with the boundary on
	if err := os.MkdirAll(links, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, "slots")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f.reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: filepath.Join(link, "slot"), Reason: "kept", Enforced: true}}, nil
	})
	for _, off := range []bool{false, true} {
		f.reg.SetSandboxOff(off)
		res := modelCall(t, f.reg, "shell", map[string]any{"command": `l=links/slo; m=ts; rm "$l$m" && mkdir -p "$l$m/slot" && echo swapped`})
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("off=%v: the link on the named path was replaced behind the wall:\n%s", off, res.Output)
		}
		if !strings.Contains(res.Output, "Operation not permitted") {
			t.Fatalf("off=%v: the swap was not refused by the kernel:\n%s%s", off, res.Error, res.Output)
		}
	}
}
