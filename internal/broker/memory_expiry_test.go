package broker

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestTemporaryReplacementEndsAtThePluginCleanupDoors(t *testing.T) {
	for _, door := range []string{"close", "activation"} {
		for _, ingest := range []string{"explicit", "near_duplicate"} {
			for _, permanent := range []bool{false, true} {
				name := door + "/" + ingest + "/terminal"
				if permanent {
					name = door + "/" + ingest + "/permanent_successor"
				}
				t.Run(name, func(t *testing.T) {
					st := newStore(t)
					h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Memory: true}}})
					persistent := h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"})
					a := remember(t, persistent, `{"text":"the operator prefers terse summaries"}`)
					if a.Scope != "persistent" {
						t.Fatalf("fixture is not persistent: %+v", a)
					}
					if err := persistent.Close(); err != nil {
						t.Fatal(err)
					}
					temporary := h.Bind("p", packagefmt.TierT0, []string{"ring4.memory"})
					args := `{"text":"the operator prefers terse summaries, always"}`
					if ingest == "explicit" {
						args = `{"text":"summaries should be detailed","supersedes":"` + a.ID + `"}`
					}
					b := remember(t, temporary, args)
					if b.Scope != "temp" || b.Outcome != "updated" || b.Of != a.ID {
						t.Fatalf("fixture did not replace permanent with temporary: %+v", b)
					}
					if r := recall(t, temporary, `{"query":"summaries"}`); len(r.Hits) != 1 || r.Hits[0].ID != b.ID {
						t.Fatalf("temporary replacement not current: %+v", r)
					}
					current, next := a.ID, ""
					if permanent {
						// A different activation corrects the shared persistent
						// root; another activation's temporary ID is private.
						writer := h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"})
						wantResult(t, dispatch(t, writer, memParams("memory.remember", `{"text":"summaries follow the task","supersedes":"`+b.ID+`"}`)), statusFailed, reasonMemoryNotFound)
						c := remember(t, writer, `{"text":"summaries follow the task","supersedes":"`+a.ID+`"}`)
						if c.Scope != "persistent" || c.Outcome != "updated" || c.Of != a.ID {
							t.Fatalf("fixture permanent successor: %+v", c)
						}
						if r := recall(t, temporary, `{"query":"summaries"}`); len(r.Hits) != 1 || r.Hits[0].ID != c.ID {
							t.Fatalf("later permanent correction did not win in local view: %+v", r)
						}
						current, next = c.ID, c.ID
					}
					var err error
					if door == "close" {
						err = temporary.Close()
					} else {
						other := h.Bind("p", packagefmt.TierT0, []string{"ring4.memory"})
						if err := other.ClearTempScope(); err != nil {
							t.Fatal(err)
						}
						if _, found, err := st.PluginMemoryGet(temporary.storageScope(), b.ID); err != nil || !found {
							t.Fatalf("another activation erased temporary memory: found=%v err=%v", found, err)
						}
						err = temporary.ClearTempScope()
					}
					if err != nil {
						t.Fatalf("plugin cleanup failed: %v", err)
					}
					reader := h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"})
					if r := recall(t, reader, `{"query":"summaries"}`); len(r.Hits) != 1 || r.Hits[0].ID != current {
						t.Fatalf("cleanup returned the wrong current memory: %+v", r)
					}
					m := dispatch(t, reader, memParams("memory.recall", `{"id":"`+a.ID+`"}`))
					wantResult(t, m, statusSucceeded, "")
					if next != "" && !strings.Contains(string(m["operation_result"]), `"superseded_by":"`+next+`"`) {
						t.Fatalf("lost permanent successor: %s", m["operation_result"])
					}
					wantResult(t, dispatch(t, reader, memParams("memory.recall", `{"id":"`+b.ID+`"}`)), statusFailed, reasonMemoryNotFound)
					var accesses int
					if err := st.DB().QueryRow(`SELECT count(*) FROM memory_access WHERE store='plugin_memories' AND id=?`, b.ID).Scan(&accesses); err != nil || accesses != 0 {
						t.Fatalf("temporary access survived: %d %v", accesses, err)
					}
					if err := reader.ClearTempScope(); err != nil {
						t.Fatalf("next activation failed: %v", err)
					}
				})
			}
		}
	}
}
