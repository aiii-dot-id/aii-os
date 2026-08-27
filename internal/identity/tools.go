package identity

import (
	"context"
	"fmt"
	"strings"
)

// --- tools: discovery — three depths (R24) ---
//
// ORGANS FIRST (2026-08-17): the identity's verbs are the identity, not
// attachments (R34) — discovery lists them before anything physical.
// Before this, the verb listed only files/shell and the resident never
// learned commit existed. All surfaces render the canonical
// VerbRegistry (verbregistry.go) — one source of truth.

func (e *Engine) verbTools(_ context.Context, args map[string]interface{}) (string, error) {
	depth := 2 // default
	if d, ok := args["depth"].(int); ok {
		depth = d
	}
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
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

	// THE SEEDED DOCS ARE DISCOVERABLE HERE, in the organ whose job is
	// discovery. SKILLS.md carried this pointer under "Not yet" —
	// "discovery today is the file at your root plus the boot log
	// line" — and the boot line only prints on the boot that seeds or
	// upgrades, so a fresh identity found the docs by luck. This is
	// the wire. Not in the system prompt (a per-turn tax on every
	// identity forever) and never in firstboot (the ceremony is the
	// bootstrap bundle's, only — operator ruling 2026-08-26).
	lines = append(lines, "\nSeeded docs at your root (yours to read and annotate; your edits win):",
		"  SKILLS.md — what this binary can do, and how to use it well",
		"  METHOD.md — the practice: Occam, First Principles, the six steps, the five gates")

	return fmt.Sprintf("What you can reach (depth %d):\n%s", depth, strings.Join(lines, "\n")), nil
}

// RecordConversationTurn records a conversation turn in the store (NOT the ledger).
// Chat turns are not identity events — they are raw input that may or may not
// become identity-bearing through the note verb. The ledger contains what the
// identity adopted; the store contains what was said.
