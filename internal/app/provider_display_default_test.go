package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// The three request shapes, as entries: the vendor API (anthropic), the
// subscription whose credential forces the Responses shape, and a
// chat-completions server, which has no such parameter.
func displayEntries() (anthropic, responses, chat providerEntry) {
	const nowhere = "http://127.0.0.1:1/v1" // nothing answers: a card's probe reaches nobody
	anthropic = providerEntry{Name: "vendor-api", APIType: "anthropic", URL: nowhere}
	// The subscription's shape is named in its credential's options, which
	// the shipped registry lends an entry when the file is loaded.
	responses = providerEntry{Name: "subscription", APIType: "openai", URL: nowhere, Credential: "codex",
		CredentialOptions: map[string]string{"dialect": "chatgpt"}}
	chat = providerEntry{Name: "compatible", APIType: "openai", URL: nowhere}
	return
}

func TestTheShapesOfTheThreeEntries(t *testing.T) {
	a, r, c := displayEntries()
	if entryDialect(a) != llm.DialectAnthropic || entryDialect(r) != llm.DialectResponses || entryDialect(c) != llm.DialectOpenAI {
		t.Fatalf("setup: dialects are %s, %s, %s", entryDialect(a), entryDialect(r), entryDialect(c))
	}
}

func TestTheCardShowsWhatARequestWillCarry(t *testing.T) {
	anthropic, responses, chat := displayEntries()
	with := func(e providerEntry, word string) providerEntry { e.ThinkingDisplay = word; return e }
	for _, tc := range []struct {
		name string
		e    providerEntry
		want string
	}{
		{"anthropic, the entry says nothing", anthropic, "summarized"},
		{"anthropic, omitted", with(anthropic, llm.SummaryOmitted), ""},
		{"anthropic, a level", with(anthropic, "summarized"), "summarized"},
		{"anthropic, the other dialect's level", with(anthropic, "detailed"), "summarized"},
		{"anthropic, a word it does not take", with(anthropic, "verbose"), ""},
		{"responses, the entry says nothing", responses, "auto"},
		{"responses, omitted", with(responses, llm.SummaryOmitted), ""},
		{"responses, a level", with(responses, "detailed"), "detailed"},
		{"responses, the operator's old word", with(responses, "summarized"), "auto"},
		{"chat completions, the entry says nothing", chat, ""},
		{"chat completions, a stray word is shown as it stands", with(chat, "summarized"), "summarized"},
	} {
		if got := displayOnCard(tc.e); got != tc.want {
			t.Errorf("%s: the card shows %q, want %q", tc.name, got, tc.want)
		}
		// What the card shows is what the client is planned to send.
		if llm.ReasoningSummaryField(entryDialect(tc.e)) != "" {
			plan := llm.PlanReasoningSummary(entryDialect(tc.e), tc.e.ThinkingDisplay)
			if (plan.Sent && plan.Wire != displayOnCard(tc.e)) || (!plan.Sent && displayOnCard(tc.e) != "") {
				t.Errorf("%s: the card shows %q and the request carries sent=%v %q", tc.name, displayOnCard(tc.e), plan.Sent, plan.Wire)
			}
		}
	}
	// And the card as the dashboard is handed it.
	infos := firstbootApp(t, t.TempDir()).providerInfos(&providerRegistry{Providers: []providerEntry{anthropic, with(responses, llm.SummaryOmitted), chat}})
	if len(infos) != 3 {
		t.Fatalf("three cards expected, got %d", len(infos))
	}
	if infos[0].ThinkingDisplay != "summarized" || infos[0].SummaryField != "thinking.display" {
		t.Errorf("the anthropic card: display %q field %q", infos[0].ThinkingDisplay, infos[0].SummaryField)
	}
	if infos[1].ThinkingDisplay != "" || infos[1].SummaryField != "reasoning.summary" {
		t.Errorf("the subscription card turned off: display %q field %q", infos[1].ThinkingDisplay, infos[1].SummaryField)
	}
	if infos[2].ThinkingDisplay != "" || infos[2].SummaryField != "" {
		t.Errorf("the chat-completions card: display %q field %q", infos[2].ThinkingDisplay, infos[2].SummaryField)
	}
}

func TestASaveFromTheCardStoresWhatTheOperatorChose(t *testing.T) {
	anthropic, responses, chat := displayEntries()
	stored := func(a *App, name string) string {
		t.Helper()
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		e := entryNamed(reg, name)
		if e == nil {
			t.Fatalf("no entry %q", name)
		}
		return e.ThinkingDisplay
	}
	// The card sends no credential options: they are file-only.
	save := func(a *App, e providerEntry, shown string) {
		t.Helper()
		if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
			Name: e.Name, APIType: e.APIType, Endpoint: e.URL, Credential: e.Credential, ThinkingDisplay: shown,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		e      providerEntry
		before string // the entry's stored word
		shown  string // what the card's selector sends
		after  string // the entry's stored word after the save
	}{
		// A save that left the selector where the entry put it changes nothing.
		{"anthropic: untouched default", anthropic, "", "summarized", ""},
		{"anthropic: untouched omitted", anthropic, llm.SummaryOmitted, "", llm.SummaryOmitted},
		{"anthropic: untouched explicit level", anthropic, "summarized", "summarized", "summarized"},
		{"responses: untouched default", responses, "", "auto", ""},
		{"responses: untouched old word", responses, "summarized", "auto", "summarized"},
		// The operator turns it off: the card's empty option is "omitted", said outright.
		{"anthropic: turned off", anthropic, "", "", llm.SummaryOmitted},
		{"responses: turned off", responses, "detailed", "", llm.SummaryOmitted},
		// The operator turns it back on, or picks a level.
		{"anthropic: turned back on", anthropic, llm.SummaryOmitted, "summarized", ""},
		{"responses: back to the default", responses, llm.SummaryOmitted, "auto", ""},
		{"responses: a level chosen", responses, "", "detailed", "detailed"},
		// A dialect with no such parameter keeps the card's word as it came.
		{"chat completions: nothing", chat, "", "", ""},
		{"chat completions: a stray word", chat, "summarized", "summarized", "summarized"},
		{"chat completions: the hidden field's echo is not a choice", chat, "summarized", "", ""},
	} {
		a := newProvidersApp(t)
		e := tc.e
		e.ThinkingDisplay = tc.before
		if err := a.setProvider(e, false); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := stored(a, e.Name); got != tc.before {
			t.Fatalf("%s: setup: a file edit stored %q, want %q", tc.name, got, tc.before)
		}
		save(a, e, tc.shown)
		if got := stored(a, e.Name); got != tc.after {
			t.Errorf("%s: the entry holds %q after the card sent %q, want %q", tc.name, got, tc.shown, tc.after)
		}
	}
	// The card shows no selector for an entry that does not exist yet, so
	// what it sends for a new one is a hidden field's echo and not a
	// choice: the new entry says nothing, and asks by default.
	// A credential is attached from the card only at the address it is for
	// (row 4.4), so the subscription is added there.
	codexHome := shippedEntry(t, "ChatGPT (Plus/Pro)").URL
	for _, fresh := range []providerEntry{anthropic, {Name: "subscription", APIType: "openai", URL: codexHome, Credential: "codex"}} {
		a := newProvidersApp(t)
		fresh.Name = "added-from-the-card"
		save(a, fresh, "")
		if got := stored(a, fresh.Name); got != "" {
			t.Errorf("a new %s entry from the card holds %q; it chose nothing and must say nothing", fresh.APIType, got)
		}
	}
	// The same when a save changes an entry's shape to one that has the
	// parameter: the card it was drawn from showed no selector.
	a := newProvidersApp(t)
	if err := a.setProvider(chat, false); err != nil {
		t.Fatal(err)
	}
	becameAnthropic := chat
	becameAnthropic.APIType = "anthropic"
	save(a, becameAnthropic, "")
	if got := stored(a, chat.Name); got != "" {
		t.Errorf("an entry that became anthropic in this save holds %q; its card showed no selector", got)
	}
	// And when a save changes the shape and the selector together, the
	// level chosen is judged in the NEW shape, whose vendor facts the card
	// does not send: the Responses default is that shape's "auto".
	a = newProvidersApp(t)
	if err := a.setProvider(anthropic, false); err != nil {
		t.Fatal(err)
	}
	becameSubscription := providerEntry{Name: anthropic.Name, APIType: "openai", URL: codexHome, Credential: "codex"}
	save(a, becameSubscription, "auto")
	if got := stored(a, anthropic.Name); got != "" {
		t.Errorf("the new shape's default level, chosen on the card, is stored as %q; the default is the entry's empty", got)
	}
}

// The entry's word reaches the client untouched: the default is the
// client's to apply (llm.PlanReasoningSummary), in one place.
func TestTheEntrysWordReachesTheClientAsItStands(t *testing.T) {
	for _, word := range []string{"", llm.SummaryOmitted, "summarized"} {
		dir := t.TempDir()
		entry := providerEntry{Name: "vendor-api", APIType: "anthropic", URL: "https://example.test/v1", APIKey: "sk-test",
			DefaultModel: "m", Models: []string{"m"}, Default: true, ThinkingDisplay: word}
		writeTestProviders(t, dir, entry)
		a := New(&Config{LLM: LLMConfig{Provider: entry.Name, Model: "m"}, SourcePath: dir + "/config.json"})
		cc, _, err := a.resolveLLM()
		if err != nil {
			t.Fatalf("entry %q: %v", word, err)
		}
		if cc.ThinkingDisplay != word {
			t.Errorf("entry %q reached the client as %q", word, cc.ThinkingDisplay)
		}
	}
}
