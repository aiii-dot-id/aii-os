package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// checkpoint_test.go — test/checkpoint.sh writes the qualification record
// beside a staged binary, and writes NOTHING unless the gate passed, its
// sdkcompat stage exited 0, its evidence verifies, and the binary is the
// gate's commit, clean, built by the gate's Go.
//
// The fixture is a repository with a history to read (a broker change, an
// unrelated one, a pin bump), a binary built from it with the toolchain
// the test runs under, and an evidence directory shaped like the gate's,
// sealed by the gate's own sealer.

type checkpointFixture struct {
	repo, evidence, binary, base, head, goBin string
}

func newCheckpointFixture(t *testing.T) checkpointFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the checkpoint is a POSIX shell script, run by the landing chain")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		if goBin, err = exec.LookPath("go"); err != nil {
			t.Skip("no Go toolchain")
		}
	}
	f := checkpointFixture{repo: t.TempDir(), evidence: t.TempDir(), goBin: goBin}
	git(t, f.repo, "init", "-q", "-b", "main")
	for _, name := range []string{"checkpoint.sh", "toolchain.sh"} {
		script, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		write(t, f.repo, filepath.Join("test", name), string(script))
	}
	write(t, f.repo, "VERSION", "9.8.7\n")
	write(t, f.repo, "go.mod", "module fixture\n\ngo 1.24\n")
	write(t, f.repo, "main.go", "package main\n\nfunc main() {}\n")
	write(t, f.repo, "test/sdk-compat.rev", strings.Repeat("1", 40)+"\n")
	git(t, f.repo, "add", "-A")
	git(t, f.repo, "commit", "-qm", "base")
	f.base = strings.TrimSpace(git(t, f.repo, "rev-parse", "HEAD"))
	for _, c := range [][2]string{
		{"internal/broker/rings.txt", "broker: a plugin sees this"},
		{"README.md", "readme: nothing a plugin sees"},
		{"test/sdk-compat.rev", "test: the pin moves"},
	} {
		write(t, f.repo, c[0], c[1]+"\n")
		git(t, f.repo, "add", "-A")
		git(t, f.repo, "commit", "-qm", c[1])
	}
	f.head = strings.TrimSpace(git(t, f.repo, "rev-parse", "HEAD"))
	f.binary = filepath.Join(t.TempDir(), "aii-os-"+f.head[:8])
	f.build(t)

	goVersion, err := exec.Command(goBin, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(git(t, f.repo, "rev-parse", "HEAD^{tree}"))
	write(t, f.evidence, "gate.log", "gate commit="+f.head+" tree="+tree+" dirty=no go=\""+strings.TrimSpace(string(goVersion))+"\" toolchain="+goBin+" asked="+goBin+" host=fixture scope=full started=2026-10-01T00:00:00Z\n"+
		"stage=scope start=2026-10-01T00:00:01Z end=2026-10-01T00:10:00Z exit=0 log=scope.log\n"+
		"stage=sdkcompat start=2026-10-01T00:10:00Z end=2026-10-01T00:12:00Z exit=0 log=sdkcompat.log\n"+
		"verdict=PASS ended=2026-10-01T00:20:00Z\n")
	write(t, f.evidence, "scope.log", "ok\n")
	write(t, f.evidence, "sdkcompat.log", "sdk revision="+strings.Repeat("2", 40)+" checkout=/sdk\n"+
		"sdk tinygo=\"tinygo version 0.42.0 linux/amd64\" go=\"go version go1.27.1 linux/amd64\"\n"+
		"sdk package compat-0.1.0.aiiospkg sha256=abc\n"+
		"=== RUN   TestSDKCompat\n"+
		"--- PASS: TestSDKCompat (6.62s)\n"+
		"    --- PASS: TestSDKCompat/T1 (4.55s)\n"+
		"        --- PASS: TestSDKCompat/T1/grants (0.03s)\n"+
		"sdkcompat exit=0\n")
	f.seal(t)
	return f
}

func (f checkpointFixture) build(t *testing.T) {
	t.Helper()
	cmd := exec.Command(f.goBin, "build", "-o", f.binary, ".")
	cmd.Dir = f.repo
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the fixture binary: %v\n%s", err, out)
	}
}

func (f checkpointFixture) seal(t *testing.T) {
	t.Helper()
	sealer, err := filepath.Abs("evidence-seal.sh")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", sealer, f.evidence).CombinedOutput(); err != nil {
		t.Fatalf("seal: %v\n%s", err, out)
	}
}

// run runs the checkpoint and answers its exit, its output, and the record
// it wrote ("" when it wrote none).
func (f checkpointFixture) run(t *testing.T) (int, string, string) {
	t.Helper()
	cmd := exec.Command("sh", "test/checkpoint.sh", f.evidence, f.binary, f.base)
	cmd.Dir = f.repo
	cmd.Env = append(os.Environ(), "AII_GO="+f.goBin)
	out, err := cmd.CombinedOutput()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the checkpoint: %v\n%s", err, out)
	}
	record, rerr := os.ReadFile(f.binary + ".qual")
	if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatal(rerr)
	}
	return status, string(out), string(record)
}

// A QUALIFIED BINARY GETS ITS RECORD: the commit, tree, version, Go and
// artifact digest; the SDK lines the stage printed; the gate's verdict and
// the digest of its seal; one line per test that passed; and every commit
// since the base that touched what a plugin can see — and nothing else.
func TestTheCheckpointRecordsAQualifiedBinary(t *testing.T) {
	f := newCheckpointFixture(t)
	status, out, record := f.run(t)
	if status != 0 || record == "" {
		t.Fatalf("a qualified binary must get its record (exit %d):\n%s", status, out)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(f.binary + ".qual"); err != nil || info.Mode().Perm() != 0o644 {
			t.Errorf("the record's mode is %v (%v), want 0644", info.Mode().Perm(), err)
		}
	}
	broker := strings.TrimSpace(git(t, f.repo, "log", "-1", "--format=%H", "--", "internal/broker"))
	readme := strings.TrimSpace(git(t, f.repo, "log", "-1", "--format=%H", "--", "README.md"))
	for _, want := range []string{
		"checkpoint commit=" + f.head + " tree=", " version=9.8.7 go=go", " sha256=",
		"sdk revision=" + strings.Repeat("2", 40),
		`sdk tinygo="tinygo version 0.42.0 linux/amd64"`,
		"sdk package compat-0.1.0.aiiospkg sha256=abc",
		"gate verdict=PASS evidence=" + f.evidence + " sha256sums=",
		"test TestSDKCompat PASS\n", "test TestSDKCompat/T1 PASS\n", "test TestSDKCompat/T1/grants PASS\n",
		"change " + broker + " broker: a plugin sees this\n",
		"change " + f.head + " test: the pin moves\n",
	} {
		if !strings.Contains(record, want) {
			t.Errorf("the record lacks %q:\n%s", want, record)
		}
	}
	if strings.Contains(record, readme) || strings.Contains(record, "change none") {
		t.Errorf("the record names a change a plugin cannot see:\n%s", record)
	}

	// A landing that touched nothing a plugin sees says so in one line.
	g := f
	g.base = f.head
	if _, out, record := g.run(t); !strings.Contains(record, "change none\n") {
		t.Fatalf("no change since the base must read change none:\n%s\n%s", record, out)
	}
}

// NOTHING IS WRITTEN UNLESS EVERYTHING HOLDS, and the refusal names what
// did not.
func TestTheCheckpointWritesNothingUnlessEveryConditionHolds(t *testing.T) {
	for _, c := range []struct {
		name, want string
		breakIt    func(t *testing.T, f checkpointFixture)
	}{
		{"one stage only", "covers only sdkcompat", func(t *testing.T, f checkpointFixture) {
			// A genuine GATE_ONLY=sdkcompat receipt: its scope and its one stage.
			f.rewrite(t, "gate.log", " scope=full ", " scope=sdkcompat ")
			f.rewrite(t, "gate.log", "stage=scope start=2026-10-01T00:00:01Z end=2026-10-01T00:10:00Z exit=0 log=scope.log\n", "")
		}},
		{"no recorded scope", "no recorded scope", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", " scope=full ", " ")
		}},
		{"a failed stage", "stage=scope did not exit 0", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", "exit=0 log=scope.log", "exit=1 log=scope.log")
		}},
		{"a missing stage", "race scopes", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", "stage=scope start=2026-10-01T00:00:01Z end=2026-10-01T00:10:00Z exit=0 log=scope.log\n", "")
		}},
		{"dirty", "dirty tree", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", " dirty=no ", " dirty=yes ")
		}},
		{"verdict", "the gate did not pass", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", "verdict=PASS", "verdict=FAIL")
		}},
		{"sdkcompat", "sdkcompat did not exit 0", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", "exit=0 log=sdkcompat.log", "exit=1 log=sdkcompat.log")
		}},
		{"seal", "does not verify", func(t *testing.T, f checkpointFixture) {
			write(t, f.evidence, "scope.log", "altered after the seal\n")
		}},
		{"revision", "the gate proved", func(t *testing.T, f checkpointFixture) {
			f.rewrite(t, "gate.log", "gate commit="+f.head, "gate commit="+f.base)
		}},
		{"modified", "modified tree", func(t *testing.T, f checkpointFixture) {
			write(t, f.repo, "main.go", "package main\n\nfunc main() { println() }\n")
			f.build(t)
		}},
		{"go", "the gate ran", func(t *testing.T, f checkpointFixture) {
			b, err := os.ReadFile(filepath.Join(f.evidence, "gate.log"))
			if err != nil {
				t.Fatal(err)
			}
			line := strings.SplitN(string(b), "\n", 2)[0]
			goField := line[strings.Index(line, ` go="`):]
			goField = goField[:strings.Index(goField[5:], `"`)+6]
			f.rewrite(t, "gate.log", goField, ` go="go version go1.0.0 plan9/386"`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCheckpointFixture(t)
			c.breakIt(t, f)
			status, out, record := f.run(t)
			if status == 0 || record != "" {
				t.Fatalf("a record was written (exit %d):\n%s\n%s", status, out, record)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("the refusal must say %q:\n%s", c.want, out)
			}
		})
	}
}

// rewrite edits one evidence file and reseals, so only the named fault
// remains.
func (f checkpointFixture) rewrite(t *testing.T, name, old, new string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.evidence, name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s holds no %q", name, old)
	}
	write(t, f.evidence, name, strings.Replace(string(b), old, new, 1))
	f.seal(t)
}
