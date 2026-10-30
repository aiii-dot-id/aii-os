package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type requestShape struct {
	name  string // its golden's name
	about string // what it is, in a line: the head of its golden
	// tools names the set of tool definitions the request offers
	// (testdata/whole_prompt/tools/<tools>.<GOOS>.golden), "" when it
	// offers none.
	tools   string
	dialect string
	// sameAs names a shape whose request this one must be, byte for byte
	// (after its head): a posture that adds no text is pinned by that
	// sameness and keeps no golden of its own.
	sameAs string
	send   func(t *testing.T) sentRequest
}

// sentRequest is the one request a shape is pinned by: the body the fake
// provider received, the fixture directory pinnedText writes <dir>, and
// any normalisation its messages need beyond pinnedText's, each said where
// the shape makes it.
type sentRequest struct {
	body string
	dir  string
	more func(string) string
}

// requestPart is one part of a request a model reads: a message, or in
// the Anthropic encoding a system block. Its text is as a golden pins it.
type requestPart struct {
	head  string // as the golden heads it: "message 0 (system)", "system block 1"
	role  string // system, user, assistant or tool
	text  string
	calls []string // the tool calls an assistant message asks for, as the golden writes them
}

// renderedShape is a shape's request as its golden holds it: its parts,
// the tools it offers, the golden's text, and the tool set's definitions
// on this platform, rendered.
type renderedShape struct {
	shape    requestShape
	dir      string // the fixture directory its request was sent from
	parts    []requestPart
	tools    []llm.ToolDefinition
	golden   string // its parts and the tool set's name
	toolText string // the set's definitions, as its golden holds them; "" when none
}

// renderShape renders one sent request.
func renderShape(t *testing.T, s requestShape, sent sentRequest) renderedShape {
	t.Helper()
	pin := func(text string) string {
		out := pinnedText(t, text, sent.dir)
		if sent.more != nil {
			out = sent.more(out)
		}
		return out
	}
	read := chatParts
	if s.dialect == "anthropic" {
		read = anthropicParts
	}
	parts, tools, choice := read(t, s, sent.body)
	r := renderedShape{shape: s, dir: sent.dir, tools: tools}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s\n\n", s.name, s.about)
	for _, p := range parts {
		p.text = pin(p.text)
		for i := range p.calls {
			p.calls[i] = pin(p.calls[i])
		}
		r.parts = append(r.parts, p)
		fmt.Fprintf(&b, "=== %s ===\n%s\n", p.head, p.text)
		for _, c := range p.calls {
			b.WriteString(c + "\n")
		}
	}
	if choice != "" {
		fmt.Fprintf(&b, "=== tool choice: %s ===\n", choice)
	}
	switch {
	case s.tools == "" && len(tools) > 0:
		t.Fatalf("the %s request offers %d tools and the shape names no tool set", s.name, len(tools))
	case s.tools != "" && len(tools) == 0:
		t.Fatalf("the %s request offers no tools and the shape names the set %q", s.name, s.tools)
	case s.tools != "":
		fmt.Fprintf(&b, "=== tools: the set %q, %s ===\n", s.tools, toolSetFile("<GOOS>", s.tools))
		r.toolText = pinnedText(t, renderTools(t, s.tools, tools), sent.dir)
	default:
		b.WriteString("=== tools: none ===\n")
	}
	r.golden = b.String()
	return r
}

// chatParts reads a chat-completions body: each message whole, with its
// role, the call it answers and the calls it asks for.
func chatParts(t *testing.T, s requestShape, body string) ([]requestPart, []llm.ToolDefinition, string) {
	t.Helper()
	var req struct {
		Messages []struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"tool_call_id"`
			ToolCalls  []llm.ToolCall  `json:"tool_calls"`
		} `json:"messages"`
		Tools      []llm.ToolDefinition `json:"tools"`
		ToolChoice string               `json:"tool_choice"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("the %s request is not a chat-completions body: %v", s.name, err)
	}
	var parts []requestPart
	for i, m := range req.Messages {
		var content string
		if len(m.Content) > 0 && string(m.Content) != "null" {
			if err := json.Unmarshal(m.Content, &content); err != nil {
				t.Fatalf("the %s request's message %d carries content parts, which this golden does not render: %s", s.name, i, m.Content)
			}
		}
		head := m.Role
		if m.ToolCallID != "" {
			head += ", answering " + m.ToolCallID
		}
		p := requestPart{head: fmt.Sprintf("message %d (%s)", i, head), role: m.Role, text: content}
		for _, c := range m.ToolCalls {
			p.calls = append(p.calls, fmt.Sprintf("--- tool call %s: %s\n%s", c.ID, c.Function.Name, c.Function.Arguments))
		}
		parts = append(parts, p)
	}
	return parts, req.Tools, req.ToolChoice
}

// anthropicParts reads an Anthropic messages body: each system block, then
// each message's blocks in order, and the tools as definitions.
func anthropicParts(t *testing.T, s requestShape, body string) ([]requestPart, []llm.ToolDefinition, string) {
	t.Helper()
	var req struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Input     json.RawMessage `json:"input"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
		} `json:"tools"`
		ToolChoice json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("the %s request is not an Anthropic messages body: %v", s.name, err)
	}
	var parts []requestPart
	for i, b := range req.System {
		parts = append(parts, requestPart{head: fmt.Sprintf("system block %d", i+1), role: "system", text: b.Text})
	}
	for i, m := range req.Messages {
		var texts []string
		p := requestPart{head: fmt.Sprintf("message %d (%s)", i+1, m.Role), role: m.Role}
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				texts = append(texts, c.Text)
			case "tool_use":
				p.calls = append(p.calls, fmt.Sprintf("--- tool call %s: %s\n%s", c.ID, c.Name, c.Input))
			default:
				t.Fatalf("the %s request's message %d carries a %s block, which this golden does not render", s.name, i+1, c.Type)
			}
		}
		// The dialect may carry one message's text in several blocks:
		// each is shown, a rule between them.
		p.text = strings.Join(texts, "\n--- next text block ---\n")
		parts = append(parts, p)
	}
	var tools []llm.ToolDefinition
	for _, d := range req.Tools {
		def := llm.ToolDefinition{Type: "function"}
		def.Function.Name, def.Function.Description, def.Function.Parameters = d.Name, d.Description, d.InputSchema
		tools = append(tools, def)
	}
	choice := ""
	if len(req.ToolChoice) > 0 {
		choice = string(req.ToolChoice)
	}
	return parts, tools, choice
}

func renderTools(t *testing.T, set string, defs []llm.ToolDefinition) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# tools %q on %s: %d definitions, in the order a request offers them\n", set, runtime.GOOS, len(defs))
	for i, d := range defs {
		params, err := json.MarshalIndent(d.Function.Parameters, "", "  ")
		if err != nil {
			t.Fatalf("tool %s: %v", d.Function.Name, err)
		}
		fmt.Fprintf(&b, "\n=== tool %d of %d: %s ===\n%s\n--- parameters ---\n%s\n", i+1, len(defs), d.Function.Name, d.Function.Description, params)
	}
	return b.String()
}

var goldenRoot = func() string {
	wd, err := os.Getwd()
	if err != nil {
		return "testdata"
	}
	return filepath.Join(wd, "testdata")
}()

func testdataPath(rel ...string) string {
	return filepath.Join(append([]string{goldenRoot}, rel...)...)
}

// toolSetFile is where a tool set's golden lives, as a golden names it:
// with forward slashes on every platform, so the name is the same bytes.
func toolSetFile(goos, set string) string {
	return "testdata/whole_prompt/tools/" + set + "." + goos + ".golden"
}

// sentShapes is the last rendering of every shape's request, which the
// size report reads after the goldens' test made it: the same requests,
// sent once.
var sentShapes struct {
	sync.Mutex
	rendered []renderedShape
}

// renderedShapes is the rendering the goldens' test made last in this
// test binary, or a new one when it has made none (renderShapes).
func renderedShapes(t *testing.T) []renderedShape {
	t.Helper()
	sentShapes.Lock()
	rendered := sentShapes.rendered
	sentShapes.Unlock()
	if rendered != nil {
		return rendered
	}
	return renderShapes(t)
}

func renderShapes(t *testing.T) []renderedShape {
	t.Helper()
	defer tools.ShellWallAsAccountForTest()()
	var out []renderedShape
	for _, s := range requestShapes() {
		ok := t.Run(s.name, func(t *testing.T) {
			out = append(out, renderShape(t, s, s.send(t)))
		})
		if !ok {
			t.Fatalf("the %s request could not be made; nothing is compared", s.name)
		}
	}
	sentShapes.Lock()
	sentShapes.rendered = out
	sentShapes.Unlock()
	return out
}

// EVERY REQUEST SHAPE SENDS ITS PINNED WHOLE REQUEST, and offers the tool
// set pinned for this platform. A change to anything a model is sent — a
// section, a sentence of a facility's prompt, a wake fact, a tool's
// description or a parameter — is a change to these files, made on purpose
// and read in the diff.
func TestEveryRequestShapeSendsItsPinnedWholeRequest(t *testing.T) {
	shapes := renderShapes(t)
	written := map[string]string{} // tool set → the definitions this run wrote for it
	for _, r := range shapes {
		file := testdataPath("whole_prompt", r.shape.name+".golden")
		if r.shape.sameAs != "" {
			if other, ok := renderedNamed(shapes, r.shape.sameAs); !ok {
				t.Errorf("shape %s is to be shape %s, which is not rendered", r.shape.name, r.shape.sameAs)
			} else if body, want := withoutHead(r.golden), withoutHead(other.golden); body != want {
				t.Errorf("shape %s added to or changed shape %s's request:\n%s", r.shape.name, r.shape.sameAs, promptDiff(want, body))
			}
		} else if *updatePromptGolden {
			writeGolden(t, file, r.golden)
		} else if want, err := os.ReadFile(file); err != nil {
			t.Errorf("shape %s: %v (write it with -update-prompt-golden)", r.shape.name, err)
		} else if r.golden != string(want) {
			t.Errorf("shape %s: its request moved from %s:\n%s", r.shape.name, file, promptDiff(string(want), r.golden))
		}
		if r.shape.tools == "" {
			continue
		}
		setFile := testdataPath("whole_prompt", "tools", r.shape.tools+"."+runtime.GOOS+".golden")
		if *updatePromptGolden {
			if prev, ok := written[r.shape.tools]; ok && prev != r.toolText {
				t.Errorf("shape %s offers other tools than an earlier shape that names the set %q; give it a set of its own:\n%s", r.shape.name, r.shape.tools, promptDiff(prev, r.toolText))
				continue
			}
			written[r.shape.tools] = r.toolText
			writeGolden(t, setFile, r.toolText)
			continue
		}
		want, err := os.ReadFile(setFile)
		if err != nil {
			t.Errorf("shape %s: no tool set %q is pinned for %s: %v (write it on %s with -update-prompt-golden)", r.shape.name, r.shape.tools, runtime.GOOS, err, runtime.GOOS)
			continue
		}
		if r.toolText != string(want) {
			t.Errorf("shape %s: the tools it offers on %s moved from %s:\n%s", r.shape.name, runtime.GOOS, setFile, promptDiff(string(want), r.toolText))
		}
	}
	// A golden no shape renders pins nothing, and reads as if it did: it
	// fails here, and an update removes it. Another platform's tool sets
	// are that platform's run's to keep.
	pinned := map[string]bool{}
	for _, r := range shapes {
		if r.shape.sameAs == "" {
			pinned[r.shape.name+".golden"] = true
		}
		if r.shape.tools != "" {
			pinned[filepath.Join("tools", r.shape.tools+"."+runtime.GOOS+".golden")] = true
		}
	}
	goldens, _ := filepath.Glob(testdataPath("whole_prompt", "*.golden"))
	sets, _ := filepath.Glob(testdataPath("whole_prompt", "tools", "*."+runtime.GOOS+".golden"))
	for _, file := range append(goldens, sets...) {
		rel, err := filepath.Rel(testdataPath("whole_prompt"), file)
		switch {
		case err != nil:
			t.Fatal(err)
		case pinned[rel]:
		case *updatePromptGolden:
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		default:
			t.Errorf("%s pins no request shape (an update removes it)", file)
		}
	}
}

// EVERY REQUEST SHAPE KEEPS ITS RECORDED SIZE (prompt_sizes_test.go). A
// size that moved fails, naming the shape and the part; the commit that
// moved it carries the new number (-update-prompt-golden). It reads the
// requests the goldens' test rendered above, when it ran first in this
// test binary.
func TestEveryRequestShapeKeepsItsRecordedSize(t *testing.T) {
	var rows, setRows []sizeRow
	sets := map[string]bool{}
	for _, r := range renderedShapes(t) {
		for _, d := range r.tools {
			if j := toolDefinitionJSON(t, d); r.dir != "" && strings.Contains(j, r.dir) {
				t.Fatalf("shape %s offers %s, whose definition names a place on this machine; its size would move with the machine", r.shape.name, d.Function.Name)
			}
		}
		rows = append(rows, sizeRows(t, r)...)
		if r.shape.tools != "" && !sets[r.shape.tools] {
			sets[r.shape.tools] = true
			setRows = append(setRows, toolSetRows(t, r.shape.tools, r.tools)...)
		}
	}
	rows = append(rows, setRows...) // the tool sets after every shape
	recorded, err := readSizeRows()
	if err != nil && !(*updatePromptGolden && os.IsNotExist(err)) {
		t.Fatalf("%v (write it with -update-prompt-golden)", err)
	}
	if *updatePromptGolden {
		writeGolden(t, promptSizesFile, sizeReport(rows, recorded))
		return
	}
	want := map[string]string{}
	for _, r := range recorded {
		if r.goos == "" || r.goos == runtime.GOOS {
			want[r.key()] = r.values
		}
	}
	for _, r := range rows {
		got, ok := want[r.key()]
		switch {
		case !ok && r.goos != "":
			t.Errorf("%s, %s: no size is recorded for %s (write it on %s with -update-prompt-golden); measured %s", r.subject(), r.part, r.goos, r.goos, r.values)
		case !ok:
			t.Errorf("%s, %s: no size is recorded; measured %s", r.subject(), r.part, r.values)
		case got != r.values:
			t.Errorf("%s, %s%s: recorded %s, measured %s", r.subject(), r.part, goosTag(r.goos), got, r.values)
		}
		delete(want, r.key())
	}
	for _, r := range recorded {
		if _, stale := want[r.key()]; stale {
			t.Errorf("%s, %s%s: a size is recorded for a part no request has now", r.subject(), r.part, goosTag(r.goos))
		}
	}
}

// renderedNamed is the rendered shape of that name.
func renderedNamed(shapes []renderedShape, name string) (renderedShape, bool) {
	for _, r := range shapes {
		if r.shape.name == name {
			return r, true
		}
	}
	return renderedShape{}, false
}

// withoutHead is a golden without its first line, which names the shape.
func withoutHead(golden string) string {
	_, body, _ := strings.Cut(golden, "\n")
	return body
}

func writeGolden(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// promptDiff is the part of two texts that differs, as lines: three lines
// of context, then what the golden holds (-) and what was sent (+). Lines
// both texts share at the start and at the end are the context; whatever
// lies between is shown whole, up to sixty lines a side.
func promptDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	head := 0
	for head < len(w) && head < len(g) && w[head] == g[head] {
		head++
	}
	tail := 0
	for tail < len(w)-head && tail < len(g)-head && w[len(w)-1-tail] == g[len(g)-1-tail] {
		tail++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@@ line %d @@\n", head+1)
	for _, l := range w[max(0, head-3):head] {
		b.WriteString("  " + l + "\n")
	}
	side := func(mark string, lines []string) {
		for i, l := range lines {
			if i == 60 {
				fmt.Fprintf(&b, "%s … %d more lines\n", mark, len(lines)-60)
				return
			}
			b.WriteString(mark + " " + l + "\n")
		}
	}
	side("-", w[head:len(w)-tail])
	side("+", g[head:len(g)-tail])
	for _, l := range w[len(w)-tail : min(len(w), len(w)-tail+3)] {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}
