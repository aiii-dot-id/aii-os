package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .

func (e *Engine) verbTools(_ context.Context, args map[string]interface{}) (string, error) {
	depth := 2
	if d, ok := args["depth"].(int); ok {
		depth = d
	}
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	plane, _ := e.toolDisc.(ToolPlane)

	// .
	// .
	// .
	// .
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
		return renderToolCard(card), nil
	case "offer":
		name := strings.TrimSpace(stringArg(args, "name"))
		if name == "" {
			return "", fmt.Errorf("tools action=offer needs a name: the operation id, or its tool name")
		}
		if plane == nil {
			return "", fmt.Errorf("no plugin operations are installed beside you")
		}
		resolved, err := plane.Offer(name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Offered: %s is callable from your next turn as `%s`, and stays offered — across restarts too — until you release it (tools action=release) or it changes what it declares; the offer holds eight.", name, resolved), nil
	case "release":
		name := strings.TrimSpace(stringArg(args, "name"))
		if name == "" {
			return "", fmt.Errorf("tools action=release needs a name")
		}
		if plane == nil {
			return "", fmt.Errorf("no plugin operations are installed beside you")
		}
		resolved, err := plane.Release(name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Released: %s (%s) is inspect-only again.", name, resolved), nil
	default:
		return "", fmt.Errorf("tools: unknown action %q — organs, brief, search, show, offer, release", action)
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

	// .
	// .
	// .
	if action == "" && plane != nil {
		if b := plane.Brief(); b.Total > 0 || b.Unavailable > 0 {
			lines = append(lines, "", renderToolBrief(b))
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	lines = append(lines, "\nSeeded docs at your root (yours to read and annotate; your edits win):",
		"  SKILLS.md — what this binary can do, and how to use it well",
		"  METHOD.md — the practice: Occam, First Principles, the six steps, the five gates")

	return fmt.Sprintf("What you can reach (depth %d):\n%s", depth, strings.Join(lines, "\n")), nil
}

func stringArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

// .
// .
func renderToolBrief(b ToolBrief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plugin operations beside you: %d across %d families (%d offered, %d unavailable).", b.Total, len(b.Families)+b.MoreFamilies, b.Offered, b.Unavailable)
	for _, f := range b.Families {
		fmt.Fprintf(&sb, "\n  %s — %d: %s", f.Name, f.Count, strings.Join(f.Names, ", "))
		if f.More > 0 {
			fmt.Fprintf(&sb, " (+%d more; search to narrow)", f.More)
		}
	}
	if b.MoreFamilies > 0 {
		fmt.Fprintf(&sb, "\n  … and %d more families; search to narrow", b.MoreFamilies)
	}
	if len(b.OfferedNames) > 0 {
		fmt.Fprintf(&sb, "\nOffered now, callable by tool name: %s", strings.Join(b.OfferedNames, "; "))
	}
	sb.WriteString("\nNext: tools action=search query=<what you need> · action=show name=<operation> · action=offer name=<operation> (eight seats; release what you no longer need). A plugin's success is what the host's receipt says, never the plugin's own text.")
	return sb.String()
}

// .
func renderToolHits(query string, hits []ToolHit) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No plugin operation matches %q. tools action=brief lists the families installed.", query)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plugin operations matching %q:", query)
	for _, h := range hits {
		fmt.Fprintf(&sb, "\n  %s — %s [%s, %s; plugin %s] → tools action=show name=%s", h.Operation, h.Summary, h.Effects, h.State, h.Plugin, h.Operation)
	}
	return sb.String()
}

// .
func renderToolCard(c ToolCard) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — callable as `%s` (%s", c.Operation, c.Name, c.State)
	if c.Reason != "" {
		fmt.Fprintf(&sb, ": %s", c.Reason)
	}
	sb.WriteString(")")
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
	switch c.State {
	case "offered":
		sb.WriteString("\nIt is in your offer: call it by its tool name.")
	case "hidden":
		sb.WriteString("\nIt cannot be offered while hidden.")
	default:
		fmt.Fprintf(&sb, "\nTo call it: tools action=offer name=%s (eight seats; release one you no longer need).", c.Operation)
	}
	return sb.String()
}

// .
// .
// .
// .
