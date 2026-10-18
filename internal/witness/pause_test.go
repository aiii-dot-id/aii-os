package witness

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// passLog is what one anchor pass did, in order.
type passLog struct {
	mu     sync.Mutex
	events []string
	// keyHeld: the witness key was persisted beside the ledger when the
	// pause began — the pass's other network round trips were done.
	keyHeld bool
	dir     string
}

func (p *passLog) add(e string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

func (p *passLog) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func (p *passLog) PauseAppends() func() {
	p.add("pause")
	_, err := os.Stat(WitnessKeysDir(p.dir))
	p.mu.Lock()
	p.keyHeld = err == nil
	p.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { p.add("release") }) }
}

type loggedMinter struct {
	inner EventMinter
	log   *passLog
}

func (m loggedMinter) MintWitnessed(r WitnessReceipt, keyID string) (*ledger.Event, error) {
	m.log.add("mint")
	return m.inner.MintWitnessed(r, keyID)
}

func TestThePauseCoversTheBookmarkAndTheMintAndIsAlwaysReleased(t *testing.T) {
	t.Run("a pass that mints", func(t *testing.T) {
		p := synthPlatform(t)
		fw := newFakeWitness(t)
		fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
		lg, kp := testLedger(t, 3)
		log := &passLog{dir: filepath.Dir(lg.Path())}
		fw.slowly(func() { log.add("bookmark") })
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, loggedMinter{testMinter{lg, kp}, log}, 1, p.writeEnv(t))
		a.SetAppendPause(log)
		a.SetSealer(lg)
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatal(err)
		}
		if want := []string{"pause", "bookmark", "mint", "release"}; !reflect.DeepEqual(log.got(), want) {
			t.Fatalf("the pass did %v, want %v", log.got(), want)
		}
		if !log.keyHeld {
			t.Fatal("THE PAUSE BEGAN BEFORE THE PASS'S OTHER ROUND TRIPS: the witness key was not yet persisted")
		}
		if lg.SealedSeq() != 4 {
			t.Fatalf("the head on time did not seal: sealed through %d", lg.SealedSeq())
		}
	})
	t.Run("a mint that does not land", func(t *testing.T) {
		fw := newFakeWitness(t)
		lg, kp := testLedger(t, 3)
		log := &passLog{dir: filepath.Dir(lg.Path())}
		fw.slowly(func() { log.add("bookmark") })
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, loggedMinter{failingMinter{}, log}, 1, "")
		a.SetAppendPause(log)
		if err := a.CheckAndAnchor(); err == nil {
			t.Fatal("fixture: the mint landed")
		}
		if want := []string{"pause", "bookmark", "mint", "release"}; !reflect.DeepEqual(log.got(), want) {
			t.Fatalf("the pass did %v, want %v", log.got(), want)
		}
	})
	t.Run("a witness that refuses", func(t *testing.T) {
		fw := newFakeWitness(t)
		var bookmarks atomic.Int64
		srv := conflictServer(t, fw, conflictMsgRollbackFork, &bookmarks)
		lg, kp := testLedger(t, 3)
		log := &passLog{dir: filepath.Dir(lg.Path())}
		a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, loggedMinter{testMinter{lg, kp}, log}, 1, "")
		a.SetAppendPause(log)
		if err := a.CheckAndAnchor(); err == nil || bookmarks.Load() != 1 {
			t.Fatalf("fixture: want one refused bookmark, got %d (%v)", bookmarks.Load(), err)
		}
		if want := []string{"pause", "release"}; !reflect.DeepEqual(log.got(), want) {
			t.Fatalf("the pass did %v, want %v", log.got(), want)
		}
	})
}
