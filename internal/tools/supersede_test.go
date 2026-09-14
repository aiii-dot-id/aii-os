package tools

import "testing"

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
