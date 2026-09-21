package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
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

type callIDKey struct{}

// .
// .
func newCallID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func withCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callIDKey{}, id)
}

// .
func CallIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(callIDKey{}).(string)
	return s
}

// .
// .
// .
type rawBody []byte

func (b rawBody) LogValue() slog.Value { return slog.StringValue(string(b)) }

func turnOf(ctx context.Context) string {
	if src, ok := logsink.SourceFrom(ctx); ok {
		return src.Turn
	}
	return ""
}

// .
// .
// .
// .
// .
// .
// .
// .
func tapResponse(ctx context.Context, model string, body io.ReadCloser, on bool) io.ReadCloser {
	if !on || body == nil {
		return body
	}
	return &tappedBody{ReadCloser: body, id: CallIDFrom(ctx), turn: turnOf(ctx), model: model}
}

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
func tapRequest(ctx context.Context, model string, req *http.Request, on bool) {
	if !on || req == nil || req.GetBody == nil {
		return
	}
	rc, err := req.GetBody()
	if err != nil {
		return
	}
	defer rc.Close()
	// .
	// .
	body, err := io.ReadAll(io.LimitReader(rc, logsink.CaptureMax+1))
	if err != nil {
		return
	}
	logsink.Tap(logsink.TapRecord{
		ID: CallIDFrom(ctx), Turn: turnOf(ctx),
		Category: "llm.prompt", Direction: "prompt",
		Model: model, Payload: rawBody(body), Continues: true,
	})
}

// .
// .
// .
// .
// .
const captureCut = "\n…[capture truncated: the answer continued past the capture bound]"

type tappedBody struct {
	io.ReadCloser
	buf   bytes.Buffer
	id    string
	turn  string
	model string
	done  bool
	over  bool
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (t *tappedBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		if room := logsink.CaptureMax - len(captureCut) - t.buf.Len(); room > 0 {
			if n <= room {
				t.buf.Write(p[:n])
			} else {
				t.buf.Write(p[:room])
				t.over = true
			}
		} else {
			t.over = true
		}
	}
	return n, err
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (t *tappedBody) Close() error {
	if !t.done {
		t.done = true
		payload := t.buf.Bytes()
		if t.over {
			payload = append(append([]byte{}, payload...), []byte(captureCut)...)
		}
		logsink.Tap(logsink.TapRecord{
			ID: t.id, Turn: t.turn,
			Category: "llm.return", Direction: "return",
			Model: t.model, Payload: rawBody(payload),
			Continues: true,
		})
	}
	return t.ReadCloser.Close()
}
