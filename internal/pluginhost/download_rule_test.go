//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// scripted is a counting fetcher: call n runs step n (the last step repeats),
// and every call's address and offset is kept.
type scripted struct {
	mu      sync.Mutex
	steps   []func(offset int64, w io.Writer) (int64, error)
	urls    []string
	offsets []int64
}

func (s *scripted) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	s.mu.Lock()
	s.urls = append(s.urls, url)
	s.offsets = append(s.offsets, offset)
	step := s.steps[min(len(s.offsets), len(s.steps))-1]
	s.mu.Unlock()
	return step(offset, w)
}

func (s *scripted) calls() (int, []int64, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.offsets), append([]int64(nil), s.offsets...), append([]string(nil), s.urls...)
}

// serve writes data from offset on, or its first cut bytes and then a
// network failure.
func serve(data []byte, cut int) func(int64, io.Writer) (int64, error) {
	return func(offset int64, w io.Writer) (int64, error) {
		end := int64(len(data))
		if cut > 0 {
			end = min(end, offset+int64(cut))
		}
		n, err := w.Write(data[offset:end])
		if err == nil && cut > 0 {
			err = &NetworkError{Cause: errors.New("connection reset by peer")}
		}
		return int64(n), err
	}
}

func answer(err error) func(int64, io.Writer) (int64, error) {
	return func(int64, io.Writer) (int64, error) { return 0, err }
}

// waits is the time seam: every wait asked for is kept, and passes at once.
type waits struct {
	mu   sync.Mutex
	asks []time.Duration
}

func (w *waits) after(d time.Duration) <-chan time.Time {
	w.mu.Lock()
	w.asks = append(w.asks, d)
	w.mu.Unlock()
	return nowAfter(d)
}

func (w *waits) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.asks)
}

// storeModel is a declared file and its bytes, and a plugin store of its
// own for it.
func storeModel(t *testing.T, size int) (ModelDecl, []byte, *plugstore.Writer) {
	t.Helper()
	data := bytes.Repeat([]byte("0123456789abcdef"), size/16+1)[:size]
	sum := sha256.Sum256(data)
	decl := ModelDecl{Name: "weights.bin", URL: "https://downloads.example.org/release/weights.bin", SHA256: hex.EncodeToString(sum[:]), Size: int64(size)}
	return decl, data, openStore(t)
}

// engineMaterial is the one material these tests ask for.
func engineMaterial(st *plugstore.Writer, decl ModelDecl) Material {
	return modelMaterial(st, "org.example.engine", "1", decl)
}

// ruleAcquirer is an Acquirer with no identity: the machine service's, one
// fetcher, one callback, the time seam.
func ruleAcquirer(t *testing.T, s *scripted, w *waits, st *plugstore.Writer) (*Acquirer, *readyLog) {
	t.Helper()
	var rl readyLog
	logf, _ := sinkLogf(t)
	q := storeAcquirer(st, s.fetch, AcquirerConfig{Ready: rl.ready, After: w.after, Logf: logf})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q.Attach(ctx)
	return q, &rl
}

func stopped(t *testing.T, q *Acquirer, id string) AcquireStatus {
	t.Helper()
	waitFor(t, "the download to stop", func() bool { st, _ := q.Status(id); return st.Remedy != "" })
	st, _ := q.Status(id)
	if st.Phase != PhaseWaiting || !st.RetryAt.IsZero() {
		t.Fatalf("a stopped download still shows a wait: %+v", st)
	}
	return st
}

func TestTheDownloadRuleNeedsNoIdentity(t *testing.T) {
	t.Run("a transient 503 is asked again and then succeeds", func(t *testing.T) {
		decl, data, st := storeModel(t, 4096)
		s := &scripted{steps: []func(int64, io.Writer) (int64, error){answer(&FetchStatusError{Status: 503}), serve(data, 0)}}
		w := &waits{}
		q, rl := ruleAcquirer(t, s, w, st)
		q.Want(engineMaterial(st, decl))
		waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
		if n, _, _ := s.calls(); n != 2 || w.count() != 1 {
			t.Fatalf("a 503 was not asked again once, after one wait: %d calls, %d waits", n, w.count())
		}
		if got := w.asks[0]; got > downloadWait(1) || got < downloadWait(1)*3/4 {
			t.Fatalf("the first wait is %s, not 5 s spread over its last quarter", got)
		}
	})
	t.Run("a 404 is the end at once", func(t *testing.T) {
		decl, _, st := storeModel(t, 4096)
		s := &scripted{steps: []func(int64, io.Writer) (int64, error){answer(&FetchStatusError{Status: 404})}}
		w := &waits{}
		q, _ := ruleAcquirer(t, s, w, st)
		m := engineMaterial(st, decl)
		q.Want(m)
		stop := stopped(t, q, m.PluginID)
		if n, _, _ := s.calls(); n != 1 || w.count() != 0 || !strings.Contains(stop.Remedy, "Nothing is at the address") || !strings.Contains(stop.LastError, "404") {
			t.Fatalf("a 404 was waited on or asked again: %d calls, %d waits, %+v", n, w.count(), stop)
		}
		// The activation is told, typed, and the facility presents it.
		var final *DownloadError
		_, err := Acquire(context.Background(), m, &Options{Acquirer: q})
		var asFinal pluginfacility.Final
		if !errors.As(err, &final) || !errors.As(err, &asFinal) || asFinal.Final() != stop.Remedy {
			t.Fatalf("the end did not reach the activation as a final, typed answer: %v", err)
		}
	})
	t.Run("a 403 on a resume is asked again of the package's own address once", func(t *testing.T) {
		decl, data, st := storeModel(t, 4096)
		s := &scripted{steps: []func(int64, io.Writer) (int64, error){serve(data, 1000), answer(&FetchStatusError{Status: 403}), serve(data, 0)}}
		w := &waits{}
		q, rl := ruleAcquirer(t, s, w, st)
		q.Want(engineMaterial(st, decl))
		waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
		n, offsets, urls := s.calls()
		if n != 3 || fmt.Sprint(offsets) != "[0 1000 1000]" || urls[2] != decl.URL {
			t.Fatalf("calls %d offsets %v urls %v", n, offsets, urls)
		}
		if got, err := os.ReadFile(modelFile(engineMaterial(st, decl).ModelsDir, decl)); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("the resumed file is not the declared bytes: %v", err)
		}
	})
}

func TestADownloadRefusedTwiceIsTheEnd(t *testing.T) {
	decl, _, st := storeModel(t, 4096)
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){answer(&FetchStatusError{Status: 403})}}
	w := &waits{}
	q, _ := ruleAcquirer(t, s, w, st)
	q.Want(engineMaterial(st, decl))
	stop := stopped(t, q, "org.example.engine")
	if n, _, _ := s.calls(); n != 2 || w.count() != 0 || !strings.Contains(stop.Remedy, "refused it again") {
		t.Fatalf("a 403 was not asked again exactly once before the end: %d calls, %d waits, %+v", n, w.count(), stop)
	}
}

func TestBytesThatDoNotHashAfterAWholeDownloadAreTheEnd(t *testing.T) {
	decl, data, st := storeModel(t, 4096)
	wrong := append([]byte(nil), data...)
	wrong[0] ^= 1
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){serve(wrong, 0)}}
	w := &waits{}
	q, _ := ruleAcquirer(t, s, w, st)
	q.Want(engineMaterial(st, decl))
	stop := stopped(t, q, "org.example.engine")
	if n, _, _ := s.calls(); n != 1 || w.count() != 0 || !strings.Contains(stop.Remedy, "not the ones the package declares") {
		t.Fatalf("a whole download that does not hash was asked again: %d calls, %d waits, %+v", n, w.count(), stop)
	}
	blob, _ := st.Blob(decl.SHA256)
	if _, err := os.Stat(blob + plugstore.PartialSuffix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the bytes that did not hash were kept: %v", err)
	}
}

func TestBytesThatDoNotHashAfterAResumeAreFetchedOnceWhole(t *testing.T) {
	for _, wholeIsRight := range []bool{true, false} {
		t.Run(fmt.Sprint("whole is right: ", wholeIsRight), func(t *testing.T) {
			decl, data, st := storeModel(t, 4096)
			// A partial kept from before whose bytes are not the file's.
			kept := append([]byte(nil), data[:1000]...)
			kept[10] ^= 1
			blob, _ := st.Blob(decl.SHA256)
			if err := os.WriteFile(blob+plugstore.PartialSuffix, kept, 0o600); err != nil {
				t.Fatal(err)
			}
			whole := data
			if !wholeIsRight {
				whole = append([]byte(nil), data...)
				whole[20] ^= 1
			}
			s := &scripted{steps: []func(int64, io.Writer) (int64, error){serve(data, 0), serve(whole, 0)}}
			w := &waits{}
			q, rl := ruleAcquirer(t, s, w, st)
			q.Want(engineMaterial(st, decl))
			if wholeIsRight {
				waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
			} else {
				stopped(t, q, "org.example.engine")
			}
			if n, offsets, _ := s.calls(); n != 2 || fmt.Sprint(offsets) != "[1000 0]" || w.count() != 0 {
				t.Fatalf("not resumed, then fetched once whole: %d calls, offsets %v, %d waits", n, offsets, w.count())
			}
		})
	}
}

func TestFiveAttemptsThatBringNothingEndTheDownloadUntilTryAgain(t *testing.T) {
	decl, data, st := storeModel(t, 4096)
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){answer(&NetworkError{Cause: errors.New("connection refused")})}}
	w := &waits{}
	q, rl := ruleAcquirer(t, s, w, st)
	m := engineMaterial(st, decl)
	q.Want(m)
	stop := stopped(t, q, m.PluginID)
	if n, _, _ := s.calls(); n != MaxDownloadAttempts || w.count() != MaxDownloadAttempts-1 || !strings.Contains(stop.Remedy, "5 attempts in a row") || !strings.Contains(stop.Remedy, "Try again") {
		t.Fatalf("five failures did not end the download with Try again: %d calls, %d waits, %+v", n, w.count(), stop)
	}
	for i, d := range w.asks {
		if full := downloadWait(i + 1); d > full || d < full-full/4 || d > time.Minute {
			t.Fatalf("wait %d is %s, not %s spread over its last quarter", i+1, d, full)
		}
	}
	// Asked for again by a sweep: still the end. Only Try again reopens it.
	q.Want(m)
	time.Sleep(50 * time.Millisecond)
	if n, _, _ := s.calls(); n != MaxDownloadAttempts {
		t.Fatalf("a repeated want reopened an ended download: %d calls", n)
	}
	s.mu.Lock()
	s.steps = append(s.steps, serve(data, 0))
	s.mu.Unlock()
	q.Retry(m.PluginID)
	waitFor(t, "ready after Try again", func() bool { return len(rl.list()) == 1 })
}

func TestASlowLinkThatBringsBytesIsNeverFailed(t *testing.T) {
	decl, data, st := storeModel(t, 4096)
	// Eight cut transfers in a row, each bringing 500 bytes: more failures
	// than the rule allows, every one of them progress.
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){serve(data, 500)}}
	w := &waits{}
	q, rl := ruleAcquirer(t, s, w, st)
	q.Want(engineMaterial(st, decl))
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
	if n, _, _ := s.calls(); n != 9 {
		t.Fatalf("calls %d", n)
	}
}

func TestADiskWithNoRoomIsTheEnd(t *testing.T) {
	decl, _, st := storeModel(t, 4096)
	full := &fs.PathError{Op: "write", Path: "weights.bin.partial", Err: syscall.ENOSPC}
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){answer(full)}}
	w := &waits{}
	q, _ := ruleAcquirer(t, s, w, st)
	q.Want(engineMaterial(st, decl))
	stop := stopped(t, q, "org.example.engine")
	if n, _, _ := s.calls(); n != 1 || w.count() != 0 || !strings.Contains(stop.Remedy, "free space") {
		t.Fatalf("a full disk was asked again: %d calls, %d waits, %+v", n, w.count(), stop)
	}
	// Even said by a fetcher as the network's, a full disk is not.
	if transientDownload(&NetworkError{Cause: full}) {
		t.Fatal("a full disk passed for a moment's trouble")
	}
}
