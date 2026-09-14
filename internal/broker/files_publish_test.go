package broker

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func hexSum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// .
// .
// .
// .
// .
// .
func TestPublishReplacesWholeOrNothing(t *testing.T) {
	data := t.TempDir()
	st := newStore(t)
	h := newHost(t, st, Config{MaxFilesBytes: 4 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
	private := filepath.Join(data, "p")
	b.SetFiles(private, 0)
	target := filepath.Join(private, "uid", "snapshot.json")

	first := []byte(`{"gen":1}`)
	m := dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data_b64":"`+base64.StdEncoding.EncodeToString(first)+`","expected_absent":true}`))
	wantResult(t, m, statusSucceeded, "")
	or := opResult(t, m)
	if or["sha256"] != hexSum(first) || or["size"] != float64(len(first)) || or["replaced"] != false || or["durable"] != true {
		t.Fatalf("first publication: %v", or)
	}
	if string(readFile(t, target)) != string(first) {
		t.Fatal("the file holds the published bytes")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(target); fi.Mode().Perm()&0o111 != 0 {
			t.Fatal("never executable")
		}
	}
	// .
	m = dispatch(t, b, fsParams("fs.read", "private", "uid/snapshot.json", ""))
	wantResult(t, m, statusSucceeded, "")
	if opResult(t, m)["sha256"] != hexSum(first) {
		t.Fatalf("a whole read carries the digest: %v", opResult(t, m))
	}
	m = dispatch(t, b, fsParams("fs.read", "private", "uid/snapshot.json", `{"offset":2,"length":3}`))
	if _, has := opResult(t, m)["sha256"]; has {
		t.Fatal("a piece carries no digest unless asked")
	}
	m = dispatch(t, b, fsParams("fs.read", "private", "uid/snapshot.json", `{"offset":2,"length":3,"digest":true}`))
	if opResult(t, m)["sha256"] != hexSum(first) {
		t.Fatalf("a piece carries the whole file's digest when asked: %v", opResult(t, m))
	}

	// .
	second := []byte(`{"gen":2,"speakers":["james"]}`)
	m = dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data_b64":"`+base64.StdEncoding.EncodeToString(second)+`","expected_sha256":"`+hexSum(first)+`"}`))
	wantResult(t, m, statusSucceeded, "")
	if or := opResult(t, m); or["replaced"] != true || or["sha256"] != hexSum(second) {
		t.Fatalf("second publication: %v", or)
	}
	stale := dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data":"stale","expected_sha256":"`+hexSum(first)+`"}`))
	wantResult(t, stale, statusFailed, reasonFSGenerationMismatch)
	if string(readFile(t, target)) != string(second) {
		t.Fatal("a refused publication leaves the file as it was")
	}
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data":"x","expected_absent":true}`)), statusFailed, reasonFSGenerationMismatch)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/other.json", `{"data":"x","expected_sha256":"`+hexSum(first)+`"}`)), statusFailed, reasonFSGenerationMismatch)
	if _, err := os.Stat(filepath.Join(private, "uid", "other.json")); !os.IsNotExist(err) {
		t.Fatal("a refused first publication creates nothing")
	}

	// .
	big := []byte(strings.Repeat("0123456789abcdef", 24*1024))
	m = dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data_b64":"`+base64.StdEncoding.EncodeToString(big)+`"}`))
	wantResult(t, m, statusDenied, reasonNetRequestTooBig)
	for i := 0; i < 3; i++ {
		piece := big[i*128*1024 : (i+1)*128*1024]
		wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "uid/.staging/snapshot.json", `{"data_b64":"`+base64.StdEncoding.EncodeToString(piece)+`","append":true}`)), statusSucceeded, "")
	}
	wrong := dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"from":"uid/.staging/snapshot.json","sha256":"`+hexSum([]byte("not it"))+`","expected_sha256":"`+hexSum(second)+`"}`))
	wantResult(t, wrong, statusFailed, reasonFSDigestMismatch)
	if string(readFile(t, target)) != string(second) {
		t.Fatal("a staged file that does not measure up replaces nothing")
	}
	if _, err := os.Stat(filepath.Join(private, "uid", ".staging", "snapshot.json")); err != nil {
		t.Fatal("the staged file is left for the caller")
	}
	m = dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"from":"uid/.staging/snapshot.json","sha256":"`+hexSum(big)+`","expected_sha256":"`+hexSum(second)+`"}`))
	wantResult(t, m, statusSucceeded, "")
	if or := opResult(t, m); or["size"] != float64(len(big)) || or["sha256"] != hexSum(big) || or["replaced"] != true {
		t.Fatalf("staged publication: %v", or)
	}
	if string(readFile(t, target)) != string(big) {
		t.Fatal("the staged bytes took the name whole")
	}
	if _, err := os.Stat(filepath.Join(private, "uid", ".staging", "snapshot.json")); !os.IsNotExist(err) {
		t.Fatal("the staged file was moved, not copied")
	}
	entries, _ := os.ReadDir(filepath.Join(private, "uid"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".publish-") {
			t.Fatalf("no temporary file survives a publication: %s", e.Name())
		}
	}

	// .
	for name, args := range map[string]string{
		"both":         `{"data":"x","from":"uid/.staging/snapshot.json","sha256":"` + hexSum(big) + `"}`,
		"neither":      `{}`,
		"from no sha":  `{"from":"uid/.staging/snapshot.json"}`,
		"bad sha":      `{"data":"x","sha256":"zz"}`,
		"both expects": `{"data":"x","expected_sha256":"` + hexSum(big) + `","expected_absent":true}`,
	} {
		wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", args)), statusDenied, reasonArgumentInvalid)
		_ = name
	}
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data":"x","mode":"0755"}`)), statusDenied, reasonNetUnknownArgument)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"from":"uid/snapshot.json","sha256":"`+hexSum(big)+`"}`)), statusDenied, reasonFSPathInvalid)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"from":"uid/.staging/missing","sha256":"`+hexSum(big)+`"}`)), statusFailed, reasonFSNotFound)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "uid/snapshot.json", `{"data":"x","sha256":"`+hexSum([]byte("y"))+`"}`)), statusFailed, reasonFSDigestMismatch)
	if string(readFile(t, target)) != string(big) {
		t.Fatal("no refusal touched the published file")
	}
	// .
	small := newHost(t, st, Config{MaxFilesBytes: 64})
	sb := small.Bind("q", packagefmt.TierT1, []string{"fs.private"})
	sb.SetFiles(filepath.Join(data, "q"), 0)
	wantResult(t, dispatch(t, sb, fsParams("fs.publish", "private", "s.json", `{"data":"`+strings.Repeat("a", 40)+`"}`)), statusSucceeded, "")
	wantResult(t, dispatch(t, sb, fsParams("fs.publish", "private", "s.json", `{"data":"`+strings.Repeat("b", 40)+`"}`)), statusFailed, reasonFSQuotaExceeded)
	if got := readFile(t, filepath.Join(data, "q", "s.json")); string(got) != strings.Repeat("a", 40) {
		t.Fatalf("over quota, the prior file stands: %q", got)
	}
}

// .
// .
func TestPublishWaitsUnderSAFE(t *testing.T) {
	data := t.TempDir()
	safe := false
	h := newHost(t, newStore(t), Config{MaxFilesBytes: 1 << 20, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
	b.SetFiles(filepath.Join(data, "p"), 0)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "private", "s.json", `{"data":"one"}`)), statusSucceeded, "")
	safe = true
	reply, err := b.Dispatch(t.Context(), methodInvokeCall, []byte(fsParams("fs.publish", "private", "s.json", `{"data":"two"}`)))
	if err != nil || !strings.Contains(string(reply), "SAFE") {
		t.Fatalf("SAFE refuses a publication: %v %s", err, reply)
	}
	m := dispatch(t, b, fsParams("fs.read", "private", "s.json", ""))
	wantResult(t, m, statusSucceeded, "")
	if opResult(t, m)["sha256"] != hexSum([]byte("one")) {
		t.Fatal("under SAFE the prior generation is what is read")
	}
}

// .
// .
// .
// .
func TestPublishSerializesAcrossBindingsOfOnePlugin(t *testing.T) {
	data := t.TempDir()
	h := newHost(t, newStore(t), Config{MaxFilesBytes: 1 << 20})
	private := filepath.Join(data, "p")
	old := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
	old.SetFiles(private, 0)
	next := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
	next.SetFiles(private, 0)
	wantResult(t, dispatch(t, old, fsParams("fs.publish", "private", "counter", `{"data":"0","expected_absent":true}`)), statusSucceeded, "")

	const perWriter, writers = 20, 2
	var wg sync.WaitGroup
	var retries, done int64
	var mu sync.Mutex
	for _, b := range []*Binding{old, next} {
		wg.Add(1)
		go func(b *Binding) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				for attempt := 0; attempt < 200; attempt++ {
					m := dispatch(t, b, fsParams("fs.read", "private", "counter", ""))
					or := opResult(t, m)
					raw, _ := base64.StdEncoding.DecodeString(or["data_b64"].(string))
					n := 0
					for _, c := range strings.TrimSpace(string(raw)) {
						n = n*10 + int(c-'0')
					}
					value := []byte(itoa(n + 1))
					pm := dispatch(t, b, fsParams("fs.publish", "private", "counter", `{"data":"`+string(value)+`","expected_sha256":"`+or["sha256"].(string)+`"}`))
					var status struct {
						Status string `json:"status"`
						Reason string `json:"reason_code"`
					}
					_ = json.Unmarshal(pm["status"], &status.Status)
					if status.Status == statusSucceeded {
						mu.Lock()
						done++
						mu.Unlock()
						break
					}
					mu.Lock()
					retries++
					mu.Unlock()
				}
			}
		}(b)
	}
	wg.Wait()
	got := strings.TrimSpace(string(readFile(t, filepath.Join(private, "counter"))))
	if got != itoa(perWriter*writers) || done != perWriter*writers {
		t.Fatalf("every increment landed exactly once: counter=%s done=%d retries=%d", got, done, retries)
	}
	t.Logf("counter=%s done=%d retries=%d", got, done, retries)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
