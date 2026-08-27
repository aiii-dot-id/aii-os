package dashboard

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Every element the frame ships must be reachable from a module, or say
// in the markup why it is not.
//
// verbregistry.go cured this disease once, and its comment names three
// live incidents in one day: "work was advertised with an empty schema;
// work was advertised but unroutable; commit and tools were advertised
// and callable by NOTHING. Same disease three times: scattered surface."
// The cure was structural — one declaration carrying name, schema AND
// handler, with init() refusing an incomplete entry — so a half-declared
// verb cannot boot.
//
// The frame had no such guarantee. Nothing forced a control to have a
// handler, and an audit found exactly that: a microphone button whose id
// appears in index.html and in no module at all. A feature no path
// reaches has the same net effect as one that was never built.
//
// This is the frame's version of that init() check. An element is
// reachable if a module names its id — literally, or by the prefix
// concatenation the module actually uses ('view-' + v). Anything else
// must carry data-inert with a REASON, so a deliberate placeholder is
// distinguishable from an accident, and the justification travels with
// the element instead of living in a list somewhere else.

var (
	idAttr    = regexp.MustCompile(`\bid="([a-zA-Z0-9_-]+)"`)
	inertAttr = regexp.MustCompile(`data-inert="([^"]*)"`)
)

func TestEveryFrameElementIsReachableOrDeclaredInert(t *testing.T) {
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)

	entries, err := staticFS.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	var modules strings.Builder
	read := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		b, err := staticFS.ReadFile("static/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		modules.Write(b)
		read++
	}
	views, err := staticFS.ReadDir("static/views")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range views {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		b, err := staticFS.ReadFile("static/views/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		modules.Write(b)
		read++
	}
	// Guard: with no modules read, every id below is "unreachable" and
	// the failure would be this test, not the frame.
	if read < 10 {
		t.Fatalf("only %d modules read — the reachability check would be measuring itself", read)
	}
	js := modules.String()

	var unreachable []string
	for _, m := range idAttr.FindAllStringSubmatch(html, -1) {
		id := m[1]
		if namedIn(js, id) || builtByPrefix(js, id) {
			continue
		}
		if reason := inertReasonFor(html, id); reason != "" {
			continue // deliberate, and it says why
		}
		unreachable = append(unreachable, id)
	}
	if len(unreachable) > 0 {
		t.Fatalf("these elements ship but no module reaches them: %s\n"+
			"Either wire them, delete them, or mark the element data-inert=\"why\" — "+
			"a control that does nothing and does not say so is machinery the "+
			"operator has to learn to discount (AGENTS.md 1.4).",
			strings.Join(unreachable, ", "))
	}
}

// namedIn matches the id as a TOKEN, not a substring. The first version
// used strings.Contains and passed while the known-unreachable "mic"
// went undetected, because "mic" is inside "dynamic" — a check that
// cannot see the instance it was written for is worse than no check,
// since it certifies the tree clean.
func namedIn(js, id string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_$-])` + regexp.QuoteMeta(id) + `([^A-Za-z0-9_$-]|$)`)
	return re.MatchString(js)
}

// builtByPrefix accepts ids the modules construct rather than spell:
// app.js toggles views with 'view-' + v, so #view-chat is reached even
// though that exact string appears nowhere.
func builtByPrefix(js, id string) bool {
	for i, r := range id {
		if r != '-' {
			continue
		}
		prefix := id[:i+1]
		for _, form := range []string{
			fmt.Sprintf("'%s' +", prefix),
			fmt.Sprintf("\"%s\" +", prefix),
			fmt.Sprintf("`%s${", prefix),
		} {
			if strings.Contains(js, form) {
				return true
			}
		}
	}
	return false
}

// inertReasonFor returns the declared reason on the element carrying id,
// or "" if it declares none. An empty data-inert is not a reason.
func inertReasonFor(html, id string) string {
	needle := `id="` + id + `"`
	at := strings.Index(html, needle)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		return ""
	}
	tag := html[start : at+end]
	m := inertAttr.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// The check must be able to fail, and an empty declaration must not
// launder an accident into a decision.
func TestInertDeclarationRequiresAReason(t *testing.T) {
	const tag = `<button id="ghost" data-inert="">x</button>`
	if got := inertReasonFor(tag, "ghost"); got != "" {
		t.Fatalf("an empty data-inert was accepted as a reason: %q", got)
	}
	const good = `<button id="ghost" data-inert="waiting on voice">x</button>`
	if got := inertReasonFor(good, "ghost"); got != "waiting on voice" {
		t.Fatalf("a declared reason was not read back: %q", got)
	}
	if builtByPrefix(`const el = $('view-' + v);`, "view-chat") != true {
		t.Fatal("a prefix-constructed id was not recognised as reachable")
	}
	if builtByPrefix(`const el = $('nothing');`, "view-chat") != false {
		t.Fatal("an unreachable id was accepted as prefix-constructed")
	}
	// The substring trap that made the first version of this check useless.
	if namedIn(`const x = dynamicThing;`, "mic") {
		t.Fatal("an id was matched inside a longer word — the check would certify an unreachable control")
	}
	if !namedIn(`const b = $('mic');`, "mic") {
		t.Fatal("a genuinely named id was not recognised")
	}
}
