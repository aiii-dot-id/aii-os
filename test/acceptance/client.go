//go:build acceptance

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/acceptance"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// frameLimit bounds one server frame. The connect-time history and
// interaction frames are the largest a dashboard sends; the library's
// 32 KiB default would end the connection on the first long transcript.
const frameLimit = 64 << 20

// session is one WebSocket connection to the slot's dashboard, used by
// the one goroutine that opened it: every read and write is that
// goroutine's, so nothing here is shared.
type session struct {
	conn   *websocket.Conn
	probes int
}

// connect signs in and opens the dashboard socket the way the page does:
// the access token is POSTed to the slot's own /auth/token, which sets
// the verifier cookie, and the socket is opened with the page's Origin
// and interaction protocol version. The token travels in that one
// request body and is never printed.
func connect(ctx context.Context, t target) (*session, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar}
	if t.token != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.origin+"/auth/token", strings.NewReader(t.token))
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sign in to the slot's dashboard: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return nil, fmt.Errorf("the slot's dashboard refused its own access token (HTTP %d)", resp.StatusCode)
		}
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(t.origin, "http")+"/ws?interaction_version=1", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Origin": []string{t.origin}},
	})
	if err != nil {
		return nil, fmt.Errorf("open the dashboard socket: %w", err)
	}
	conn.SetReadLimit(frameLimit)
	return &session{conn: conn}, nil
}

func (s *session) close() { s.conn.Close(websocket.StatusNormalClosure, "") }

func (s *session) send(ctx context.Context, m dashboard.ClientMessage) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, websocket.MessageText, b)
}

func (s *session) read(ctx context.Context) (dashboard.ServerMessage, error) {
	for {
		typ, b, err := s.conn.Read(ctx)
		if err != nil {
			return dashboard.ServerMessage{}, err
		}
		if typ != websocket.MessageText {
			continue // audio for a speaking page; the driver has no speaker
		}
		var m dashboard.ServerMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return dashboard.ServerMessage{}, fmt.Errorf("unreadable dashboard frame: %w", err)
		}
		return m, nil
	}
}

// chat sends the input as the operator and waits for the dashboard to
// say what became of it: recorded (a receipt with a reference and no
// error), refused, or — when the host accepted it and could not confirm
// the record, or ctx ended first — unconfirmed. It returns an error only
// when the socket failed before ctx did.
func (s *session) chat(ctx context.Context, id, text string) (string, error) {
	if err := s.send(ctx, dashboard.ClientMessage{Type: "chat", Message: text, RequestID: id}); err != nil {
		return transportOr(ctx, err)
	}
	var ack string
	var receipt *dashboard.ChatRecording
	for {
		m, err := s.read(ctx)
		if err != nil {
			return transportOr(ctx, err)
		}
		if m.RequestID != id {
			continue
		}
		switch m.Type {
		case "chat_ack":
			ack = m.Message
		case "chat_receipt":
			receipt = m.ChatRecording
			if receipt == nil {
				receipt = &dashboard.ChatRecording{}
			}
		case "error":
			return acceptance.Refused, nil // a refusal addressed to this submission alone
		}
		switch {
		case ack == "refused":
			return acceptance.Refused, nil
		case receipt != nil && receipt.Error == nil && receipt.Ref != nil:
			return acceptance.Recorded, nil
		case receipt != nil && ack == "accepted":
			return acceptance.Unconfirmed, nil
		}
	}
}

// transportOr is unconfirmed when the wait ran out, and the error when
// the socket failed on its own.
func transportOr(ctx context.Context, err error) (string, error) {
	if ctx.Err() != nil {
		return acceptance.Unconfirmed, nil
	}
	return "", err
}

// turnActive asks the dashboard whether a turn is running, with the
// same foreground probe a page sends when it returns from the
// background. A probe is not an operator act.
func (s *session) turnActive(ctx context.Context) (bool, error) {
	s.probes++
	id := "probe-" + strconv.Itoa(s.probes)
	if err := s.send(ctx, dashboard.ClientMessage{Type: "probe", RequestID: id}); err != nil {
		return false, err
	}
	for {
		m, err := s.read(ctx)
		if err != nil {
			return false, err
		}
		if m.Type == "probe" && m.RequestID == id {
			return m.TurnActive, nil
		}
	}
}
