package tools

import (
	"errors"
	"testing"
)

// .
// .
// .
func TestSupersedeOriginRedirectsAddsAndRemoves(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	if err := r.RegisterDynamic(&namedTool{n: "pl_a_one"}, "org.a"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterDynamic(&namedTool{n: "pl_a_two"}, "org.a"); err != nil {
		t.Fatal(err)
	}
	bKeep := &namedTool{n: "pl_b_keep"}
	if err := r.RegisterDynamic(bKeep, "org.b"); err != nil {
		t.Fatal(err)
	}

	one2 := &namedTool{n: "pl_a_one"}
	three := &namedTool{n: "pl_a_three"}
	if err := r.SupersedeOrigin("org.a", []Tool{one2, three}, nil); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if got, ok := r.Get("pl_a_one"); !ok || got != Tool(one2) {
		t.Fatalf("the kept name must redirect to the new release's tool")
	}
	if _, ok := r.Get("pl_a_two"); ok {
		t.Fatalf("a name the new release dropped must be gone")
	}
	if _, ok := r.Get("pl_a_three"); !ok {
		t.Fatalf("a name the new release added must be present")
	}
	if got, _ := r.Get("pl_b_keep"); got != Tool(bKeep) {
		t.Fatalf("another origin's tool is untouched by a supersede")
	}
}

// .
// .
// .
func TestSupersedeOriginRefusesCrossOrigin(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	aOne := &namedTool{n: "pl_a_one"}
	bTwo := &namedTool{n: "pl_b_two"}
	_ = r.RegisterDynamic(aOne, "org.a")
	_ = r.RegisterDynamic(bTwo, "org.b")
	if err := r.SupersedeOrigin("org.a", []Tool{&namedTool{n: "pl_a_one"}, &namedTool{n: "pl_b_two"}}, nil); err == nil {
		t.Fatal("a supersede across origins must refuse")
	}
	if got, _ := r.Get("pl_a_one"); got != Tool(aOne) {
		t.Fatal("A's own tool stays after a refused supersede")
	}
	if got, _ := r.Get("pl_b_two"); got != Tool(bTwo) {
		t.Fatal("B's tool is untouched by A's refused supersede")
	}
}

// .
// .
// .
// .
// .
func TestAGuardedSupersedeRefusedLeavesEveryRouteWhereItWas(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	serving := &namedTool{n: "pl_a_one"}
	dropped := &namedTool{n: "pl_a_two"}
	_ = r.RegisterDynamic(serving, "org.a")
	_ = r.RegisterDynamic(dropped, "org.a")
	asked := 0
	err := r.SupersedeOriginIf("org.a", []Tool{&namedTool{n: "pl_a_one"}, &namedTool{n: "pl_a_three"}}, nil, func() bool {
		asked++
		// .
		// .
		if r.regMu.TryLock() {
			r.regMu.Unlock()
			t.Error("the guard was asked outside the registry's lock")
		}
		return false
	})
	if !errors.Is(err, ErrSupersedeRefused) || asked != 1 {
		t.Fatalf("err=%v asked=%d", err, asked)
	}
	if got, _ := r.Get("pl_a_one"); got != Tool(serving) {
		t.Error("a refused supersede redirected a name")
	}
	if got, _ := r.Get("pl_a_two"); got != Tool(dropped) {
		t.Error("a refused supersede removed a name")
	}
	if _, ok := r.Get("pl_a_three"); ok {
		t.Error("a refused supersede added a name")
	}
	// .
	next := &namedTool{n: "pl_a_one"}
	if err := r.SupersedeOriginIf("org.a", []Tool{next}, nil, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get("pl_a_one"); got != Tool(next) {
		t.Error("an allowed supersede did not redirect")
	}
	if _, ok := r.Get("pl_a_two"); ok {
		t.Error("an allowed supersede kept a dropped name")
	}
}
