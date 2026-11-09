//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

// capacityProbeAsked is set in the environment of internal/app's test
// program when the qualification asks it for the host's capacity
// (TestVoiceQualificationCapacity skips without it).
const capacityProbeAsked = "AII_VOICE_QUALIFICATION_CAPACITY"

// voiceRow is one row of the Windows voice qualification: the run's inputs,
// and what the row asks of the host's selection.
type voiceRow struct {
	pkg, sha256 string // the signed package, and its digest as the run read it
	models      string // each model at its declared path
	assets      string // each runtime archive by its address's last segment
	recordings  string // private-0.pcm to private-3.pcm
	probe       string // internal/app's test program, which measures capacity
	evidence    string // this row's own folder
	expected    string // the component set the host's selection must choose
	budget      bool   // an operator plugin memory budget between Small and Full
	// A store whose write lock is held for storeHold each time a write of the
	// host's waits for it; the host must then have said a storage call of
	// slowAtLeast or more.
	storeHold, slowAtLeast time.Duration
}

// THE WINDOWS VOICE QUALIFICATION: voice's installed journeys, run against
// the signed voice package the operator's inputs directory holds, whichever
// build that is (the operator's ruling of 2026-10-07: the latest voice
// plugin build). A new voice release needs no change here: the package's
// digest, id and version are read at run time and recorded.
//
//	AII_WINDOWS_VOICE_FIXTURE   the inputs directory: exactly one signed
//	                            *.aiiospkg; assets/, each runtime archive by
//	                            its address's last segment; models/, each
//	                            model at its declared path; recordings/,
//	                            private-0.pcm to private-3.pcm (16 kHz mono
//	                            16-bit, one speaker)
//	AII_WINDOWS_VOICE_EVIDENCE  a directory; each run makes a new folder of
//	                            its own under it
//
// Without both it is skipped, by name; with AII_REQUIRED_WINDOWS_QUALIFICATION=1
// (test/native-run.sh sets it where the operator's profile names the inputs)
// it fails. The fixtures' own self-test runs first; then each journey twice:
// natural, where the host must choose the Full set, and budget, where an
// operator budget must make it choose Small. Every row's activation receipt
// must say tier 3, the set the row expects, and passed.
func TestWindowsVoiceQualification(t *testing.T) {
	inputs, out := os.Getenv("AII_WINDOWS_VOICE_FIXTURE"), os.Getenv("AII_WINDOWS_VOICE_EVIDENCE")
	refuse := t.Skip
	if os.Getenv("AII_REQUIRED_WINDOWS_QUALIFICATION") == "1" {
		refuse = t.Fatal
	}
	if inputs == "" || out == "" {
		refuse("the Windows voice qualification needs AII_WINDOWS_VOICE_FIXTURE (its inputs directory) and AII_WINDOWS_VOICE_EVIDENCE (where its evidence goes)")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		refuse("the Windows voice qualification's rows are the package's windows-x86_64 sets; this is " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	pkg := onlyPackage(t, inputs)
	raw, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	roots := packagefmt.ShippedRoots()
	roots.Revocation = packagefmt.LoadRevocationStatus(t.TempDir(), roots, nil)
	verified, err := packagefmt.VerifyFile(pkg, roots)
	if err != nil {
		t.Fatalf("%s does not verify against the roots this build ships: %v", pkg, err)
	}
	if verified.Tier != packagefmt.TierT3 {
		t.Fatalf("%s verifies at %v, not T3", pkg, verified.Tier)
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(out, "voice-qualification-"+time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"package": filepath.Base(pkg), "package_id": verified.Manifest.ID, "package_version": verified.Manifest.Version,
		"package_sha256": digest, "host_version": version.Authored(), "system": runtime.GOOS + "/" + runtime.GOARCH,
		"started_utc": time.Now().UTC().Format(time.RFC3339)}
	var rows []map[string]any
	t.Cleanup(func() {
		record["rows"], record["passed"], record["ended_utc"] = rows, !t.Failed(), time.Now().UTC().Format(time.RFC3339)
		data, err := json.MarshalIndent(record, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(run, "qualification.json"), append(data, '\n'), 0600)
		}
		if err != nil {
			t.Error(err)
		}
	})
	t.Logf("voice qualification: %s %s (sha256 %s) on host %s; evidence in %s", verified.Manifest.ID, verified.Manifest.Version, digest, version.Authored(), run)

	base := voiceRow{pkg: pkg, sha256: digest, models: filepath.Join(inputs, "models"), assets: filepath.Join(inputs, "assets"),
		recordings: filepath.Join(inputs, "recordings"), probe: buildCapacityProbe(t)}
	// The fixtures' own parts first; the rows run whatever it says.
	rows = append(rows, map[string]any{"row": "selftest", "passed": t.Run("selftest", journeySelfTest)})
	for _, j := range []struct {
		name    string
		journey func(*testing.T, voiceRow)
		held    bool
	}{
		{"standard", journeyStandard, false},
		{"language", journeyLanguage, false},
		{"replyvoice", journeyReplyVoice, false},
		{"latesettings", journeyLateSettings, false},
		{"storeheld", journeyStandard, true},
	} {
		for _, mode := range []string{"natural", "budget"} {
			r := base
			name := mode + "-" + j.name
			r.evidence = filepath.Join(run, name)
			r.budget = mode == "budget"
			r.expected = "windows-x86_64-full"
			if r.budget {
				r.expected = "windows-x86_64-small"
			}
			if j.held {
				r.storeHold, r.slowAtLeast = 2500*time.Millisecond, 1500*time.Millisecond
			}
			began := time.Now()
			passed := t.Run(name, func(t *testing.T) { j.journey(t, r) })
			said, err := voiceReceipt(r)
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			rows = append(rows, map[string]any{"row": name, "expected": r.expected, "passed": passed && err == nil,
				"receipt": said, "seconds": time.Since(began).Seconds()})
		}
	}
}

// onlyPackage is the one signed package in the inputs directory.
func onlyPackage(t *testing.T, inputs string) string {
	t.Helper()
	entries, err := os.ReadDir(inputs)
	if err != nil {
		t.Fatalf("the inputs directory (AII_WINDOWS_VOICE_FIXTURE): %v", err)
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".aiiospkg") {
			found = append(found, filepath.Join(inputs, e.Name()))
		}
	}
	if len(found) != 1 {
		t.Fatalf("the inputs directory %s holds %d signed packages (*.aiiospkg): the qualification runs exactly one", inputs, len(found))
	}
	return found[0]
}

// buildCapacityProbe builds internal/app's test program, whose
// TestVoiceQualificationCapacity is the host's production capacity
// measurement (this package cannot import internal/app).
func buildCapacityProbe(t *testing.T) string {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "capacity.test")
	if runtime.GOOS == "windows" {
		probe += ".exe"
	}
	if out, err := exec.Command("go", "test", "-c", "-o", probe, "../app").CombinedOutput(); err != nil {
		t.Fatalf("build internal/app's test program for the capacity measurement: %v\n%s", err, out)
	}
	return probe
}

// voiceReceipt reads a row's activation receipt and holds it to the row:
// tier 3, the set the row expects, and passed.
func voiceReceipt(r voiceRow) (string, error) {
	entries, err := os.ReadDir(r.evidence)
	if err != nil {
		return "", fmt.Errorf("no activation receipt: %v", err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-activation.json") {
			found = append(found, filepath.Join(r.evidence, e.Name()))
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("%d activation receipts, not one", len(found))
	}
	data, err := os.ReadFile(found[0])
	if err != nil {
		return "", err
	}
	var got struct {
		Tier       packagefmt.Tier `json:"tier"`
		Variant    string          `json:"variant"`
		Passed     bool            `json:"passed"`
		Activation float64         `json:"activation_seconds"`
		Slowest    float64         `json:"host_storage_call_slowest_seconds"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		return "", fmt.Errorf("the activation receipt: %v", err)
	}
	said := fmt.Sprintf("tier=%v set=%s passed=%v activation=%.1fs slowest_storage_call=%gs", got.Tier, got.Variant, got.Passed, got.Activation, got.Slowest)
	if got.Tier != packagefmt.TierT3 || got.Variant != r.expected || !got.Passed {
		return said, fmt.Errorf("receipt not as required (tier T3, set %s, passed): %s", r.expected, said)
	}
	return said, nil
}
