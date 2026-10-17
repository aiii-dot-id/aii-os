package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const pubsubScope = "https://www.googleapis.com/auth/pubsub"

func TestTheGoogleTemplateGainsPubSubAndLosesNothing(t *testing.T) {
	g, ok := embeddedRegistry().OAuth["google"]
	if !ok {
		t.Fatal("no google template")
	}
	want := map[string][2][]string{
		"calendar": {{"https://www.googleapis.com/auth/calendar.readonly"}, {"https://www.googleapis.com/auth/calendar.events"}},
		"contacts": {{"https://www.googleapis.com/auth/contacts.readonly"}, {"https://www.googleapis.com/auth/contacts"}},
		"drive":    {{"https://www.googleapis.com/auth/drive.readonly"}, {"https://www.googleapis.com/auth/drive.file"}},
		"gmail":    {{"https://www.googleapis.com/auth/gmail.readonly"}, {"https://www.googleapis.com/auth/gmail.modify", "https://www.googleapis.com/auth/gmail.send"}},
		"mail":     {{"https://mail.google.com/"}, {"https://mail.google.com/"}},
		"pubsub":   {nil, {pubsubScope}},
	}
	if len(g.Scopes) != len(want) {
		t.Fatalf("the services are %d, want %d", len(g.Scopes), len(want))
	}
	for svc, w := range want {
		got := g.Scopes[svc]
		if !slices.Equal(got.Read, w[0]) || !slices.Equal(got.Modify, w[1]) {
			t.Errorf("service %s: read %v modify %v, want %v", svc, got.Read, got.Modify, w)
		}
	}
	wantHosts := []string{"www.googleapis.com:443", "gmail.googleapis.com:443", "people.googleapis.com:443", "imap.gmail.com:993", "smtp.gmail.com:465", "pubsub.googleapis.com:443"}
	if !slices.Equal(g.Hosts, wantHosts) {
		t.Errorf("hosts %v, want %v", g.Hosts, wantHosts)
	}
	for _, svc := range []string{"gmail", "calendar", "contacts", "mail", "pubsub"} {
		if !slices.Contains(g.DeviceScopesUnsupported, svc) {
			t.Errorf("the device flow is offered for %s", svc)
		}
	}
	if deviceExcluded(g, []string{pubsubScope}) != "pubsub" || deviceExcluded(g, want["drive"][1]) != "" || deviceExcluded(g, want["gmail"][0]) != "gmail" {
		t.Errorf("device exclusion: pubsub %q drive %q gmail %q", deviceExcluded(g, []string{pubsubScope}), deviceExcluded(g, want["drive"][1]), deviceExcluded(g, want["gmail"][0]))
	}
	// Pub/Sub has no read-only consent: its modify scopes carry it, and only they.
	if !servesService(g.Scopes["pubsub"], []string{pubsubScope}) || servesService(g.Scopes["pubsub"], want["gmail"][1]) {
		t.Error("pubsub is served by its scope alone")
	}
}

// What was consented stays what was consented. A profile made before Pub/Sub
// existed keeps its scopes — it is not a Pub/Sub account — while, as a
// template follower, it may now be sent to the new host if an operator grants
// a plugin that host and that handle; one that named its own hosts is
// unchanged. A plugin that asks only for Gmail is offered such a profile as
// before; one that also asks for Pub/Sub is told to add it.
func TestAProfileMadeBeforePubSubKeepsWhatItConsentedTo(t *testing.T) {
	catalog := embeddedRegistry().OAuth
	old := broker.AuthProfile{Scheme: broker.SchemeOAuth2, Provider: "google", ClientID: "cid", TokenFile: "/x.json",
		Scopes: []string{"https://www.googleapis.com/auth/gmail.modify", "https://www.googleapis.com/auth/gmail.send", "https://www.googleapis.com/auth/gmail.readonly"}}
	narrow := old
	narrow.Hosts = []string{"www.googleapis.com:443"}

	if _, tpl, err := old.Contract(catalog); err != nil || !slices.Contains(tpl.Hosts, "pubsub.googleapis.com:443") {
		t.Fatalf("a template follower now rides to the new host too: %v %v", tpl.Hosts, err)
	}
	if _, tpl, err := narrow.Contract(catalog); err != nil || slices.Contains(tpl.Hosts, "pubsub.googleapis.com:443") {
		t.Fatalf("a profile with its own hosts is unchanged: %v %v", tpl.Hosts, err)
	}
	if slices.Contains(old.Scopes, pubsubScope) {
		t.Fatal("the fixture claims a scope it was not given")
	}
	gmailOnly := pluginhost.SettingOAuthHint{Provider: "google", Services: []string{"gmail"}}
	both := pluginhost.SettingOAuthHint{Provider: "google", Services: []string{"gmail", "pubsub"}}
	host := []string{"gmail.googleapis.com:443"}
	if err := accountFit("old", old, gmailOnly, host, catalog); err != nil {
		t.Errorf("a Gmail plugin is offered the old profile as before: %v", err)
	}
	if err := accountFit("old", old, both, host, catalog); err == nil || !strings.Contains(err.Error(), "old was not set up for pubsub") {
		t.Errorf("a plugin that also asks for Pub/Sub is told to add it: %v", err)
	}
	// And a profile that has it is offered to both.
	fresh := old
	fresh.Scopes = append(append([]string(nil), old.Scopes...), pubsubScope)
	if err := accountFit("fresh", fresh, both, []string{"gmail.googleapis.com:443", "pubsub.googleapis.com:443"}, catalog); err != nil {
		t.Errorf("a Pub/Sub account rides to both hosts: %v", err)
	}
	if err := accountFit("narrow", narrow, both, []string{"gmail.googleapis.com:443", "pubsub.googleapis.com:443"}, catalog); err == nil {
		t.Error("a narrowed profile does not ride to Pub/Sub")
	}
}

// The New profile door: Pub/Sub is chosen as modify — the only consent it has —
// and "read" is refused in words rather than granting nothing; every service
// that has a read consent still takes it.
func TestAPubSubAccountIsMadeAsModifyAndReadIsRefused(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	err := a.SetAuthProfile(dashboard.AuthProfileEdit{Name: "google-ps", Provider: "google", ClientID: "cid", ClientSecret: "s", Services: map[string]string{"gmail": "modify", "pubsub": "read"}})
	if err == nil || !strings.Contains(err.Error(), "google has no read-only consent for pubsub; choose modify, or no") {
		t.Fatalf("read on Pub/Sub: %v", err)
	}
	if _, listed := a.configSnapshot().Plugins.AuthProfiles["google-ps"]; listed {
		t.Fatal("the refusal wrote a profile")
	}
	if err := a.SetAuthProfile(dashboard.AuthProfileEdit{Name: "google-ps", Provider: "google", ClientID: "cid", ClientSecret: "s", Services: map[string]string{"gmail": "read", "pubsub": "modify"}}); err != nil {
		t.Fatal(err)
	}
	p := a.configSnapshot().Plugins.AuthProfiles["google-ps"]
	if !slices.Contains(p.Scopes, pubsubScope) || !slices.Contains(p.Scopes, "https://www.googleapis.com/auth/gmail.readonly") || slices.Contains(p.Scopes, "https://www.googleapis.com/auth/gmail.modify") || len(p.Hosts) != 0 {
		t.Fatalf("the profile: %+v", p)
	}
	if v := a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}); len(v) != 1 || v[0].CanDevice {
		t.Fatalf("a Pub/Sub account is not offered the device flow: %+v", v)
	}
}
