package broker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// listPage lists one page and returns its names and truncated flag.
func listPage(t *testing.T, b *Binding, path, args string) ([]string, bool) {
	t.Helper()
	m := dispatch(t, b, fsParams("fs.list", "private", path, args))
	wantResult(t, m, statusSucceeded, "")
	var or struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(m["operation_result"], &or); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(or.Entries))
	for _, e := range or.Entries {
		names = append(names, e.Name)
	}
	return names, or.Truncated
}

// A directory larger than one reply is read whole, page by page: each page is
// the names strictly after the last name of the page before, in the reply's own
// order, and the pages together are every entry exactly once (the operator's
// ruling of 2026-11-10). Before it, entry 1,025 and on could not be seen, and a
// plugin that walks a tree refused the whole directory.
func TestAListingPagesPastItsCeilingByName(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{})
	b := h.Bind("p", packagefmt.TierT0, []string{"fs.private"})
	b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "big/.seed", `{"data":"x"}`)), statusSucceeded, "")
	all := []string{".seed"}
	for i := 0; i < MaxListEntries+300; i++ {
		// Mixed case and a leading dot and dash: the order is the bytes', as the reply sorts.
		name := fmt.Sprintf("f%05d", i)
		switch i % 7 {
		case 1:
			name = "F" + name
		case 2:
			name = "-" + name
		case 3:
			name = "." + name
		}
		if err := os.WriteFile(filepath.Join(b.privateDir, "big", name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		all = append(all, name)
	}
	sort.Strings(all)

	var got []string
	after, pages := "", 0
	for {
		args := ""
		if pages > 0 {
			raw, _ := json.Marshal(map[string]string{"after": after})
			args = string(raw)
		}
		names, truncated := listPage(t, b, "big", args)
		pages++
		if len(names) > MaxListEntries {
			t.Fatalf("page %d holds %d entries, over the ceiling of %d", pages, len(names), MaxListEntries)
		}
		if pages > 1 && len(names) > 0 && names[0] <= after {
			t.Fatalf("page %d starts at %q, not after %q", pages, names[0], after)
		}
		got = append(got, names...)
		if !truncated {
			break
		}
		if len(names) == 0 || pages > 4 {
			t.Fatalf("a truncated page must advance: page %d, %d names", pages, len(names))
		}
		after = names[len(names)-1]
	}
	if pages != 2 || strings.Join(got, "\n") != strings.Join(all, "\n") {
		t.Fatalf("pages=%d, %d of %d names, or not every entry once in order", pages, len(got), len(all))
	}

	// After a name that is not there: the names after it. After the last: none, not truncated.
	names, truncated := listPage(t, b, "big", `{"after":"f00500"}`)
	if truncated || len(names) == 0 || names[0] <= "f00500" {
		t.Fatalf("after an absent name: %d names from %q, truncated %v", len(names), names[0], truncated)
	}
	names, truncated = listPage(t, b, "big", fmt.Sprintf(`{"after":%q}`, all[len(all)-1]))
	if len(names) != 0 || truncated {
		t.Fatalf("after the last name: %v, truncated %v", names, truncated)
	}
	// An empty after is the first page, as no argument is.
	first, _ := listPage(t, b, "big", `{"after":""}`)
	plain, _ := listPage(t, b, "big", "")
	if strings.Join(first, "\n") != strings.Join(plain, "\n") {
		t.Fatal(`{"after":""} must be the first page`)
	}
	// Arguments that are null as a whole are no arguments, as they were: the first page.
	none, _ := listPage(t, b, "big", "null")
	if strings.Join(none, "\n") != strings.Join(plain, "\n") {
		t.Fatal("arguments that are null as a whole must be the first page")
	}
}

// What fs.list does not understand it refuses by name, as fs.read does, so a
// plugin never reads a first page it took for a later one.
func TestAListingRefusesAnArgumentItDoesNotKnowOrAnAfterThatIsNotAName(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{})
	b := h.Bind("p", packagefmt.TierT0, []string{"fs.private"})
	b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "d/a", `{"data":"x"}`)), statusSucceeded, "")
	for name, c := range map[string]struct{ args, reason string }{
		"unknown argument":   {`{"offset":3}`, reasonNetUnknownArgument},
		"after not a string": {`{"after":5}`, reasonArgumentInvalid},
		// A null is not a string either. Decoded into a string it changes nothing and raises no
		// error, so it once answered the first page as if that were what was asked for.
		"after null":       {`{"after":null}`, reasonArgumentInvalid},
		"after a path":     {`{"after":"a/b"}`, reasonArgumentInvalid},
		"after with a NUL": {`{"after":"a\u0000b"}`, reasonArgumentInvalid},
		"after too long":   {fmt.Sprintf(`{"after":%q}`, strings.Repeat("n", MaxFilePathBytes+1)), reasonArgumentInvalid},
		"arguments a list": {`["after"]`, reasonArgumentInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			wantResult(t, dispatch(t, b, fsParams("fs.list", "private", "d", c.args)), statusDenied, c.reason)
		})
	}
}
