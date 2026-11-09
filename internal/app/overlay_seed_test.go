package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSeedOverlayREADME pins the seed contract: absent seeds, identity
// edits win, removal re-seeds, and a PRIOR SHIPPED version upgrades
// through the answer key. The README is the capability matrix for
// identities without the Go source — if this seed clobbers, the doc
// they trust is silently ours; if it never upgrades, the doc they
// trust describes a dashboard that no longer exists.
func TestSeedOverlayREADME(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "the-ledger-path.jsonl")
	a := &App{cfg: &Config{Identity: IdentityConfig{LedgerPath: ledgerPath}}}
	a.snapshotUILayoutPath(ledgerPath)

	// Absent → seeded, byte-equal to the embed.
	a.seedOverlayREADME()
	got, err := os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if err != nil || len(got) == 0 {
		t.Fatalf("absent must seed, got err=%v len=%d", err, len(got))
	}
	if !bytes.Equal(got, overlayREADME) {
		t.Fatalf("seed must be byte-equal to the embed, got %d bytes", len(got))
	}

	// Identity's own README → untouched.
	identity := []byte("# my dashboard, my rules")
	if err := os.WriteFile(filepath.Join(dir, "ui", "README.md"), identity, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	got, _ = os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if !bytes.Equal(got, identity) {
		t.Fatalf("identity edits must win, got %q", got)
	}

	// Removed → seeds again.
	if err := os.Remove(filepath.Join(dir, "ui", "README.md")); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	got, err = os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if err != nil || !bytes.Equal(got, overlayREADME) {
		t.Fatalf("re-seed after removal failed: err=%v", err)
	}

	// A PRIOR SHIPPED MATRIX — different bytes, in the answer key —
	// upgrades. The pre-key seeder classified exactly this state as an
	// identity edit ("theirs wins"), under a comment promising the
	// opposite, so matrix updates never reached an untouched doc.
	oldMatrix := []byte("# capability matrix, the first shipped version\n")
	if err := os.WriteFile(filepath.Join(dir, "ui", "README.md"), oldMatrix, 0o644); err != nil {
		t.Fatal(err)
	}
	overlayShippedSeeds = append(overlayShippedSeeds, docSeedKey(nil, oldMatrix))
	defer func() { overlayShippedSeeds = overlayShippedSeeds[:len(overlayShippedSeeds)-1] }()
	a.seedOverlayREADME()
	got, _ = os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if !bytes.Equal(got, overlayREADME) {
		t.Fatalf("a prior shipped matrix did not upgrade — the answer key is not consulted")
	}
}

// overlayPolicySentence is the sentence the operator approved for the
// README (the expert review's decisions sheet, row E, "Yes, these words"),
// in place of one that said overlay code "can never beacon to an external
// origin", with "font" added to its list when the page took its own faces
// (0.2.1 step 9, approved). It is held here word for word; the README wraps
// it as prose.
const overlayPolicySentence = "It cannot make a background request to any other origin: `uiCSP` sets `default-src 'none'` and allows only `'self'` on every fetch-carrying directive (script, style, img, font, connect, frame) and on `form-action`. The browser enforces that per response. That is a limit on requests, not a promise that nothing a page shows can leave it: what you write here runs with everything your operator's page can see, and is trusted with it."

func TestTheOverlayREADMESaysWhatThePolicyStopsAndAnUntouchedCopyUpgrades(t *testing.T) {
	doc := strings.Join(strings.Fields(string(overlayREADME)), " ")
	if !strings.Contains(doc, overlayPolicySentence) {
		t.Fatalf("the overlay README does not carry the approved sentence:\n%s", overlayPolicySentence)
	}
	for _, untrue := range []string{"never beacon", "form-action 'none'", "kernel-enforced per response"} {
		if strings.Contains(doc, untrue) {
			t.Errorf("the overlay README still says %q", untrue)
		}
	}

	// The edition before this one is this one with the old sentence: its
	// key must be one shipped before the current, or this test is not
	// holding the edition an identity really has.
	newText := "re-form the view and speak to this server. It cannot make a background\n" +
		"   request to any other origin: `uiCSP` sets `default-src 'none'` and\n" +
		"   allows only `'self'` on every fetch-carrying directive (script, style,\n" +
		"   img, connect, frame) and on `form-action`. The browser enforces that\n" +
		"   per response. That is a limit on requests, not a promise that nothing\n" +
		"   a page shows can leave it: what you write here runs with everything\n" +
		"   your operator's page can see, and is trusted with it.\n"
	oldText := "re-form the view and speak to this server — but it can never beacon to\n" +
		"   an external origin: `uiCSP` sets `default-src 'none'` and allows only\n" +
		"   `'self'` on every fetch-carrying directive (script, style, img,\n" +
		"   connect, frame), with `form-action 'none'`. The network boundary is\n" +
		"   the browser's, kernel-enforced per response.\n"
	// The public edition before this one is public 0.2.0's: its table and
	// its scripts section said the same in other words, the approval was in
	// Settings → Dashboard → Scripts, and it had neither the page's own
	// faces ("font"), their mark, what to do when the operator asks for a
	// change, nor the 0.2.1 section. Each edit is undone to rebuild it.
	earlier := overlayREADME
	scripts := overlayREADME[bytes.Index(overlayREADME, []byte("Restyling is free")):bytes.Index(overlayREADME, []byte("## A copy of a shipped file"))]
	for _, edit := range [][2]string{
		{"| `name` | resolves your display name (file > ledger birth name) | the sidebar and the bar update on next stats send (~turn cadence) |\n", "| `name` | resolves your display name (file > ledger birth name) | rail + home greeting update on the next stats send (~turn cadence) |\n"},
		{"| `custom.js` | serves your bytes for `/custom.js` **once your operator approved this exact file**; until then the shipped (empty) one | draft-safe reload |\n", "| `custom.js` | serves your exact approved file; otherwise the shipped empty file remains | draft-safe reload |\n"},
		{"| `app.js` | **replaces the frame's app.js**, once approved; until then the shipped one | draft-safe reload |\n", "| `app.js` | replaces the frame's file only after approval; otherwise the shipped file remains | draft-safe reload |\n"},
		{"| `views/*.js` | serves under `/views/` once approved; a shipped view's name replaces that view | draft-safe reload |\n", "| `views/*.js` | serves an approved file under `/views/`; a shipped view's name replaces that view | draft-safe reload |\n"},
		{"| `anything.html` | serves as `/anything.html` | no auto-load; link it from your custom.js |\n", "| `anything.html` | serves as `/anything.html` | no auto-load; link it from approved `custom.js` |\n"},
		{"## Your scripts run once your operator approves them\n", "## Scripts require your operator's approval\n"},
		{"   img, font, connect, frame) and on `form-action`. The browser enforces that\n", "   img, connect, frame) and on `form-action`. The browser enforces that\n"},
		{"   DOM (`#bar-mark`, `#slot-panel`", "   DOM (`#orb`, `#slot-panel`"},
		{string(scripts), "Stylesheets and pages are served as you write them. A script — any `.js`\n" +
			"file here, including `custom.js` — runs in the dashboard only after your\n" +
			"operator approves that exact file by its bytes. Until then, the shipped\n" +
			"file remains in place when yours replaces one, and a new script of yours\n" +
			"is not served. Any change to an approved script requires approval again.\n" +
			"Inline `<script>` and `onclick=` handlers in your pages do not run.\n" +
			"\n" +
			"Your operator reviews scripts in Settings → Dashboard → Scripts, where\n" +
			"each file is shown with its SHA-256 before approval. Approval requires a\n" +
			"dashboard session with an access token; a dashboard without a token does\n" +
			"not approve scripts. The UI Adapters card says a script of yours is\n" +
			"`held` while it waits. Tell your operator when you add or change a script\n" +
			"and what it does.\n" +
			"\n"},
	} {
		if !bytes.Contains(earlier, []byte(edit[0])) {
			t.Fatalf("rig: the README no longer holds %q", edit[0])
		}
		earlier = bytes.Replace(earlier, []byte(edit[0]), []byte(edit[1]), 1)
	}
	if !bytes.Contains(earlier, []byte(newText)) {
		t.Fatal("rig: the README's policy sentence is not wrapped as this test expects")
	}
	// The policy edition is this one without "font" and without the section
	// 0.2.1 added since (what the page moved, for a copy an identity keeps),
	// and its key is in the answer key; the edition before it is that one
	// with the old sentence.
	policyEdition := earlier
	const moved = "## What the 0.2.1 page moved, for a copy you keep\n"
	if i := bytes.Index(policyEdition, []byte(moved)); i >= 0 {
		j := bytes.Index(policyEdition[i+len(moved):], []byte("\n## "))
		if j < 0 {
			t.Fatal("rig: the 0.2.1 section is not followed by another, as this test expects")
		}
		policyEdition = append(append([]byte{}, policyEdition[:i]...), policyEdition[i+len(moved)+j+1:]...)
	}
	at := -1
	for i, key := range overlayShippedSeeds {
		if key == docSeedKey(nil, policyEdition) {
			at = i
		}
	}
	if at < 1 {
		t.Fatalf("rig: the policy edition rebuilt here is not in the answer key (%s)", docSeedKey(nil, policyEdition))
	}
	previous := bytes.Replace(policyEdition, []byte(newText), []byte(oldText), 1)
	if !slices.Contains(overlayShippedSeeds[:at], docSeedKey(nil, previous)) {
		t.Fatalf("rig: the edition rebuilt here is not one shipped before the policy edition (%s)", docSeedKey(nil, previous))
	}

	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "the-ledger-path.jsonl")
	a := &App{cfg: &Config{Identity: IdentityConfig{LedgerPath: ledgerPath}}}
	a.snapshotUILayoutPath(ledgerPath)
	readme := filepath.Join(dir, "ui", "README.md")
	if err := os.MkdirAll(filepath.Dir(readme), 0o755); err != nil {
		t.Fatal(err)
	}

	// Untouched previous edition: upgraded, with nothing left beside it.
	if err := os.WriteFile(readme, previous, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	if got, _ := os.ReadFile(readme); !bytes.Equal(got, overlayREADME) {
		t.Fatal("an identity's untouched copy of the previous edition was not upgraded: it still says the policy keeps overlay code from beaconing out")
	}
	if _, err := os.Stat(readme + sidecarSuffix); !os.IsNotExist(err) {
		t.Fatalf("an upgraded README left a copy beside it: %v", err)
	}

	// Edited previous edition: theirs, with this edition beside it.
	edited := append(append([]byte{}, previous...), []byte("\nMy own note on the policy.\n")...)
	if err := os.WriteFile(readme, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	if got, _ := os.ReadFile(readme); !bytes.Equal(got, edited) {
		t.Fatal("an identity's edited README was replaced")
	}
	if got, err := os.ReadFile(readme + sidecarSuffix); err != nil || !bytes.Equal(got, overlayREADME) {
		t.Fatalf("the current edition was not left beside an edited README: %v", err)
	}
}

// THE README SAYS WHAT TO DO WHEN THE OPERATOR ASKS FOR A CHANGE, AND WHERE
// THE APPROVAL IS: a person asks the identity, who makes the change.
// Settings → Dashboard is gone, and the slots are the sections'.
func TestTheOverlayREADMESaysHowToChangeThePageAndWhereToApprove(t *testing.T) {
	readme := string(overlayREADME)
	for _, want := range []string{"## When your operator asks you to change the page", "Settings → Plugins and permissions", "`custom.css`", "`views/<name>.js`", "`#slot-rail`"} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README does not say %q", want)
		}
	}
	if strings.Contains(readme, "Settings → Dashboard") {
		t.Error("the README still sends the identity to Settings → Dashboard, which is gone")
	}
}

// THE README NAMES SETTINGS' GROUPS AND WHERE THE OLD ADDRESSES GO. Two of
// the names wrap after "Settings →", so the words are read with the
// whitespace collapsed.
func TestTheOverlayREADMENamesSettingsGroupsAndWhereTheOldAddressesGo(t *testing.T) {
	doc := strings.Join(strings.Fields(string(overlayREADME)), " ")
	for _, want := range []string{"Settings → Model", "Settings → People and messages", "Settings → Access", "`#/settings/model`"} {
		if !strings.Contains(doc, want) {
			t.Errorf("the README does not say %q", want)
		}
	}
	for _, gone := range []string{"Settings → Substrate", "Settings → Messages", "All settings", "keeps its address"} {
		if strings.Contains(doc, gone) {
			t.Errorf("the README still says %q, which step 14 retired", gone)
		}
	}
}
