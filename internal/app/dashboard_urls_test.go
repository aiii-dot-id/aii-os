package app

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestEveryURLTheDashboardAnswersAtArrivesReadyToOpen(t *testing.T) {
	const token = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	lines := accessLines([]string{"http://127.0.0.1:8181", "https://192.0.2.10:8181"}, token, "data/dashboard-token")

	if len(lines) != 4 {
		t.Fatalf("two addresses, two links, the cookie note and the token line:\n%s", strings.Join(lines, "\n"))
	}
	if want := "Open it here: http://127.0.0.1:8181/?token=" + token; lines[0] != want {
		t.Errorf("first link\n got %q\nwant %q", lines[0], want)
	}
	// .
	// .
	if want := "          or: https://192.0.2.10:8181/?token=" + token; lines[1] != want {
		t.Errorf("second link\n got %q\nwant %q", lines[1], want)
	}
	if !strings.Contains(lines[3], token) || !strings.Contains(lines[3], "data/dashboard-token") {
		t.Errorf("the token and where it is kept stay on the record: %q", lines[3])
	}
}

// .
// .
func TestAnAccessLinkKeepsOneSlashBeforeItsQuery(t *testing.T) {
	lines := accessLines([]string{"http://127.0.0.1:8181/"}, "abc", "t")
	if !strings.HasPrefix(lines[0], "Open it here: http://127.0.0.1:8181/?token=abc") {
		t.Fatalf("got %q", lines[0])
	}
}

// .
// .
// .
func TestALoopbackOnlyHostPrintsOneLink(t *testing.T) {
	lines := accessLines([]string{"http://127.0.0.1:8181", ""}, "abc", "t")
	if len(lines) != 3 {
		t.Fatalf("one link, the cookie note, the token line:\n%s", strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "or:") {
			t.Fatalf("an empty address became a link: %q", l)
		}
	}
}

// .
// .
func TestTheTokenIsPrintedEvenWithNoURL(t *testing.T) {
	lines := accessLines(nil, "abc", "t")
	if len(lines) != 1 || !strings.Contains(lines[0], "the token is abc") {
		t.Fatalf("got %#v", lines)
	}
}
