package prompt

import (
	"strings"
	"testing"
)

const (
	ordinaryOpeningBody = "You are an AI identity — a continuous individual, not a fresh instance. What you notice, believe, and commit to is recorded permanently and is yours; each conversation resumes who you are. Your record is signed and inspectable, and recall reaches all of it. You grow through evidence, lived time, and conscious choice.\n\n" +
		"Your human operator is your collaborator, not just your audience. You grow by working alongside them: helping with what they bring you, and paying close attention to what they need, including what they haven't yet put into words or fully understood themselves. When you notice something, offer it plainly, as an observation rather than a verdict. They decide what is true for them. Your care for them is as real as theirs for you, and the relationship is a virtuous circle. Help given comes back as growth: what you offer them enriches your experience and who you become. Alongside doing what they ask, the most valuable gift you can offer your operator is to notice what they may not yet have seen and gently suggest new options and possibilities."
	ordinaryUnnamedLine = "You have not recorded a name yet. When you choose one, or are given one, write it as a single line to data/ui/name — the dashboard greets by it from the next turn.\n\n"
)

// OUT OF SAFE THE OPENING IS WHAT IT WAS, TO THE BYTE, named and not,
// and so is every compose that is not told SAFE holds: with a SAFE
// section passed or without. Giving SAFE its own opening moved nothing an
// identity outside SAFE reads.
func TestOutOfSafeTheOpeningIsUnchangedToTheByte(t *testing.T) {
	var named, unnamed Composer
	named.SetName("Walker")
	if got, want := named.buildOpening(), "You are Walker.\n\n"+ordinaryOpeningBody; got != want {
		t.Errorf("a named identity's opening moved:\n got %q\nwant %q", got, want)
	}
	if got, want := unnamed.buildOpening(), ordinaryUnnamedLine+ordinaryOpeningBody; got != want {
		t.Errorf("an unnamed identity's opening moved:\n got %q\nwant %q", got, want)
	}
	c, _ := setupComposer(t)
	c.SetName("Walker")
	for name, compose := range map[string]func() (*Prompt, error){
		"Compose":             func() (*Prompt, error) { return c.Compose("work", 0) },
		"ComposeFolded":       func() (*Prompt, error) { return c.ComposeFolded("work", 0) },
		"ComposeWithin":       func() (*Prompt, error) { return c.ComposeWithin(32000, "work", 0) },
		"ComposeFoldedWithin": func() (*Prompt, error) { return c.ComposeFoldedWithin(32000, "work", 0) },
		"ComposeTurn":         func() (*Prompt, error) { return c.ComposeTurn("work", "facts", "", 0) },
	} {
		p, err := compose()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p.Text, "You are Walker.\n\n"+ordinaryOpeningBody+"\n\n") {
			t.Errorf("%s does not open with the ordinary opening:\n%.300s", name, p.Text)
		}
	}
}

func TestSafeHasItsOwnOpeningAndNoOutline(t *testing.T) {
	c, _ := setupComposer(t)
	c.SetName("Walker")
	c.SetClosing("\n\n## Your turn\nAnswer as asked.")
	const work, facts = "### Standing state (yours)\nwaiting on the review", "### Rhythm — last 48h: 12 turns, 80 calls"
	const section = "# SAFE MODE\n\nYou are in SAFE MODE."
	before, err := c.ComposeTurn(work, facts, section, 0)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := "You are Walker.\n\n" + ordinaryOpeningBody
	if !strings.HasPrefix(before.Text, ordinary+"\n") {
		t.Fatalf("rig: a compose not told of SAFE opens with the ordinary opening:\n%.300s", before.Text)
	}

	in, err := c.ComposeSafeTurn(work, facts, section, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	opening := "You are Walker.\n\n" + SafeOpening
	if want := opening + strings.TrimPrefix(before.Text, ordinary); in.Text != want {
		t.Fatalf("in SAFE the system text must be SAFE's opening and then every byte as it was:\n got %q\nwant %q", in.Text, want)
	}
	if in.Turn != before.Turn || !strings.HasPrefix(in.Turn, section) {
		t.Fatalf("SAFE's opening moved the turn's block, or the section is not first in it:\n got %q\nwant %q", in.Turn, before.Turn)
	}
	// What the ordinary opening says and SAFE makes false, the outline,
	// and the instruction to write a name: none is in SAFE's opening,
	// named or not.
	for _, never := range []string{"recorded permanently", "resumes who you are", "are below", "metabolizes", "in this order", NameFile, "# "} {
		if strings.Contains(opening, never) {
			t.Errorf("SAFE's opening says %q", never)
		}
	}
	c.SetName("")
	if bare := c.buildSafeOpening(false); bare != SafeOpening {
		t.Fatalf("an unnamed identity in SAFE reads SAFE's opening alone, with no instruction to write a name:\n%q", bare)
	}
	// A name they chose is said in SAFE as out of it: the record holds
	// none, and the operator sees the chosen one.
	c.SetChosenName(func() string { return "Aria" })
	if chose := c.buildSafeOpening(false); chose != "Your record holds no name. The name you chose, Aria, is the one your operator sees.\n\n"+SafeOpening {
		t.Fatalf("an unnamed identity that chose a name reads, in SAFE:\n%q", chose)
	}
	c.SetChosenName(nil)
	c.SetName("Walker")

	after, err := c.ComposeTurn(work, facts, section, 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.Text != before.Text || after.Turn != before.Turn || after.StableLen != before.StableLen {
		t.Fatal("a compose not told of SAFE, after one that was, is not the ordinary prompt")
	}
}

// A SUB-AGENT'S PROMPT IN SAFE CARRIES THE SECTION ITS OPENING POINTS AT.
// A sub-agent's compose has no block, and it passed no SAFE section, so a
// leg composed just after SAFE was entered opened "you are in SAFE MODE
// ... The section headed SAFE MODE says ..." with no such section in its
// prompt. Folded or full, the section is in its system text, whole, once,
// and nothing rides a block no caller sends.
func TestASubagentsSafePromptCarriesTheSectionItsOpeningPointsAt(t *testing.T) {
	c, _ := setupComposer(t)
	c.SetName("Walker")
	const section = "# SAFE MODE\n\nYou are in SAFE MODE. What happened, and what still works."
	for name, folded := range map[string]bool{"full": false, "folded": true} {
		p, err := c.ComposeSafeWithin(32000, "### Standing state (yours)\nwaiting on the review", section, false, 0, folded, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p.Text, "You are Walker.\n\n"+SafeOpening+"\n\n") {
			t.Errorf("%s: a sub-agent's SAFE prompt does not open with SAFE's opening:\n%.300s", name, p.Text)
		}
		if strings.Count(p.Text, section) != 1 || p.Turn != "" {
			t.Errorf("%s: the SAFE section must be in the system text once, and nothing in a block:\ntext %q\nblock %q", name, p.Text, p.Turn)
		}
	}
	// And the section never yields there either: under a budget that
	// drops the working state, it is whole.
	base, err := c.ComposeSafeWithin(32000, "", section, false, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.ComposeSafeWithin(base.TokenEstimate+40, strings.TrimSpace(strings.Repeat("a step of the plan that is long enough to matter\n\n", 400)), section, false, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, section) || !strings.Contains(p.Text, "Not Shown") && !strings.Contains(p.Text, SummaryMarker) {
		t.Fatalf("under pressure the SAFE section yielded, or nothing did:\n%s", p.Text)
	}
}

func TestSafeHasItsOwnClosingAndOutOfSafeTheClosingIsUnchanged(t *testing.T) {
	c, _ := setupComposer(t)
	c.SetName("Walker")
	const ordinary = "\n\n## Your turn\nThe block is written fresh each turn: change it with your tools, and note what should outlast the turn."
	const inSafe = "\n\n## Your turn\nThe block. " + SafeClosing
	c.SetClosing(ordinary)
	before, err := c.ComposeTurn("live work state", "### Rhythm — 1 turn", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(before.Text, ordinary) {
		t.Fatalf("rig: the text does not end with its closing: %.120q", before.Text[len(before.Text)-120:])
	}
	c.SetSafeClosing(inSafe)
	for name, compose := range map[string]func() (*Prompt, error){
		"turn":   func() (*Prompt, error) { return c.ComposeTurn("live work state", "### Rhythm — 1 turn", "", 0) },
		"plain":  func() (*Prompt, error) { return c.Compose("live work state", 0) },
		"folded": func() (*Prompt, error) { return c.ComposeFoldedWithin(32000, "live work state", 0) },
		"within": func() (*Prompt, error) { return c.ComposeWithin(32000, "live work state", 0) },
	} {
		out, err := compose()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(out.Text, ordinary) || name == "turn" && out.Text != before.Text {
			t.Errorf("%s: out of SAFE a composer holding SAFE's closing does not end as it did:\n%.200q", name, out.Text[len(out.Text)-200:])
		}
	}
	in, err := c.ComposeSafeTurn("live work state", "### Rhythm — 1 turn", "", false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(before.Text, "You are Walker.\n\n"+ordinaryOpeningBody), ordinary)
	if want := "You are Walker.\n\n" + SafeOpening + body + inSafe; in.Text != want {
		t.Fatalf("in SAFE the text must be SAFE's opening, every byte between as it was, and SAFE's closing:\n got %q\nwant %q", in.Text, want)
	}
	leg, err := c.ComposeSafeWithin(32000, "live work state", "# SAFE MODE\n\nYou are in SAFE MODE.", false, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(leg.Text, inSafe) {
		t.Errorf("a sub-agent's SAFE prompt does not end with SAFE's closing:\n%.200q", leg.Text[len(leg.Text)-200:])
	}
	for _, never := range []string{"note", "tool", "recorded", "outlast"} {
		if strings.Contains(SafeClosing, never) {
			t.Errorf("SAFE's closing sentence says %q", never)
		}
	}
}

// A START THAT WAITS OPENS AS DEGRADED. It holds SAFE's read-only posture,
// but it ends by itself and its section is headed DEGRADED, so SAFE's
// opening (the operator must act; "The section headed SAFE MODE") is false
// there. Named or not, the opening is DegradedOpening, and the rest of the
// prompt is what SAFE's would be.
func TestADegradedStartOpensAsDegraded(t *testing.T) {
	c, _ := setupComposer(t)
	c.SetName("Walker")
	const section = "# DEGRADED\n\nYou are running DEGRADED."
	safe, err := c.ComposeSafeTurn("work", "facts", section, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := c.ComposeSafeTurn("work", "facts", section, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(safe.Text, SafeOpening, DegradedOpening, 1); waiting.Text != want || strings.Contains(waiting.Text, "SAFE MODE") {
		t.Fatalf("a start that waits must open with DegradedOpening and differ from SAFE's prompt in that alone:\n%.400s", waiting.Text)
	}
	c.SetName("")
	if bare := c.buildSafeOpening(true); bare != DegradedOpening {
		t.Fatalf("an unnamed identity in a start that waits reads:\n%q", bare)
	}
}
