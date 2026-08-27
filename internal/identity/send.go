package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// --- send: interpersonal — writes to outbox ---

// verbSend addresses a PERSON, never an address (operator ruling
// 2026-08-24). "james" resolves through the address book, which holds the
// operator's ordering of how to reach them.
//
// Naming a channel ("james on signal") is NOT offered. It was, briefly,
// and nothing could carry it: with no adapter installed there is no
// second channel to choose between, so the surface advertised a judgement
// the system could not act on. AGENTS.md 1.3 — a feature enters whole or
// not at all.
//
// Credentials, protocols and addresses stay with the host. A plugin the
// operator uninstalls must not leave nouns behind in a mind that never
// knew its name.
func (e *Engine) verbSend(_ context.Context, args map[string]interface{}) (string, error) {
	to, _ := args["to"].(string)
	message, _ := args["message"].(string)
	if message == "" {
		message, _ = args["_positional"].(string)
	}
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("send needs something to say")
	}
	to = strings.TrimSpace(to)
	if to == "" || to == "operator" {
		return e.sendToOperator(message)
	}

	// The identity learns ONLY whether a name can be reached, never how.
	// "Where they are, and how to get there, is not yours to know" was a
	// ruling held by convention while this function read the whole
	// address book and used one integer from it. A port that answers
	// yes or no makes the ruling structural: there is no address here to
	// leak, log, or reason about.
	if e.reachable == nil || !e.reachable(to) {
		// Not knowing how to reach someone is an ANSWER. Naming it lets
		// the identity ask rather than guess at a spelling.
		return "", fmt.Errorf("no way to reach %q — no channel is configured for that name; "+
			"ask your operator to add one, or send to your operator", to)
	}
	// The row records WHO, not where. The address resolves at delivery,
	// so someone who changes their number still receives what was
	// queued before they moved — and the operator's ordering, which IS
	// primary and secondary, is applied by whoever carries it.
	msgID := "msg_" + uuid.New().String()
	// send is outbox-only — never mints experience.create (ENTITY_TYPES.md
	// boundary table: "send verb output | Outbox only"). Interpersonal acts
	// are not experiences. If the send turns out to matter to the identity,
	// the resident notes it consciously via note.
	if err := e.store.AddOutboxMessage(msgID, "peer", to, message, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Queued for %s.", to), nil
}

// sendToOperator is the path that always existed: the dashboard carries
// it, and "while you were away" is where it lands.
func (e *Engine) sendToOperator(message string) (string, error) {
	msgID := "msg_" + uuid.New().String()
	if err := e.store.AddOutboxMessage(msgID, "operator", "", message, nil); err != nil {
		return "", err
	}
	return "Sent to operator.", nil
}
