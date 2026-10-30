package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// keptRuntime answers Recheck from the evidence its caller kept, and
// refuses to verify a package whose file is gone or whose bytes it is told
// are bad.
type keptRuntime struct {
	*fakeRuntime
	mu      sync.Mutex
	gone    map[string]bool // a package path whose file holds nothing now
	bad     map[string]bool // a package path whose bytes refuse at verification
	revoked map[string]bool // a package whose kept evidence is revoked now
}

func newKeptRuntime() *keptRuntime {
	return &keptRuntime{fakeRuntime: newFakeRuntime(), gone: map[string]bool{}, bad: map[string]bool{}, revoked: map[string]bool{}}
}

func (r *keptRuntime) set(m map[string]bool, pkg string) {
	r.mu.Lock()
	m[pkg] = true
	r.mu.Unlock()
}

func (r *keptRuntime) Verify(ctx context.Context, pkg string) (Evidence, error) {
	r.mu.Lock()
	gone, bad := r.gone[pkg], r.bad[pkg]
	r.mu.Unlock()
	switch {
	case gone:
		r.note("verify:" + pkg)
		return Evidence{}, errors.New("open " + pkg + ": no such file")
	case bad:
		r.note("verify:" + pkg)
		return Evidence{}, errors.New("the publisher's signature does not verify")
	}
	return r.fakeRuntime.Verify(ctx, pkg)
}

func (r *keptRuntime) Recheck(_ context.Context, ev Evidence) error {
	r.note("recheck:" + ev.Package)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.revoked[ev.Package] {
		return errors.New("the publisher certificate is revoked")
	}
	return nil
}

func (r *keptRuntime) called(prefix string) int {
	n := 0
	for _, c := range r.seen() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// A RELEASE THAT SERVES IS ASKED AGAIN WHEN TRUST OR POLICY MOVES, whatever
// is desired beside it. A newer release placed and refused used to leave the
// one it would replace serving with nobody asking about it: its signer
// revoked, it went on serving. Now the trust generation moving re-asks what
// serves, from the evidence kept when it began to serve, and a revoked one
// stops.
func TestAServingReleaseIsAskedAgainWhenTheUpdateBesideItWasRefused(t *testing.T) {
	r := newKeptRuntime()
	f := newFacility(t, r)
	const id = "id.example.p"
	one := []Observed{{ID: id, Package: "one", Hash: "one"}}
	two := []Observed{{ID: id, Package: "two", Hash: "two"}}
	f.Observe(one, Policy{Revision: 1})
	until(t, "one serving", func() bool { return viewOf(f, id).State == StateActive && sawCall(r.fakeRuntime, "start:one") })

	r.set(r.bad, "two")
	f.Observe(two, Policy{Revision: 1})
	until(t, "the update refused", func() bool { return sawCall(r.fakeRuntime, "verify:two") && viewOf(f, id).Refusal != nil })
	if sawCall(r.fakeRuntime, "stop:one") {
		t.Fatal("a refused update stopped the release it would have replaced")
	}

	// Trust moves: the serving release is asked about, and it still holds.
	f.Observe(two, Policy{Revision: 1, TrustGen: 1})
	until(t, "what serves asked again", func() bool { return r.called("recheck:one") >= 1 })
	time.Sleep(50 * time.Millisecond)
	if sawCall(r.fakeRuntime, "stop:one") {
		t.Fatal("a release whose evidence still holds was stopped")
	}

	// Its signer revoked: asked again, and it stops.
	r.set(r.revoked, "one")
	f.Observe(two, Policy{Revision: 1, TrustGen: 2})
	until(t, "the revoked release stopped", func() bool { return sawCall(r.fakeRuntime, "stop:one") })
}

// A SERVING RELEASE WHOSE FILE IS GONE IS ASKED ABOUT WHAT WAS KEPT, not
// stopped for want of a file and not answered for by other bytes: an update
// replacing a file is no cause to stop a working plugin, and a revoked one
// stops all the same.
func TestAServingReleaseWhoseFileIsGoneIsAskedAboutItsKeptEvidence(t *testing.T) {
	r := newKeptRuntime()
	f := newFacility(t, r)
	const id = "id.example.p"
	one := []Observed{{ID: id, Package: "one", Hash: "one"}}
	f.Observe(one, Policy{Revision: 1})
	until(t, "one serving", func() bool { return viewOf(f, id).State == StateActive && sawCall(r.fakeRuntime, "start:one") })
	verified := r.called("verify:one")

	r.set(r.gone, "one")
	f.Observe(one, Policy{Revision: 2})
	until(t, "what serves asked again", func() bool { return r.called("recheck:one") >= 1 })
	time.Sleep(50 * time.Millisecond)
	if sawCall(r.fakeRuntime, "stop:one") || viewOf(f, id).State != StateActive {
		t.Fatalf("a serving release was stopped because its file is gone: %v", r.seen())
	}
	if r.called("verify:one") != verified {
		t.Fatalf("the re-ask read the package's path, not the evidence kept: %v", r.seen())
	}

	r.set(r.revoked, "one")
	f.Observe(one, Policy{Revision: 3})
	until(t, "the revoked release stopped", func() bool { return sawCall(r.fakeRuntime, "stop:one") })
}
