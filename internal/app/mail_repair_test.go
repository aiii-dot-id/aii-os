package app

import (
	"context"
	"os"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestHeldMailRepairUsesCurrentContactAndQueuesOneCorrection(t *testing.T) {
	a := communicationsApp(t, `[{"name":"quinn","channel":"signal","address":"+1999","wake":false}]`)
	before, err := os.ReadFile(a.cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.heldMail(t.Context(), "")
	if err != nil || len(view.Messages) != 1 {
		t.Fatalf("held mail: %+v %v", view, err)
	}
	m := view.Messages[0]
	req := dashboard.MailRepairRequest{ID: m.ID, SHA256: m.SHA256, Recipient: "quinn", Channel: "signal"}
	wrong := req
	wrong.Channel = "email"
	if err := a.repairMail(t.Context(), wrong); err == nil {
		t.Fatal("unconfigured channel accepted")
	}
	if err := a.repairMail(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := a.repairMail(t.Context(), req); err == nil {
		t.Fatal("the same correction accepted twice")
	}
	after, err := os.ReadFile(a.cfg.SourcePath)
	if err != nil || string(before) != string(after) {
		t.Fatal("message correction rewrote contacts")
	}
	sender := installReasonedAdapter(t, a, "org.example.signal", "signal", "", "")
	row := outboxRow(t, a, m.ID)
	if err := a.deliverOne(context.Background(), row, a.channelRoutes(context.Background())); err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 1 || sender.calls[0] != "+1999|not to be sent" {
		t.Fatalf("wrong destination or body: %v", sender.calls)
	}
}

func TestHeldMailRepairRejectsRevokedContactAndSAFE(t *testing.T) {
	a := communicationsApp(t, `[{"name":"quinn","channel":"signal","address":"+1999"}]`)
	view, err := a.heldMail(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	m := view.Messages[0]
	req := dashboard.MailRepairRequest{ID: m.ID, SHA256: m.SHA256, Recipient: "quinn", Channel: "signal"}
	a.cfg.Contacts = nil
	if err := a.repairMail(t.Context(), req); err == nil {
		t.Fatal("revoked contact accepted from stale page")
	}
	contact(t, a, Contact{Name: "quinn", Channel: "signal", Address: "+1999"})
	a.store.SetFrozen(true, "test integrity refusal")
	if err := a.repairMail(t.Context(), req); err == nil {
		t.Fatal("SAFE store changed")
	}
	row := outboxRow(t, a, m.ID)
	if row.RecipientError == "" || row.ToIdentity != "unknown" {
		t.Fatalf("refusal changed held mail: %+v", row)
	}
}
