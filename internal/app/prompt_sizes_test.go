package app

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// promptSizesFile is the size report.
var promptSizesFile = testdataPath("prompt_sizes.txt")

// sizeRow is one row of the report: a group (a shape, or a tool set), the
// part measured, the platform when the number is one platform's, and the
// numbers.
type sizeRow struct {
	group, part, goos, values string
}

func (r sizeRow) key() string { return r.group + " | " + r.part + goosTag(r.goos) }

func goosTag(goos string) string {
	if goos == "" {
		return ""
	}
	return " [" + goos + "]"
}

func (r sizeRow) line() string { return r.key() + " | " + r.values }

// subject names a row's group as a failure says it: "shape turn_operator",
// "tool set resident".
func (r sizeRow) subject() string {
	if set, ok := strings.CutPrefix(r.group, "tools "); ok {
		return "tool set " + set
	}
	return "shape " + r.group
}

// measured is "chars | tokens" for a text.
func measured(text string) string {
	return strconv.Itoa(utf8.RuneCountInString(text)) + " | " + strconv.Itoa(tokenestimate.Estimate(text))
}

// measuredPair is "chars / tokens" for a part of a row.
func measuredPair(text string) string {
	return strconv.Itoa(utf8.RuneCountInString(text)) + " / " + strconv.Itoa(tokenestimate.Estimate(text))
}

// liveMarkers are the markers that stand for a live platform text, by the
// name a size row gives it.
var liveMarkers = map[string]string{
	ring0Marker:     "Ring 0",
	ring5Marker:     "Ring 5",
	bootstrapMarker: "the founding packet",
}

const liveValues = "live, not pinned | -"

// sectionsOf splits a text a model reads into the parts a reader names: at
// each heading of the first or second level, at each live platform text's
// marker, and at the rule ("---") between a facility's preamble and its
// prompt. Blank lines that would stand alone join the part before them.
// The parts, put back together, are the text. A part with no heading of
// its own is counted in the one before it: the facility preamble's charter,
// which the gate writes with no heading, is counted in "## Your Walls".
func sectionsOf(text string) (names, bodies []string) {
	lines := strings.SplitAfter(text, "\n")
	var cur strings.Builder
	name := "(opening)"
	flush := func(next string) {
		switch {
		case cur.Len() == 0:
		case strings.TrimSpace(cur.String()) == "" && len(bodies) > 0:
			bodies[len(bodies)-1] += cur.String()
		default:
			names, bodies = append(names, name), append(bodies, cur.String())
		}
		cur.Reset()
		name = next
	}
	for _, l := range lines {
		bare := strings.TrimRight(l, "\n")
		switch {
		case liveMarkers[bare] != "":
			flush(liveMarkers[bare])
			cur.WriteString(l)
			flush("(after " + liveMarkers[bare] + ")")
		case strings.HasPrefix(bare, "# ") || strings.HasPrefix(bare, "## "):
			flush(clipName(bare))
			cur.WriteString(l)
		case bare == "---":
			flush("--- (the facility's own prompt)")
			cur.WriteString(l)
		default:
			cur.WriteString(l)
		}
	}
	flush("")
	return names, bodies
}

func clipName(s string) string {
	s = strings.ReplaceAll(s, "|", "/")
	if r := []rune(s); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return s
}

// blockOpen is the line the turn block opens with, at the end of the
// current message (conversation.TurnBlock).
const blockOpen = "\n[substrate · this turn]\n"

// sizeRows measures one rendered shape: its parts and their sections, its
// totals, and its tools on this platform.
func sizeRows(t *testing.T, r renderedShape) []sizeRow {
	t.Helper()
	var rows []sizeRow
	seen := map[string]int{}
	add := func(part, text string) {
		part = clipName(part)
		if seen[part]++; seen[part] > 1 {
			part += fmt.Sprintf(" (%d)", seen[part])
		}
		rows = append(rows, sizeRow{group: r.shape.name, part: part, values: measured(text)})
	}
	var system, messages strings.Builder
	for _, p := range r.parts {
		into := &messages
		if p.role == "system" {
			into = &system
		}
		// The system text by its sections; any other message whole, and
		// the block that closes the current one by its sections.
		names, bodies := sectionsOf(p.text)
		words, block, hasBlock := strings.Cut(p.text, blockOpen)
		if hasBlock {
			block = blockOpen[1:] + block
			words += "\n"
		}
		if p.role == "system" {
			hasBlock = false
		} else {
			names, bodies = []string{"words"}, []string{words}
		}
		for i, body := range bodies {
			if label := liveMarkers[strings.TrimRight(body, "\n")]; label != "" {
				rows = append(rows, sizeRow{group: r.shape.name, part: p.head + " › " + label, values: liveValues})
				continue
			}
			add(p.head+" › "+names[i], body)
			into.WriteString(body)
		}
		if hasBlock {
			names, bodies = sectionsOf(block)
			for i, body := range bodies {
				if i == 0 && names[i] == "(opening)" {
					names[i] = "the block's frame"
				}
				add(p.head+" › block › "+names[i], body)
				into.WriteString(body)
			}
		}
		for i, c := range p.calls {
			add(fmt.Sprintf("%s › tool call %d", p.head, i+1), c)
			into.WriteString(c)
		}
	}
	add("system, without the live parts", system.String())
	add("messages", messages.String())
	if len(r.tools) > 0 {
		chars, tokens := toolsSize(t, r.tools)
		rows = append(rows, sizeRow{group: r.shape.name, part: "tools (the set " + r.shape.tools + ")", goos: runtime.GOOS,
			values: fmt.Sprintf("%d | %d", chars, tokens)})
		rows = append(rows, sizeRow{group: r.shape.name, part: "whole request, without the live parts", goos: runtime.GOOS,
			values: fmt.Sprintf("%d | %d", utf8.RuneCountInString(system.String()+messages.String())+chars, tokenestimate.Estimate(system.String()+messages.String())+tokens)})
	}
	return rows
}

// toolDefinitionJSON is one definition as the request carries it.
func toolDefinitionJSON(t *testing.T, d any) string {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// toolsSize is a tool list's characters and tokens, each definition as
// the request carries it.
func toolsSize(t *testing.T, defs []llm.ToolDefinition) (chars, tokens int) {
	t.Helper()
	for _, d := range defs {
		j := toolDefinitionJSON(t, d)
		chars += utf8.RuneCountInString(j)
		tokens += tokenestimate.Estimate(j)
	}
	return chars, tokens
}

// toolSetRows measures each tool of a set on this platform.
func toolSetRows(t *testing.T, set string, defs []llm.ToolDefinition) []sizeRow {
	t.Helper()
	group := "tools " + set
	var rows []sizeRow
	for _, d := range defs {
		params := toolDefinitionJSON(t, d.Function.Parameters)
		rows = append(rows, sizeRow{group: group, part: d.Function.Name, goos: runtime.GOOS,
			values: measured(toolDefinitionJSON(t, d)) + " | description " + measuredPair(d.Function.Description) + " | parameters " + measuredPair(params)})
	}
	chars, tokens := toolsSize(t, defs)
	rows = append(rows, sizeRow{group: group, part: "all " + strconv.Itoa(len(defs)), goos: runtime.GOOS, values: fmt.Sprintf("%d | %d", chars, tokens)})
	return rows
}

// readSizeRows reads the report's rows.
func readSizeRows() ([]sizeRow, error) {
	raw, err := os.ReadFile(promptSizesFile)
	if err != nil {
		return nil, err
	}
	var rows []sizeRow
	for _, l := range strings.Split(string(raw), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.SplitN(l, " | ", 3)
		if len(f) != 3 {
			return nil, fmt.Errorf("%s: a row is not group | part | numbers: %q", promptSizesFile, l)
		}
		r := sizeRow{group: f[0], part: f[1], values: f[2]}
		for _, goos := range []string{"darwin", "linux", "windows"} {
			if tag := goosTag(goos); strings.HasSuffix(r.part, tag) {
				r.part, r.goos = strings.TrimSuffix(r.part, tag), goos
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// sizeReport is the report: this run's rows, and each row another platform
// measured for a shape or a tool set this run still has, after this
// platform's rows for the same group. A part only another platform has
// (a tool only it offers) is kept for it; its own run rewrites its rows.
func sizeReport(rows, recorded []sizeRow) string {
	var b strings.Builder
	b.WriteString(`# Prompt sizes: every request shape the host sends a model, part by part
# (TestEveryRequestShapeKeepsItsRecordedSize; measured by internal/app/prompt_sizes_test.go).
# Each row: group | part | characters | tokens; a tool's row adds its description's and its
# parameters' characters / tokens. Characters are Unicode code points of the text as its golden
# pins it (testdata/whole_prompt). Tokens are the host's own estimate, internal/tokenestimate:
# one token per three UTF-8 bytes, rounded up; the composer folds and the admission refuses by it.
# Ring 0, Ring 5 and the founding packet are fetched live from the platform: their rows say
# "live, not pinned", and no total counts them. A row tagged [darwin], [linux] or [windows] is that
# platform's (the tools differ by platform); each platform's run rewrites only its own.
# Regenerate: go test ./internal/app -run 'TestEachTurnPathSendsItsPinnedPrompt|TestEachSafePostureSendsItsPinnedPrompt|TestEveryRequestShape' -update-prompt-golden
`)
	type groupRows struct {
		name string
		rows []sizeRow
	}
	var groups []*groupRows
	byName := map[string]*groupRows{}
	for _, r := range rows {
		g := byName[r.group]
		if g == nil {
			g = &groupRows{name: r.group}
			byName[r.group] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, r)
	}
	for _, r := range recorded {
		if g := byName[r.group]; g != nil && r.goos != "" && r.goos != runtime.GOOS {
			g.rows = append(g.rows, r)
		}
	}
	for _, g := range groups {
		b.WriteString("\n")
		// Within a group: the rows every platform shares in the order
		// measured, then each platform's, platforms in name order.
		slices.SortStableFunc(g.rows, func(x, y sizeRow) int { return strings.Compare(x.goos, y.goos) })
		for _, r := range g.rows {
			b.WriteString(r.line() + "\n")
		}
	}
	return b.String()
}
