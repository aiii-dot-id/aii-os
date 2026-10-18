//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"bytes"
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// held is a fetcher that holds every download until open closes, and says
// when one began and when one was given up.
type held struct {
	began, gaveUp chan string
	open          chan struct{}
}

func holdFetches(f *fetches) *held {
	h := &held{began: make(chan string, 16), gaveUp: make(chan string, 16), open: make(chan struct{})}
	f.before = func(ctx context.Context, url string, offset int64) error {
		select {
		case h.began <- url:
		default:
		}
		select {
		case <-h.open:
			return nil
		case <-ctx.Done():
			select {
			case h.gaveUp <- url:
			default:
			}
			return ctx.Err()
		}
	}
	return h
}

func (h *held) await(t *testing.T, ch chan string, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("never: %s", what)
	}
}

// none says nothing arrives on ch for a moment.
func (h *held) none(t *testing.T, ch chan string, what string) {
	t.Helper()
	select {
	case url := <-ch:
		t.Fatalf("%s (%s)", what, url)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestReleasingTheLastReferenceStopsItsDownload(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	h := holdFetches(f)
	r := newStoreRig(t, "", f, Options{})
	m := f.model("long.bin", bytes.Repeat([]byte("a long download "), 64))
	work := viewWork(m)
	l := r.link(newKey(t), slot)
	need := NeedParams{Plugin: "org.example.long", Works: []pluginhost.StoreWork{work}}
	if res, err := l.need(need); err != nil || res.State != NeedPending {
		t.Fatalf("need: %+v, %v", res, err)
	}
	h.await(t, h.began, "the download began")
	if _, err := l.ask(MsgRelease, ReleaseParams{Refs: []LinkRef{{Plugin: "org.example.long", Entry: work.Entry}}}); err != nil {
		t.Fatal(err)
	}
	h.await(t, h.gaveUp, "the download of an entry no slot refers to any longer was stopped")
	if st := r.diskState(); len(st.Pending) != 0 {
		t.Fatalf("an entry no slot refers to is still pending on disk: %+v", st.Pending)
	}
	// Asked for again, it is brought afresh: the stopped job is no answer.
	close(h.open)
	l.needUntil(need, NeedPresent, 10*time.Second)
}

func TestADownloadAnotherSlotStillRefersToGoesOnAfterARelease(t *testing.T) {
	root := t.TempDir()
	a, b := slotIn(t, root, "identity-a"), slotIn(t, root, "identity-b")
	f := newFetches()
	h := holdFetches(f)
	r := newStoreRig(t, "", f, Options{})
	m := f.model("shared.bin", bytes.Repeat([]byte("one download for two "), 64))
	work := viewWork(m)
	la, lb := r.link(newKey(t), a), r.link(newKey(t), b)
	need := NeedParams{Plugin: "org.example.shared", Works: []pluginhost.StoreWork{work}}
	for _, l := range []*rawLink{la, lb} {
		if res, err := l.need(need); err != nil || res.State != NeedPending {
			t.Fatalf("need: %+v, %v", res, err)
		}
	}
	h.await(t, h.began, "the download began")
	if _, err := la.ask(MsgRelease, ReleaseParams{Refs: []LinkRef{{Plugin: "org.example.shared", Entry: work.Entry}}}); err != nil {
		t.Fatal(err)
	}
	h.none(t, h.gaveUp, "a download another slot still refers to was stopped")
	if st := r.diskState(); len(st.Pending) != 1 || st.Pending[0].Entry != work.Entry {
		t.Fatalf("a download another slot still refers to is no longer pending: %+v", st.Pending)
	}
	close(h.open)
	lb.needUntil(need, NeedPresent, 10*time.Second)
	if n := f.count(m.URL); n != 1 {
		t.Fatalf("the shared download was fetched %d times, want once", n)
	}
}

func TestForgettingTheLastReferringSlotStopsItsDownload(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	h := holdFetches(f)
	r := newStoreRig(t, "", f, Options{})
	work := viewWork(f.model("forgotten.bin", bytes.Repeat([]byte("never wanted again "), 64)))
	l := r.link(newKey(t), slot)
	if res, err := l.need(NeedParams{Plugin: "org.example.forgotten", Works: []pluginhost.StoreWork{work}}); err != nil || res.State != NeedPending {
		t.Fatalf("need: %+v, %v", res, err)
	}
	h.await(t, h.began, "the download began")
	if _, err := l.ask(MsgForget, nil); err != nil {
		t.Fatalf("forget: %v", err)
	}
	h.await(t, h.gaveUp, "the download of a forgotten slot's entry was stopped")
	if st := r.diskState(); len(st.Pending) != 0 {
		t.Fatalf("a forgotten slot's entry is still pending on disk: %+v", st.Pending)
	}
}

func TestASlotFoundGoneTakesItsDownloadWithItsReferences(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	h := holdFetches(f)
	r := newStoreRig(t, "", f, Options{})
	work := viewWork(f.model("orphan.bin", bytes.Repeat([]byte("its slot was removed "), 64)))
	l := r.link(newKey(t), slot)
	if res, err := l.need(NeedParams{Plugin: "org.example.orphan", Works: []pluginhost.StoreWork{work}}); err != nil || res.State != NeedPending {
		t.Fatalf("need: %+v, %v", res, err)
	}
	h.await(t, h.began, "the download began")
	if err := os.RemoveAll(slot); err != nil {
		t.Fatal(err)
	}
	r.collect()
	r.clock.advance(storeGrace + time.Minute)
	r.collect()
	h.await(t, h.gaveUp, "the download of a slot found gone was stopped with its references")
	if st := r.diskState(); len(st.Pending) != 0 || len(st.Refs) != 0 {
		t.Fatalf("a slot found gone still holds a download: pending %+v, refs %+v", st.Pending, st.Refs)
	}
}

// A LINK THAT CLOSES LETS GO OF NOTHING: its slot's references are durable,
// and the download they asked for goes on to be published.
func TestALinkThatClosesKeepsItsReferencesAndItsDownload(t *testing.T) {
	slot := slotIn(t, t.TempDir(), "identity-a")
	f := newFetches()
	h := holdFetches(f)
	r := newStoreRig(t, "", f, Options{})
	work := viewWork(f.model("kept.bin", bytes.Repeat([]byte("asked for, then the link went "), 64)))
	l := r.link(newKey(t), slot)
	if res, err := l.need(NeedParams{Plugin: "org.example.kept", Works: []pluginhost.StoreWork{work}}); err != nil || res.State != NeedPending {
		t.Fatalf("need: %+v, %v", res, err)
	}
	h.await(t, h.began, "the download began")
	l.conn.Close()
	waitFor(t, "the service to see the link go", func() bool {
		r.srv.mu.Lock()
		defer r.srv.mu.Unlock()
		return len(r.srv.links) == 0
	})
	h.none(t, h.gaveUp, "a closed link stopped its slot's download")
	if !slices.Contains(r.diskState().Refs, Ref{Slot: slot, Plugin: "org.example.kept", Entry: work.Entry}) {
		t.Fatalf("a closed link let go of its slot's reference: %+v", r.diskState().Refs)
	}
	close(h.open)
	waitFor(t, "the download to be published", func() bool { return r.published(work.Entry) })
}
