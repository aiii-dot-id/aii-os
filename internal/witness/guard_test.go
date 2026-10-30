package witness

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// guard_test.go — the witness files beside the ledger and a snapshot's
// capture of them (guard.go; P1 of the witness design, revision 5).

// witnessCut is what a capture holds when it runs at one instant: the
// record, every witness file beside it, and — so the cut can be restored
// against the witness as it stood then — the witness's rows.
type witnessCut struct {
	at      string
	ledger  []byte
	files   map[string][]byte
	witness map[string]anchorState
}

// watchingGuard is a FileGuard that stands where the ledger door does: it
// runs each section alone, and at each section's two edges it takes a cut,
// as a capture waiting on the door would. It also holds the witness files
// to one rule: they change only inside a section. A change between the
// end of one section and the start of the next is a writer that bypassed
// the guard.
type watchingGuard struct {
	t        *testing.T
	dir      string
	fw       *fakeWitness
	mu       sync.Mutex
	sections int
	lastExit map[string]string
	bypassed []string
	cuts     []witnessCut
}

// witnessFilesIn reads every witness file beside the ledger in dir: the
// files named witness-*, and the keys in witness-keys/.
func witnessFilesIn(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." || rel == WitnessKeysDirName {
				return nil
			}
			return fs.SkipDir
		}
		if strings.HasPrefix(rel, "witness-") || strings.HasPrefix(rel, WitnessKeysDirName+"/") {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = raw
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func digests(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for name, raw := range files {
		sum := sha256.Sum256(raw)
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

func changed(was, now map[string]string) []string {
	var out []string
	for name, d := range now {
		if was[name] != d {
			out = append(out, name)
		}
	}
	for name := range was {
		if _, ok := now[name]; !ok {
			out = append(out, name+" (removed)")
		}
	}
	sort.Strings(out)
	return out
}

func (g *watchingGuard) cut(at string) witnessCut {
	g.t.Helper()
	raw, err := os.ReadFile(filepath.Join(g.dir, "ledger.jsonl"))
	if err != nil {
		g.t.Fatal(err)
	}
	g.fw.mu.Lock()
	rows := map[string]anchorState{}
	for id, st := range g.fw.state {
		rows[id] = *st
	}
	g.fw.mu.Unlock()
	return witnessCut{at: at, ledger: raw, files: witnessFilesIn(g.t, g.dir), witness: rows}
}

func (g *watchingGuard) WitnessFiles(fn func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sections++
	if now := digests(witnessFilesIn(g.t, g.dir)); g.lastExit != nil {
		if moved := changed(g.lastExit, now); len(moved) > 0 {
			g.bypassed = append(g.bypassed, fmt.Sprintf("before section %d: %v", g.sections, moved))
		}
	}
	g.cuts = append(g.cuts, g.cut(fmt.Sprintf("before section %d", g.sections)))
	err := fn()
	g.lastExit = digests(witnessFilesIn(g.t, g.dir))
	g.cuts = append(g.cuts, g.cut(fmt.Sprintf("after section %d", g.sections)))
	return err
}

func guardedAnchoring(t *testing.T) (*watchingGuard, *anchoredRig) {
	t.Helper()
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 3)
	r := &anchoredRig{fw: fw, platformPath: p.writeEnv(t), lg: lg, kp: kp}
	g := &watchingGuard{t: t, dir: filepath.Dir(lg.Path()), fw: fw}
	// In front of the witness: its cadence refusal, when asked for, which
	// retires the bookmark the pass kept.
	var cadence atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/witness/bookmark" && cadence.Load() {
			w.WriteHeader(http.StatusConflict)
			writeTestJSON(w, map[string]string{"error": conflictMsgCadence, "code": codeCadence})
			return
		}
		fw.server.Config.Handler.ServeHTTP(w, req)
	}))
	t.Cleanup(front.Close)
	a := NewAnchorer(New(front.URL, ""), lg, AsIdentityKey(kp), &recordStore{lg: lg}, &recordStore{lg: lg}, testMinter{lg, kp}, 1, r.platformPath)
	a.SetFileGuard(g)
	fw.slowly(func() { appendRecords(t, lg, kp, 2) })
	for pass := 1; pass <= 2; pass++ {
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		appendRecords(t, lg, kp, 1)
	}
	cadence.Store(true)
	var ce *ConflictError
	if err := a.CheckAndAnchor(); !errors.As(err, &ce) || !ce.IsCadence() {
		t.Fatalf("rig: the third pass was to meet the cadence: %v", err)
	}
	cadence.Store(false)
	return g, r
}

// anchoredRig is what guardedAnchoring leaves: the witness, the platform
// its key is vouched by, the record and the identity's key.
type anchoredRig struct {
	fw           *fakeWitness
	platformPath string
	lg           *ledger.Ledger
	kp           *crypto.KeyPair
}

// NO WITNESS FILE CHANGES OUTSIDE THE GUARD. Three anchor passes of a new
// identity write every witness file there is to write: the envelope at
// enrolment, the witness's key, the bookmark before it leaves, the receipt
// kept and the bookmark retired, the receipt retired once the record
// carries it, the tail, and a bookmark retired when the witness refuses
// it. Between one section and the next nothing beside
// the ledger moves, and nothing after the last.
func TestNoWitnessFileChangesOutsideTheGuard(t *testing.T) {
	g, r := guardedAnchoring(t)
	if g.sections == 0 {
		t.Fatal("rig: the anchorer never took the guard")
	}
	if moved := changed(g.lastExit, digests(witnessFilesIn(t, filepath.Dir(r.lg.Path())))); len(moved) > 0 {
		g.bypassed = append(g.bypassed, fmt.Sprintf("after the last section: %v", moved))
	}
	if len(g.bypassed) > 0 {
		t.Fatalf("A WITNESS FILE CHANGED OUTSIDE THE GUARD A CAPTURE TAKES: %v", g.bypassed)
	}
	for _, want := range []string{EnvelopeFileName, TailFileName} {
		if _, ok := g.cuts[len(g.cuts)-1].files[want]; !ok {
			t.Fatalf("rig: the passes never wrote %s", want)
		}
	}
}

// A CAPTURE AT EVERY WITNESS-FILE BOUNDARY RESTORES TO A RECOVERABLE STATE.
// A capture waits on the door, so it lands at the edge of a section: each
// cut is one a snapshot could hold. Each is put back on its own, beside a
// witness as it stood at that instant, and the identity anchors from it:
// the receipt it carries pending is minted, the bookmark it kept is sent
// again and its answer minted late, and nothing is a conflict. Then the
// record verifies as boot verifies it, under the keys the cut carried.
func TestACaptureAtEveryWitnessFileBoundaryRestoresToARecoverableState(t *testing.T) {
	g, r := guardedAnchoring(t)
	platform, err := LoadPlatformEnvelope(r.platformPath)
	if err != nil {
		t.Fatal(err)
	}
	sawRequest, sawReceipt := false, false
	for _, c := range g.cuts {
		if _, ok := c.files[PendingRequestFileName]; ok {
			sawRequest = true
		}
		if _, ok := c.files[PendingReceiptFileName]; ok {
			sawReceipt = true
		}
		t.Run(c.at, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), c.ledger, 0o600); err != nil {
				t.Fatal(err)
			}
			for name, raw := range c.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			then := &fakeWitness{state: map[string]*anchorState{}, witnessKp: r.fw.witnessKp, witnessEnv: r.fw.witnessEnv, sawIdentityIDs: map[string]int{}, manifest: r.fw.manifest}
			for id, st := range c.witness {
				st := st
				then.state[id] = &st
			}
			startFakeWitness(t, then)
			lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lg.Close() })
			kp := r.kp
			db := &recordStore{lg: lg}
			a := NewAnchorer(New(then.server.URL, ""), lg, AsIdentityKey(kp), db, db, testMinter{lg, kp}, 1, r.platformPath)
			var alarms []*ConflictError
			a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })
			if err := a.SettlePending(); err != nil {
				t.Fatalf("THE CUT DOES NOT SETTLE: %v", err)
			}
			for pass := 1; pass <= 2; pass++ {
				appendRecords(t, lg, kp, 1)
				if err := a.CheckAndAnchor(); err != nil {
					t.Fatalf("THE CUT IS NOT RECOVERABLE: pass %d after restoring it: %v (alarms %v)", pass, err, alarms)
				}
			}
			heads, err := LoadHeadVerifier(dir, platform)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), heads); err != nil {
				t.Fatalf("the record restored from the cut and anchored on does not verify as boot verifies it: %v", err)
			}
			if ids, _ := witnessHolds(then); ids != 1 {
				t.Fatalf("the witness holds %d identities after the cut was restored", ids)
			}
		})
		if t.Failed() {
			return
		}
	}
	if !sawRequest || !sawReceipt {
		t.Fatalf("rig: no cut held a kept bookmark (%v) or a pending receipt (%v)", sawRequest, sawReceipt)
	}
}
