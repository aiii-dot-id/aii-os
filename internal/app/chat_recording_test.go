package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

func TestChatReceiptFollowsActualAppAdmissionAndRecording(t *testing.T) {
	for _, path := range []string{"normal", "safe", "failed", "steered", "facility", "leftover-refused", "shutdown-steer"} {
		t.Run(path, func(t *testing.T) {
			st, err := store.NewMemory()
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			a := newSteerApp(t)
			a.store, a.engine = st, identity.NewEngine(st, nil, nil, nil, identity.Ports{})
			a.keyPair, err = crypto.GenerateKeyPair()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { a.bgCancel(); a.bgWG.Wait() }()
			if path == "safe" {
				a.engine.SetSafeMode("fixture")
			}
			if path == "failed" {
				st.SetFrozen(true, "fixture")
			}
			h := &dashboard.WSHandler{Speaker: "identity", AdmitChat: a.admitOperatorChat, ReleaseTurn: a.EndTurn, AcquireTurn: a.acquireTurn, PendingSteers: a.PendingSteers,
				HandleMessage: func(context.Context, string) (string, error) { return "", nil },
				ObserveChat: func(ctx context.Context, text string, _ func(string, string, string)) (string, error) {
					defer a.EndTurn()
					_, err := a.recordConversationRef(ctx, "operator", text)
					return "fixture response", err
				},
			}
			s := dashboard.New("127.0.0.1", 0, h)
			if _, err := s.Start(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown(context.Background())
			conn := dialWSS(t, s.Origin())
			held := path == "steered" || path == "facility" || path == "leftover-refused" || path == "shutdown-steer"
			if held {
				if path == "facility" {
					if !(facilityGate{a}).TryBeginTurn() {
						t.Fatal("no facility claim")
					}
				} else if !a.TryBeginTurn() {
					t.Fatal("no turn claim")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, _ := json.Marshal(dashboard.ClientMessage{Type: "chat", RequestID: "input-1", Message: "same words"})
			if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
				t.Fatal(err)
			}
			if held {
				ack := readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "chat_ack" })
				if ack.Message != "accepted" {
					t.Fatalf("not admitted: %+v", ack)
				}
				if path == "steered" {
					a.DrainSteering()
				}
				if path == "shutdown-steer" {
					a.bgMu.Lock()
					a.stopping = true
					a.bgMu.Unlock()
				}
				a.EndTurn()
			}
			msg := readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "chat_receipt" })
			r := msg.ChatRecording
			if msg.RequestID != "input-1" || r == nil {
				t.Fatalf("receipt lost: %+v", msg)
			}
			if path == "leftover-refused" || path == "shutdown-steer" {
				if r.Ref != nil || r.Error == nil || r.Error.Code != dashboard.ChatNotRecordedCode {
					t.Fatalf("false recording: %+v", r)
				}
				return
			}
			if r.Ref == nil || r.Ref.Identity != a.keyPair.Fingerprint() {
				t.Fatalf("wrong identity: %+v", r)
			}
			if (r.Error != nil) != (path == "failed") {
				t.Fatalf("wrong failure: %+v", r)
			}
			p, err := a.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: r.Ref.Source})
			if err != nil || len(p.Rows) != 1 || p.Rows[0].ID != r.Ref.ID || p.Incarnation != r.Ref.Incarnation || p.Rows[0].Role != interaction.Operator || p.Rows[0].Content != "same words" {
				t.Fatalf("receipt did not name the real recorded words: %+v %+v %v", r, p, err)
			}
			if path == "safe" || path == "failed" {
				if r.Ref.Source != "transient" {
					t.Fatal("claimed durability")
				}
			} else if r.Ref.Source != "recorded" {
				t.Fatal("lost durable reference")
			}
		})
	}
}
