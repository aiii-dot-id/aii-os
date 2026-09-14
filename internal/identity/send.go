package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

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

	// .
	// .
	// .
	// .
	// .
	// .
	if e.reachable == nil || !e.reachable(to) {
		// .
		// .
		return "", fmt.Errorf("no way to reach %q — no channel is configured for that name; "+
			"ask your operator to add one, or send to your operator", to)
	}
	// .
	// .
	// .
	// .
	msgID := "msg_" + uuid.New().String()
	// .
	// .
	// .
	// .
	if err := e.store.AddOutboxMessage(msgID, "peer", to, message, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Queued for %s. Your next turn will say what became of it.", to), nil
}

// .
// .
func (e *Engine) sendToOperator(message string) (string, error) {
	msgID := "msg_" + uuid.New().String()
	if err := e.store.AddOutboxMessage(msgID, "operator", "", message, nil); err != nil {
		return "", err
	}
	return "Sent to operator.", nil
}
