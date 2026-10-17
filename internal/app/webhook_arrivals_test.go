package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// installPush registers a push channel adapter serving the channel "mail": a
// token-verified webhook whose operation answers what its out field says,
// and a describe that says what describe says. It returns the operation, to
// change its answer between calls, and every argument the host handed it.
func installPush(t *testing.T, a *App, id, describe string) (*fakeOp, *[]string) {
	t.Helper()
	given := &[]string{}
	decl := pluginhost.WebhookDecl{Path: "notify", Operation: "mail.notify",
		Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: "X-T", SecretSetting: "tok"}}
	op := &fakeOp{name: pluginhost.ToolNameFor(id, decl.Operation), allArgs: given, out: "{}"}
	if err := a.toolReg.RegisterDynamic(op, id); err != nil {
		t.Fatal(err)
	}
	desc := "pl_" + id + "_describe"
	if err := a.toolReg.RegisterHostOp(&fakeOp{name: desc, out: describe}, id); err != nil {
		t.Fatal(err)
	}
	addPlugin(a, &pluginhost.ActivePlugin{ID: id, Webhooks: []pluginhost.WebhookDecl{decl},
		Channel: &pluginhost.Channel{PluginID: id, Describe: desc, Send: "pl_" + id + "_send", Receive: "pl_" + id + "_receive"}})
	grantSecret(t, a, id, "tok", "push", "AII_TEST_PUSH_TOKEN", "sekret")
	return op, given
}

func notify(a *App, id string) *httptest.ResponseRecorder {
	return post(a, id, "notify", "{}", map[string]string{"X-T": "sekret"})
}

func arrivals(ids ...string) string {
	var list []string
	for _, id := range ids {
		list = append(list, fmt.Sprintf(`{"id":%q,"from":"mira@example.com","body":"mail %s"}`, id, id))
	}
	return `{"arrivals":[` + strings.Join(list, ",") + `]}`
}

// recordedIn is the recorded list a call was handed, and whether it was handed one.
func recordedIn(t *testing.T, call string) ([]string, bool) {
	t.Helper()
	var args struct {
		Recorded *[]string `json:"recorded"`
	}
	if err := json.Unmarshal([]byte(call), &args); err != nil {
		t.Fatal(err)
	}
	if args.Recorded == nil {
		return nil, false
	}
	return *args.Recorded, true
}

// A push adapter's answer may carry several arrivals — a notification can
// stand for a burst of mail — each recorded once by id, a replay not a second
// message, and the single arrival of the older shape as well.
func TestAWebhookMayAnswerSeveralArrivals(t *testing.T) {
	a := liveApp(t)
	op, _ := installPush(t, a, "com.example.mail", `{"channel":"mail","receive":"webhook"}`)

	op.out = `{"arrival":{"id":"m0","from":"mira@example.com","body":"first"},"arrivals":[{"id":"m1","from":"mira@example.com","body":"second"},{"id":"m2","from":"mira@example.com","body":"third"}]}`
	if rec := notify(a, "com.example.mail"); rec.Code != http.StatusOK {
		t.Fatalf("a webhook answering three arrivals: %d", rec.Code)
	}
	if unseen, _ := a.store.InboundSince(0); len(unseen) != 3 {
		t.Fatalf("three arrivals landed: %+v", unseen)
	}
	notify(a, "com.example.mail")
	if unseen, _ := a.store.InboundSince(0); len(unseen) != 3 {
		t.Fatalf("a replay is not a second message: %d", len(unseen))
	}
}

// An answer over the bound is refused whole, so the sender retries.
func TestAWebhookAnswerOverTheBoundIsRefusedWhole(t *testing.T) {
	a := liveApp(t)
	op, _ := installPush(t, a, "com.example.mail", `{"channel":"mail","receive":"webhook"}`)
	var ids []string
	for i := 0; i <= maxWebhookArrivals; i++ {
		ids = append(ids, fmt.Sprintf("m%d", i))
	}
	op.out = arrivals(ids...)
	if rec := notify(a, "com.example.mail"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d arrivals: %d", len(ids), rec.Code)
	}
	if unseen, _ := a.store.InboundSince(0); len(unseen) != 0 {
		t.Fatalf("nothing lands from an answer refused whole: %d", len(unseen))
	}
	op.out = arrivals(ids[:maxWebhookArrivals]...)
	if rec := notify(a, "com.example.mail"); rec.Code != http.StatusOK {
		t.Fatalf("%d arrivals: %d", maxWebhookArrivals, rec.Code)
	}
}

// A push adapter that says it acknowledges is told, with every webhook call,
// which of its last answer's arrivals the host holds — recorded now, already
// held, or declined for good — and never one the record refused; an empty
// list before it has answered since the host started.
func TestAPushAdapterThatAcknowledgesIsToldWhatWasRecorded(t *testing.T) {
	a := liveApp(t)
	op, given := installPush(t, a, "com.example.mail", `{"channel":"mail","receive":"webhook","acknowledges":true}`)
	call := func(out string) int {
		op.out = out
		return notify(a, "com.example.mail").Code
	}

	if call(arrivals("m1", "m2")) != http.StatusOK {
		t.Fatal("the first answer")
	}
	if got, ok := recordedIn(t, (*given)[0]); !ok || len(got) != 0 {
		t.Errorf("the first call: recorded %v (handed: %v), want an empty list", got, ok)
	}

	a.recordInboundFn = func(rowID, channel, from, body string) (bool, error) {
		if strings.HasSuffix(rowID, "_m3") {
			return false, fmt.Errorf("the record is full")
		}
		return a.store.RecordInbound(rowID, channel, from, body)
	}
	if call(`{"arrivals":[{"id":"m3","from":"mira@example.com","body":"3"},{"id":"m4","from":"mira@example.com","body":"4"},{"id":"x5","from":"","body":"5"}]}`) != http.StatusInternalServerError {
		t.Fatal("an answer with an arrival the record refused is answered 500, so the sender retries")
	}
	if got, _ := recordedIn(t, (*given)[1]); !reflect.DeepEqual(got, []string{"m1", "m2"}) {
		t.Errorf("the second call: recorded %v, want the first answer's [m1 m2]", got)
	}
	call(arrivals("m6"))
	if got, _ := recordedIn(t, (*given)[2]); !reflect.DeepEqual(got, []string{"m4", "x5"}) {
		t.Errorf("the third call: recorded %v, want [m4 x5] — m3 was refused by the record and is not reported", got)
	}
	call(arrivals("m7"))
	if got, _ := recordedIn(t, (*given)[3]); !reflect.DeepEqual(got, []string{"m6"}) {
		t.Errorf("the fourth call: recorded %v, want [m6]: only the previous answer's, not a running total", got)
	}
	// An answer that held nothing — a renewal, a quiet sync — leaves what was
	// held of the last one that did: the adapter has not been told it yet.
	call(`{}`)
	call(`{}`)
	if got, _ := recordedIn(t, (*given)[5]); !reflect.DeepEqual(got, []string{"m7"}) {
		t.Errorf("after an empty answer: recorded %v, want [m7]", got)
	}
}

// An adapter that does not say it acknowledges, and a plugin that is no
// adapter, are never handed the field: their schemas may refuse an argument
// they do not know.
func TestAPushAdapterThatDoesNotAcknowledgeIsNeverToldWhatWasRecorded(t *testing.T) {
	a := liveApp(t)
	op, given := installPush(t, a, "com.example.mail", `{"channel":"mail","receive":"webhook"}`)
	op.out = arrivals("m1")
	notify(a, "com.example.mail")
	notify(a, "com.example.mail")
	for i, call := range *given {
		if _, ok := recordedIn(t, call); ok {
			t.Errorf("call %d was handed recorded: %s", i, call)
		}
	}

	const hook = "org.example.hook"
	decl := pluginhost.WebhookDecl{Path: "sms", Operation: "sms.inbound", Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: "X-T", SecretSetting: "tok"}}
	installWebhookPlugin(t, a, hook, decl, `{"arrival":{"id":"h1","from":"x","body":"y"}}`, "")
	grantSecret(t, a, hook, "tok", "hookh", "AII_TEST_HOOK_TOKEN", "sekret")
	given2 := webhookArgRecorder(t, a, hook, "sms.inbound")
	post(a, hook, "sms", "{}", map[string]string{"X-T": "sekret"})
	if _, ok := recordedIn(t, (*given2)[0]); ok || len(*given2) != 1 {
		t.Errorf("a plugin that is no channel adapter was handed recorded: %v", *given2)
	}
}
