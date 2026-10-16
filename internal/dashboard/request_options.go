package dashboard

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type requestIDKey struct{}
type structuredOutputKey struct{}

func requestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }

func requestFailureCode(ctx context.Context, err error) string {
	if requestID(ctx) == "" {
		return ""
	}
	var contract *llm.ResponseFormatError
	switch {
	case errors.As(err, &contract):
		return contract.Code
	case errors.Is(err, context.Canceled):
		return "REQUEST_CANCELLED"
	}
	return "REQUEST_NOT_COMPLETED"
}

func ChatStructuredOutput(ctx context.Context, output json.RawMessage) {
	if publish, ok := ctx.Value(structuredOutputKey{}).(func(json.RawMessage)); ok {
		publish(output)
	}
}
func (s *Server) sendRequestError(ctx context.Context, conn carrier, id, code, message string) {
	s.sendMsg(ctx, conn, ServerMessage{Type: "error", RequestID: id, RequestErrorCode: code, Message: message})
}
