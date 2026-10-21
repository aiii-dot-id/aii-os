package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE OPERATOR'S REMOVAL OF WHAT A PLUGIN STORED, as the Plugins page's
// act reaches it. Nothing removed a plugin's memories or keys before, at
// uninstall or anywhere. What each plugin holds is listed whether or not
// its package is installed; the act takes one plugin's record and says
// what it took, in the sentence the page then shows; an uninstalled
// plugin's record is still there to remove; another plugin's is not
// touched.
//
// Installed is the page's one definition of it, the verified active set
// the catalog rows use, with their pending mark: a directory named for a
// plugin's id says nothing (here "org.example.gone" has one and is not
// installed, and "org.example.here" runs from a directory of another
// name).
func TestTheOperatorRemovesWhatAPluginStoredAndIsToldWhatWent(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"org.example.gone", "notes-dev"} {
		if err := os.MkdirAll(filepath.Join(dir, "plugins", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	st := newAdmissionStore(t, dir)
	remember := func(plugin, id, old string) {
		t.Helper()
		if err := st.PluginMemoryAdd(store.PluginScope{PluginID: plugin}, store.PluginMemory{ID: id, Text: "memory " + id}, 0, old); err != nil {
			t.Fatal(err)
		}
	}
	remember("org.example.gone", "g1", "")
	remember("org.example.gone", "g2", "g1")
	if err := st.PluginKVPut(store.PluginScope{PluginID: "org.example.gone"}, "cursor", "42", false, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	remember("org.example.here", "h1", "")
	remember("org.example.held", "p1", "")
	a := &App{store: st}
	setPlugins(a, []*pluginhost.ActivePlugin{{ID: "org.example.here"}})
	pending := []dashboard.PluginPendingView{{ID: "org.example.held", Phase: "refused"}}

	views, failure := a.pluginStoredViews()
	a.markStoredInstalled(views, pending)
	want := []dashboard.PluginStoredView{
		{ID: "org.example.gone", Memories: 2, Replaced: 1, Keys: 1},
		{ID: "org.example.held", Pending: "refused", Memories: 1},
		{ID: "org.example.here", Installed: true, Memories: 1},
	}
	if failure != "" || len(views) != 3 || views[0] != want[0] || views[1] != want[1] || views[2] != want[2] {
		t.Fatalf("what the page is told the plugins hold = %+v (%q), want %+v", views, failure, want)
	}

	// THE OPERATOR READS IT BEFORE ANYTHING IS ASKED OF THEM: the
	// same hooks that carry the removal carry the read, every row whole.
	h := &dashboard.WSHandler{}
	a.wirePluginHooks(h)
	memories, err := h.PluginStored(dashboard.PluginStoredRequest{Plugin: "org.example.gone", Kind: "memories"})
	if err != nil || memories.Total != 2 || memories.Next != 0 || len(memories.Memories) != 2 {
		t.Fatalf("the operator's read of its memories: %+v %v", memories, err)
	}
	if newest, oldest := memories.Memories[0], memories.Memories[1]; newest.ID != "g2" || newest.Text != "memory g2" || newest.ReplacedBy != "" || newest.StoredAt == "" ||
		oldest.ID != "g1" || oldest.Text != "memory g1" || oldest.ReplacedBy != "g2" || oldest.ReplacedAt == "" {
		t.Fatalf("the memories are read newest first, each whole, the replaced one named with what replaced it: %+v", memories.Memories)
	}
	keys, err := h.PluginStored(dashboard.PluginStoredRequest{Plugin: "org.example.gone", Kind: "keys"})
	if err != nil || keys.Total != 1 || len(keys.Keys) != 1 || keys.Keys[0].Key != "cursor" || keys.Keys[0].Value != "42" || keys.Keys[0].StoredAt == "" {
		t.Fatalf("the operator's read of its keys: %+v %v", keys, err)
	}
	if _, err := h.PluginStored(dashboard.PluginStoredRequest{Plugin: "org.example.gone", Kind: "secrets"}); err == nil {
		t.Fatal("a kind that is neither memories nor keys was answered")
	}
	if a.storedRemoved.Load() != nil {
		t.Fatal("a removal is reported before any was made")
	}

	if err := h.PluginAct(dashboard.PluginAction{Action: "remove_stored", ID: "org.example.gone"}); err != nil {
		t.Fatalf("the page's act: %v", err)
	}
	said := a.storedRemoved.Load()
	const sentence = "Removed what org.example.gone stored in this identity's database: 2 memories (1 of them replaced) and 1 key."
	if said == nil || *said != sentence {
		t.Fatalf("what the operator is told went = %v, want %q", said, sentence)
	}
	views, failure = a.pluginStoredViews()
	a.markStoredInstalled(views, pending)
	if failure != "" || len(views) != 2 || views[0] != want[1] || views[1] != want[2] {
		t.Fatalf("after the removal the page is told %+v (%q), want the other plugins' records alone", views, failure)
	}

	// A plugin that holds nothing is answered so, and nothing is refused.
	if err := a.RemovePluginStored("org.example.gone"); err != nil {
		t.Fatal(err)
	}
	if said := a.storedRemoved.Load(); said == nil || !strings.Contains(*said, "had nothing stored") {
		t.Fatalf("a removal that found nothing says %v", said)
	}
	if err := a.RemovePluginStored(""); err == nil {
		t.Fatal("a removal that names no plugin was accepted")
	}
	if err := (&App{}).RemovePluginStored("org.example.here"); err == nil {
		t.Fatal("a removal with no database was accepted")
	}
}

// AN UNINSTALL REMOVES A PACKAGE AND NOTHING THE PLUGIN STORED. What it
// stored is the operator's to remove or keep, by an act of its own.
func TestAnUninstallLeavesWhatThePluginStored(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plugins", "org.example.rm"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	st := newAdmissionStore(t, dir)
	if err := st.PluginMemoryAdd(store.PluginScope{PluginID: "org.example.rm"}, store.PluginMemory{ID: "m1", Text: "kept across the uninstall"}, 0, ""); err != nil {
		t.Fatal(err)
	}
	a := &App{store: st}
	if err := a.UninstallPlugin("org.example.rm"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	views, failure := a.pluginStoredViews()
	a.markStoredInstalled(views, nil)
	if failure != "" || len(views) != 1 || views[0] != (dashboard.PluginStoredView{ID: "org.example.rm", Memories: 1}) {
		t.Fatalf("after the uninstall the page is told %+v (%q), want the record kept and the plugin not installed", views, failure)
	}
}
