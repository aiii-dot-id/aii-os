package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
)

func (e *Engine) verbTools(ctx context.Context, args map[string]any) (string, error) {
	depth := 2
	if d, ok := args["depth"].(int); ok {
		depth = d
	}
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	plane, _ := e.toolDisc.(ToolPlane)

	switch action {
	case "", "organs":
	case "brief":
		if plane == nil {
			return "No plugin operations are installed beside you.", nil
		}
		return renderToolBrief(plane.Brief()), nil
	case "search":
		query := strings.TrimSpace(stringArg(args, "query"))
		if query == "" {
			return "", fmt.Errorf("tools action=search needs a query: what you need, in your own words")
		}
		if plane == nil {
			return "No plugin operations are installed beside you.", nil
		}
		return renderToolHits(query, plane.Search(query, 0)), nil
	case "show":
		name := strings.TrimSpace(stringArg(args, "name"))
		if name == "" {
			return "", fmt.Errorf("tools action=show needs a name: the operation id, or its tool name")
		}
		if plane == nil {
			return "", fmt.Errorf("no plugin operations are installed beside you")
		}
		card, err := plane.Show(name)
		if err != nil {
			return "", err
		}
		return ToolCardText(card, listed(ctx, card)), nil
	case "call":
		return "", fmt.Errorf("tools action=call runs a plugin operation, and no plugin host is beside you to run it")
	default:
		return "", fmt.Errorf("tools: unknown action %q — organs, brief, search, show, call", action)
	}

	var lines []string
	if depth >= 2 {
		lines = append(lines, "Your organs (these are you, not tools):")
	} else {
		lines = append(lines, "Organs:")
	}
	for _, v := range Verbs() {
		if depth >= 2 {
			lines = append(lines, fmt.Sprintf("  %s — %s", v.Name, v.Description))
		} else {
			lines = append(lines, "  "+v.Name)
		}
	}

	if e.toolDisc != nil {
		infos := e.toolDisc.Discover(depth)
		if len(infos) > 0 {
			if depth >= 2 {
				lines = append(lines, "\nTools in your sandbox:")
			} else {
				lines = append(lines, "\nTools:")
			}
			for _, info := range infos {
				if depth >= 2 {
					lines = append(lines, fmt.Sprintf("  %s — %s", info.Name, info.Description))
				} else {
					lines = append(lines, "  "+info.Name)
				}
			}
		}
	}

	if action == "" && plane != nil {
		if b := plane.Brief(); b.Total > 0 || b.Unavailable > 0 {
			lines = append(lines, "", renderToolBrief(b))
		}
	}

	docs := "\nSeeded docs at your root (yours to read and annotate; your edits win):"
	if e.inSafeMode() || (e.toolDisc != nil && !writesFiles(e.toolDisc.Discover(1))) {
		docs = "\nSeeded docs at your root (yours to read):"
	}
	lines = append(lines, docs,
		"  SKILLS.md — what this binary can do, and how to use it well",
		"  METHOD.md — the practice: Occam, First Principles, the six steps, the five gates")

	return fmt.Sprintf("What you can reach (depth %d):\n%s", depth, strings.Join(lines, "\n")), nil
}

func writesFiles(offered []ToolInfo) bool {
	for _, t := range offered {
		switch t.Name {
		case "write", "edit", "shell":
			return true
		}
	}
	return false
}

func stringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func renderToolBrief(b ToolBrief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plugin operations beside you: %d across %d families (%d unavailable).", b.Total, len(b.Families)+b.MoreFamilies, b.Unavailable)
	for _, f := range b.Families {
		fmt.Fprintf(&sb, "\n  %s — %d: %s", f.Name, f.Count, strings.Join(f.Names, ", "))
		if f.More > 0 {
			fmt.Fprintf(&sb, " (+%d more; search to narrow)", f.More)
		}
	}
	if b.MoreFamilies > 0 {
		fmt.Fprintf(&sb, "\n  … and %d more families; search to narrow", b.MoreFamilies)
	}
	sb.WriteString("\nEach family names its primary operations first; they are in your tool list. Next: tools action=search query=<what you need> · action=show name=<operation> · action=call name=<operation> args={…} for one that is not. A plugin's success is what the host's receipt says, never the plugin's own text.")
	return sb.String()
}

func renderToolHits(query string, hits []ToolHit) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No plugin operation matches %q. tools action=brief lists the families installed.", query)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plugin operations matching %q:", query)
	for _, h := range hits {
		fmt.Fprintf(&sb, "\n  %s — %s [%s; plugin %s] → tools action=show name=%s", h.Operation, h.Summary, h.Effects, h.Plugin, h.Operation)
		if h.Primary {
			sb.WriteString(" [primary]")
		}
	}
	return sb.String()
}

func listed(ctx context.Context, c ToolCard) bool {
	if shown, inTurn := conversation.ShownThisTurn(ctx, c.Name); inTurn {
		return shown
	}
	return c.Primary && c.Reason == ""
}

func ToolCardText(c ToolCard, listed bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — tool name `%s`", c.Operation, c.Name)
	switch {
	case c.Reason != "":
		fmt.Fprintf(&sb, " (unavailable: %s)", c.Reason)
	case c.Primary:
		sb.WriteString(" (primary)")
	}
	fmt.Fprintf(&sb, "\nplugin %s %s (%s); family %s; effects %s", c.Plugin, c.Version, c.Tier, c.Family, c.Effects)
	if len(c.Capabilities) > 0 {
		fmt.Fprintf(&sb, "; capabilities %s", strings.Join(c.Capabilities, ", "))
	}
	fmt.Fprintf(&sb, "\nsummary: %s", c.Summary)
	fmt.Fprintf(&sb, "\nreceipt rule: %s", c.Receipt)
	if c.MaxResultBytes > 0 {
		fmt.Fprintf(&sb, "\nresult bound: %d bytes", c.MaxResultBytes)
	}
	if len(c.Examples) > 0 {
		fmt.Fprintf(&sb, "\nexamples: %s", strings.Join(c.Examples, " · "))
	}
	if c.Parameters != nil {
		if raw, err := json.Marshal(c.Parameters); err == nil {
			fmt.Fprintf(&sb, "\narguments (JSON Schema): %s", raw)
		}
	}
	switch {
	case c.Reason != "":
		sb.WriteString("\nIt cannot be called while it is unavailable.")
	case listed:
		sb.WriteString("\nIt is in your tool list: call it by its tool name.")
	default:
		fmt.Fprintf(&sb, "\nTo call it: tools action=call name=%s args={…}, the arguments as above.", c.Name)
	}
	return sb.String()
}
