package llm

import (
	"context"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

type (
	Message                 = wire.Message
	ThinkingBlock           = wire.ThinkingBlock
	ToolCall                = wire.ToolCall
	ToolDefinition          = wire.ToolDefinition
	ToolFunction            = wire.ToolFunction
	Choice                  = wire.Choice
	Response                = wire.Response
	ChatOptions             = wire.ChatOptions
	Usage                   = wire.Usage
	IncompleteResponseError = wire.IncompleteResponseError
)

func WithModelID(ctx context.Context, modelID string) context.Context {
	return wire.WithModelID(ctx, modelID)
}

func ModelIDFromContext(ctx context.Context) string { return wire.ModelIDFromContext(ctx) }

func FormatToolResult(toolCallID, content string) Message {
	return Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	}
}
