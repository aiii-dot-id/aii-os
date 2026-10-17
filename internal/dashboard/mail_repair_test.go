package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

func TestHeldMailProtocolCorrelatesReadCorrectionAndRefusal(t *testing.T) {
	requests := make(chan MailRepairRequest, 8)
	s := New("127.0.0.1", 0, &WSHandler{
		HeldMail: func(context.Context, string) (MailRepairState, error) {
			return MailRepairState{Messages: []store.HeldMessage{{ID: "held", SHA256: "view", Reason: "unresolved"}}}, nil
		},
		RepairMail: func(_ context.Context, r MailRepairRequest) error {
			requests <- r
			if r.SHA256 != "view" {
				return fmt.Errorf("stale view")
			}
			return nil
		},
		InspectMail: func(context.Context, string) (MailRepairState, error) {
			return MailRepairState{Messages: []store.HeldMessage{{ID: "held", Preview: "full original text", SHA256: "view"}}}, nil
		},
	})
	s.SetAccessToken(true, "private-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	if c, err := dialWSToken(addr, ""); err == nil {
		c.CloseNow()
		t.Fatal("unauthenticated operator interface accepted")
	}
	c, err := dialWSToken(addr, s.accessHash())
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for i, body := range []string{
		`{"type":"query","query":"held_mail"}`,
		`{"type":"inspect_mail","mail_repair":{"id":"held"}}`,
		`{"type":"repair_mail","mail_repair":{"id":"held","sha256":"view","recipient":"quinn","channel":"signal"}}`,
		`{"type":"repair_mail","mail_repair":{"id":"held","sha256":"old","recipient":"quinn","channel":"signal"}}`,
	} {
		id := fmt.Sprintf("request-%d", i)
		body = strings.TrimSuffix(body, "}") + `,"request_id":"` + id + `"}`
		if err := c.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var reply ServerMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.RequestID != id {
				continue
			}
			if i == 3 {
				if reply.Type != "error" || !strings.Contains(reply.Message, "stale") {
					t.Fatalf("refusal lost: %+v", reply)
				}
			} else {
				if reply.Type != "held_mail" || reply.MailRepair == nil {
					t.Fatalf("uncorrelated view: %+v", reply)
				}
				if i == 1 && reply.MailRepair.Messages[0].Preview != "full original text" {
					t.Fatal("inspection omitted text")
				}
				if i == 2 && reply.MailRepair.Queued != "held" {
					t.Fatal("correction not acknowledged as queued")
				}
			}
			break
		}
	}
	if len(requests) != 2 {
		t.Fatalf("read caused repair or mutation omitted: %d", len(requests))
	}
}
