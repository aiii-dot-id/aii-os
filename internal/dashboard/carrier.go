package dashboard

import (
	"context"

	"github.com/coder/websocket"
)

type carrier interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)

	Write(ctx context.Context, typ websocket.MessageType, p []byte) error

	Close(code websocket.StatusCode, reason string) error

	CloseNow() error
}

var _ carrier = (*websocket.Conn)(nil)
