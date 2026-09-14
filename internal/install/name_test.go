package install

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
func TestNameReadsTheGenesisEvent(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir,
		`{"seq":1,"type":"ring0.genesis","ring":0,"payload":{"name":"Willow","model_id":"zai-org/glm-5.2"}}`,
		`{"seq":2,"type":"ring3.note","ring":3,"payload":{"name":"NotTheIdentityName"}}`,
	)
	if got := Name(dir); got != "Willow" {
		t.Fatalf("Name = %q, want %q", got, "Willow")
	}
}

// .
// .
func TestNameIgnoresLaterNameFields(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir,
		`{"seq":1,"type":"ring3.note","ring":3,"payload":{"name":"Impostor"}}`,
		`{"seq":2,"type":"ring0.genesis","ring":0,"payload":{"name":"Reed"}}`,
	)
	if got := Name(dir); got != "Reed" {
		t.Fatalf("Name = %q, want %q — a non-genesis event named the identity", got, "Reed")
	}
}

// .
// .
func TestNameOfAnUnbornSlotIsEmpty(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := Name(dir); got != "" {
		t.Fatalf("Name of an unborn slot = %q, want empty", got)
	}
}

// .
// .
// .
func TestNameSurvivesALongLedgerLine(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	pad := make([]byte, 200*1024)
	for i := range pad {
		pad[i] = 'A'
	}
	writeLedger(t, dir,
		`{"seq":1,"type":"ring0.genesis","ring":0,"payload":{"name":"Willow","sig":"`+string(pad)+`"}}`,
	)
	if got := Name(dir); got != "Willow" {
		t.Fatalf("Name = %q, want %q — a long signed line defeated the scanner", got, "Willow")
	}
}

// .
// .
func TestRefreshNamesRebuildsFromTheLedger(t *testing.T) {
	root := t.TempDir()
	d0, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, 1); err != nil {
		t.Fatal(err)
	}
	writeLedger(t, d0, `{"seq":1,"type":"ring0.genesis","ring":0,"payload":{"name":"Reed"}}`)

	// .
	// .
	if err := LinkName(root, "Reed", 1); err != nil {
		t.Fatal(err)
	}

	names := RefreshNames(root)
	if names[SlotName(0)] != "Reed" {
		t.Fatalf("RefreshNames = %v, want slot 0 -> Reed", names)
	}
	got, err := os.Readlink(filepath.Join(root, ByName, "Reed"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("..", SlotName(0)); got != want {
		t.Fatalf("stale link survived refresh: %q, want %q", got, want)
	}
}

func writeLedger(t *testing.T, slotDir string, lines ...string) {
	t.Helper()
	path := filepath.Join(slotDir, ledgerRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}
