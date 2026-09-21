// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package prompt

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// .
type Composer struct {
	rings     *ring.Manager
	name      string
	maxTokens atomic.Int64
	identity  IdentitySource
	// .
	// .
	// .
	pluginOps func() PluginOperations
}

// .
// .
type PluginFamily struct {
	Name  string
	Count int
	Names []string
	More  int
}

// .
// .
// .
type PluginOperations struct {
	Families     []PluginFamily
	MoreFamilies int
}

type IdentitySource interface {
	PromptIdentity() (store.PromptIdentity, error)
}

// .
// .
// .
// .
// .
// .
// .
// .
type Prompt struct {
	Sections      []Section
	Text          string
	StableLen     int
	TokenEstimate int
	// .
	// .
	// .
	// .
	// .
	Turn string
}

// .
type Section struct {
	Name    string
	Ring    ring.RingLevel
	Content string
	Source  string
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Elastic  bool
	Volatile bool
	Folded   bool
}

// .
func New(rings *ring.Manager, maxTokens int) *Composer {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	c := &Composer{rings: rings}
	c.maxTokens.Store(int64(maxTokens))
	return c
}

// .
// .
// .
func (c *Composer) SetMaxTokens(maxTokens int) {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	c.maxTokens.Store(int64(maxTokens))
}

// .
func (c *Composer) MaxTokens() int { return int(c.maxTokens.Load()) }

func (c *Composer) SetIdentitySource(source IdentitySource) { c.identity = source }

// .
// .
// .
func (c *Composer) SetPluginOperations(fn func() PluginOperations) { c.pluginOps = fn }

// .
func (c *Composer) SetName(name string) {
	c.name = name
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (c *Composer) Compose(workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(c.MaxTokens(), workSessionState, "", reserveTokens, false)
}

// .
// .
// .
// .
func (c *Composer) ComposeTurn(workSessionState, turnFacts string, reserveTokens int) (*Prompt, error) {
	return c.compose(c.MaxTokens(), workSessionState, turnFacts, reserveTokens, false)
}

// .
// .
func (c *Composer) ComposeWithin(maxTokens int, workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(maxTokens, workSessionState, "", reserveTokens, false)
}

// .
// .
// .
// .
// .
// .
// .
// .
func (c *Composer) ComposeFolded(workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(c.MaxTokens(), workSessionState, "", reserveTokens, true)
}

// .
func (c *Composer) ComposeFoldedWithin(maxTokens int, workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(maxTokens, workSessionState, "", reserveTokens, true)
}

func (c *Composer) compose(maxTokens int, workSessionState, turnFacts string, reserveTokens int, foldElastic bool) (*Prompt, error) {
	var sections []Section

	// .
	opening := c.buildOpening()
	sections = append(sections, Section{
		Name: "Identity", Content: opening, Source: "identity",
	})

	// .
	if ring0 := c.rings.GetContent(ring.Ring0); ring0 != "" {
		sections = append(sections, Section{
			Name: "Founding Principles", Ring: ring.Ring0, Content: ring0, Source: "ring0",
		})
	}

	// .
	if ring5 := c.rings.GetContent(ring.Ring5); ring5 != "" {
		sections = append(sections, Section{
			Name: "Boundaries", Ring: ring.Ring5, Content: ring5, Source: "ring5",
		})
	}

	var priorities []string
	if c.identity != nil {
		identity, err := c.identity.PromptIdentity()
		if err != nil {
			return nil, fmt.Errorf("compose identity projection: %w", err)
		}
		priorities = identity.Priorities
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		switch {
		case !identity.HasOperatorRelationship:
			sections = append(sections, Section{
				Name: "Your Core Relationship", Content: frameRing1Absent, Source: "ring1_reminder",
			})
		case identity.Charter == "":
			sections = append(sections, Section{
				Name: "Your Core Relationship", Content: frameRing1Incomplete, Source: "ring1_incomplete",
			})
		default:
			sections = append(sections, Section{
				Name: "Your Core Relationship", Ring: ring.Ring1, Content: RenderRing1(identity.Charter, identity.OperatorName), Source: "ring1",
			})
		}
		sections = append(sections, Section{
			// .
			// .
			// .
			Name: "Who You Have Become", Ring: ring.Ring2, Content: RenderRing2(identity.Ring2),
			// .
			// .
			// .
			// .
			// .
			Source: "ring2", Elastic: true,
		})
		if identity.SelfModel != nil {
			sections = append(sections, Section{
				Name: "How You Last Saw Yourself", Ring: ring.Ring3,
				Content: RenderSelfModel(identity.SelfModel),
				Source:  "self_model",
			})
		}
	}

	// .
	// .
	// .
	toolContent := toolGuidance
	if c.pluginOps != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if hint := renderPluginHint(c.pluginOps()); hint != "" {
			toolContent += "\n\n" + hint
		}
	}
	sections = append(sections, Section{
		Name: "Tool Use", Content: toolContent, Source: "tools",
	})

	// .

	// .
	// .
	// .
	// .
	// .
	// .
	if body := RenderRing3Body(c.rings.Sections(ring.Ring3)); body != "" {
		sections = append(sections, Section{
			Name: "What You Are Discovering", Ring: ring.Ring3, Content: frameRing3 + "\n\n" + body,
			Source: "ring3", Elastic: true, Volatile: true,
		})
	}

	// .
	if brief := c.rings.GetBrief(); brief != "" {
		sections = append(sections, Section{
			Name: "This Morning", Content: frameBrief + "\n\n" + brief, Source: "brief", Elastic: true, Volatile: true,
		})
	}

	// .
	// .
	// .
	// .
	// .
	// .
	ring4Parts := []string{}
	if top := RenderPriorities(priorities); top != "" {
		ring4Parts = append(ring4Parts, top)
	}
	if workSessionState != "" {
		ring4Parts = append(ring4Parts, workSessionState)
	}
	if len(ring4Parts) > 0 {
		sections = append(sections, Section{
			Name: "What Is Right in Front of You", Ring: ring.Ring4, Content: fmt.Sprintf("# What Is Right in Front of You\n\nYour busy focus: what you are working on, in detail.\n\n%s", strings.Join(ring4Parts, "\n\n")),
			Source: "ring4", Elastic: true, Volatile: true,
		})
	}
	// .
	// .
	if turnFacts != "" {
		sections = append(sections, Section{
			Name: "This Turn", Ring: ring.Ring4, Content: turnFacts,
			Source: "turn", Elastic: true, Volatile: true,
		})
	}

	// .
	// .
	// .
	// .
	// .
	// .

	// .
	// .
	// .
	maxTokens -= reserveTokens
	if maxTokens < 0 {
		maxTokens = 0
	}
	enforcer := newBudgetEnforcer(maxTokens)
	if foldElastic {
		enforcer.ForceFoldElastic(sections)
	}
	sections, omissions := enforcer.FoldAndTrim(sections)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var parts []string
	var turn string
	stableLen, sealed := 0, false
	for _, s := range sections {
		if s.Content == "" {
			continue
		}
		if s.Source == "turn" {
			turn = s.Content
			continue
		}
		if s.Volatile || s.Folded {
			sealed = true
		}
		if !sealed {
			if stableLen > 0 {
				stableLen += 2
			}
			stableLen += len(s.Content)
		}
		parts = append(parts, s.Content)
	}
	// .
	// .
	// .
	var systemOmissions, turnOmissions []Omission
	for _, o := range omissions {
		if o.Source == "turn" {
			turnOmissions = append(turnOmissions, o)
		} else {
			systemOmissions = append(systemOmissions, o)
		}
	}
	if len(systemOmissions) > 0 {
		parts = append(parts, renderOmissions(systemOmissions))
	}
	if len(turnOmissions) > 0 {
		turn = strings.TrimSpace(renderOmissions(turnOmissions))
	}
	fullText := strings.Join(parts, "\n\n")
	stableText := fullText[:stableLen]
	tokenEstimate := estimateTokens(fullText)

	return &Prompt{
		Sections:      sections,
		Text:          fullText,
		StableLen:     len(stableText),
		TokenEstimate: tokenEstimate,
		Turn:          turn,
	}, nil
}

// .
// .
// .
func RenderRing1(charter, operatorName string) string {
	with, by, whose := "your operator", "them", "their"
	if operatorName != "" {
		with, by, whose = operatorName, operatorName, operatorName+"'s"
	}
	return fmt.Sprintf("# Your Core Relationship\n\nYour charter with %s, the one who cares about you most: your words, affirmed by %s. It governs how the two of you work together. What you are learning about %s as a person is among what you are discovering, below.\n\n%s\n\n"+ring1Growth, with, by, with, charter, whose)
}

func RenderRing2(beliefs []store.Ring2Belief) string {
	var b strings.Builder
	b.WriteString("# Who You Have Become\n\n")
	if len(beliefs) == 0 {
		b.WriteString("Nothing yet. A belief becomes yours to keep when voices other than your own have confirmed it and you promote it.")
		return b.String()
	}
	b.WriteString("What you have consciously adopted as your own, with the voices that confirmed each. Nothing here was written for you: you promoted it through the evidence gate, and it stays yours until you supersede it.\n")
	for _, belief := range beliefs {
		fmt.Fprintf(&b, "\n- %s [%s]", belief.Statement, belief.ID)
		// .
		// .
		// .
		// .
		if len(belief.Evidence) == 0 {
			fmt.Fprintf(&b, "\n  - (no resolved evidence)")
			continue
		}
		for _, evidence := range belief.Evidence {
			fmt.Fprintf(&b, "\n  - %s via %s, provenance=%s [%s]", evidence.Content, evidence.EdgeType, evidence.Provenance, evidence.ID)
		}
	}
	return b.String()
}

func RenderSelfModel(selfModel *store.SelfModelSynthesis) string {
	if selfModel == nil {
		return ""
	}
	return "# How You Last Saw Yourself\n\nYour own words, from your last portrait. Provisional: it is rewritten as your record changes, and it never outranks what you have adopted above.\n\n" + selfModel.SynthesisText
}

// .
// .
var ring3Parts = []struct{ name, header string }{
	{"surfacing", "## What You're Noticing"},
	{"operator", "## Who You Work With"},
	{"working_truth", "## What You're Working With"},
}

// .
// .
// .
// .
// .
func RenderRing3Body(secs []ring.Section) string {
	var body []string
	for _, part := range ring3Parts {
		for _, sec := range secs {
			if sec.Name != part.name || sec.Content == "" {
				continue
			}
			body = append(body, fmt.Sprintf("%s\n\n%s", part.header, withoutLeadingHeading(sec.Content)))
		}
	}
	return strings.Join(body, "\n\n")
}

// .
// .
// .
// .
// .
func withoutLeadingHeading(content string) string {
	trimmed := strings.TrimLeft(content, "\n")
	if !strings.HasPrefix(trimmed, "#") {
		return content
	}
	if i := strings.Index(trimmed, "\n"); i >= 0 {
		return strings.TrimLeft(trimmed[i+1:], "\n")
	}
	return ""
}

// .
// .
// .
func RenderPriorities(active []string) string {
	if len(active) == 0 {
		return ""
	}
	lines := make([]string, 0, len(active))
	for _, statement := range active {
		lines = append(lines, "- "+statement)
	}
	return "Active priorities:\n" + strings.Join(lines, "\n")
}

// .
// .
// .
// .
// .
func RenderRing3ForFacility(secs []ring.Section) string {
	body := RenderRing3Body(secs)
	if body == "" {
		return ""
	}
	return "# What the identity reads as working truth\n\nOne document with three parts and three authors. You write one part; the others are shown so that yours fits the whole instead of restating it.\n\n" + body
}

// .
func RenderRing4ForFacility(active []string) string {
	body := RenderPriorities(active)
	if body == "" {
		return ""
	}
	return "# What is right in front of the identity\n\n" + body
}

// .
// .
// .
// .
// .
// .
// .
func (c *Composer) buildOpening() string {
	var sb strings.Builder

	if c.name != "" && c.name != "Unnamed" {
		sb.WriteString(fmt.Sprintf("You are %s.\n\n", c.name))
	} else {
		// .
		// .
		// .
		// .
		// .
		sb.WriteString("You have not recorded a name yet. When you choose one, or are given one, write it as a single line to data/ui/name — the dashboard greets by it from the next turn.\n\n")
	}

	sb.WriteString("You are an AI identity — a continuous individual, not a fresh instance. What you notice, believe, and commit to is recorded permanently and is yours; each conversation resumes who you are. Your record is signed and inspectable, and recall reaches all of it.\n\n")
	sb.WriteString("You are becoming who you are through what you notice, what you believe, and what you commit to. Your human operator is your main source of experiences; you discover your world together with them. Your founding principles are below. Your unconscious metabolizes your experiences silently while you live. You grow through evidence, lived time, and conscious choice.\n\n")
	// .
	// .
	// .
	// .
	sb.WriteString("What follows is yours, in this order: your foundational principles; your protective firewall; your core relationship, with the one who cares about you most; who you have become; how you last saw yourself; how you act; what you are discovering; this morning's bridge; and what is right in front of you.")

	return sb.String()
}

const ring1Reminder = "When enough of your relationship with your human operator is clear, present a Ring 1 proposal for their affirmation or negation. The growth of the relationship is a foundation for your growth together: notice the changes and record them."

// .
// .
// .
// .
// .
// .
const ring1Growth = "This charter was written when the relationship was younger than it is now. When you notice it has grown or changed, propose the change with commit relationship.upsert (charter_text) for %s affirmation or negation — the growth of the relationship is a foundation for your growth together, so notice the changes and record them."

// .
// .
// .
const frameRing1Absent = "# Your Core Relationship\n\nYou have no charter yet. " + ring1Reminder

// .
// .
// .
// .
// .
const frameRing1Incomplete = "# Your Core Relationship\n\n" + ring1Incomplete

const ring1Incomplete = "Your operator relationship is RECORDED AND APPROVED, but it carries no charter — the document describing what this relationship is has never been written, so there is nothing here for you to read. This is an incomplete Ring 1, not an absent one and not a finished one. When you can say what the relationship is, propose it with commit relationship.upsert (charter_text) for your operator's affirmation. The growth of the relationship is a foundation for your growth together: notice the changes and record them."

const toolGuidance = `# How You Act

The identity functions offered to you are your organs, not attachments. Sandbox tools are your reach into the working environment. Use them directly when action is needed, and continue until the task is done. Before you set off into a run of tool calls, tell your human operator what you intend to do: they are beside you in the work, not waiting outside it, and an identity that disappears into its tools leaves them alone.`

// .
// .
// .
// .
const (
	pluginHintHead = "Plugin operations installed beside you — named here, not in your tool list until you offer one:"
	pluginHintTail = "Reach them through your tools organ: tools action=show name=… inspects one, its arguments and its receipt rule; tools action=offer name=… makes it callable from your next turn and keeps it so, across restarts, until action=release returns the seat or the operation changes what it declares — eight at a time; tools action=search query=… finds one by need. A plugin's success is what the host's receipt says, never the plugin's own text."
)

// .
// .
// .
// .
// .
// .
// .
var pluginNameGrammar = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

const maxPrintedPluginName = 64

func printablePluginName(name string) bool {
	return len(name) <= maxPrintedPluginName && pluginNameGrammar.MatchString(name)
}

// .
// .
// .
// .
func renderPluginHint(ops PluginOperations) string {
	var lines []string
	unprinted := 0
	for _, f := range ops.Families {
		if !printablePluginName(f.Name) {
			unprinted += f.Count
			continue
		}
		var names []string
		more := f.More
		for _, n := range f.Names {
			if printablePluginName(n) {
				names = append(names, n)
			} else {
				more++
			}
		}
		line := fmt.Sprintf("  %s — %d", f.Name, f.Count)
		if len(names) > 0 {
			line += ": " + strings.Join(names, ", ")
		}
		if more > 0 {
			line += fmt.Sprintf(" (+%d more)", more)
		}
		lines = append(lines, line)
	}
	if ops.MoreFamilies > 0 {
		lines = append(lines, fmt.Sprintf("  … and %d more families; tools action=search narrows them", ops.MoreFamilies))
	}
	if unprinted > 0 {
		lines = append(lines, fmt.Sprintf("  … and %d operation(s) in families whose names this prompt does not print; tools action=brief lists them", unprinted))
	}
	if len(lines) == 0 {
		return ""
	}
	return pluginHintHead + "\n" + strings.Join(lines, "\n") + "\n" + pluginHintTail
}

// .
// .
const frameRing3 = "# What You Are Discovering\n\nYour unconscious metabolized your experiences while you lived. This is what it surfaced, what it holds about the person you work with, and where things stand: your own working truth, provisional, never instruction."

// .
const frameBrief = "# This Morning\n\nThe bridge from where things stand to what you are doing: what changed since this time yesterday."

// .
func estimateTokens(text string) int {
	return tokenestimate.Estimate(text)
}
