package app

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAKeyVariableGoesToTheProviderItIsFor(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("OPENROUTER_API_KEY", "sk-openrouter")
	t.Setenv("AII_TEST_OWN_KEY", "sk-own")
	os.Unsetenv("ANTHROPIC_API_KEY")

	lent := func(name string) providerEntry {
		t.Helper()
		for _, e := range embeddedRegistry().Providers {
			if e.Name == name {
				if e.VendorKeyEnv == "" {
					t.Fatalf("the shipped registry names no key variable for %s", name)
				}
				return providerEntry{Name: e.Name, URL: e.URL, VendorKeyEnv: e.VendorKeyEnv}
			}
		}
		t.Fatalf("no shipped entry named %s", name)
		return providerEntry{}
	}
	for name, c := range map[string]struct {
		entry    providerEntry
		blockEnv string
		want     string
	}{
		"OpenAI, selected with its own variable":                         {lent("OpenAI"), "OPENAI_API_KEY", "sk-openai"},
		"OpenAI, not the selected provider":                              {lent("OpenAI"), "", "sk-openai"},
		"another vendor, selected with the default global":               {lent("Anthropic"), "OPENAI_API_KEY", ""},
		"another vendor, not selected: its own variable":                 {lent("OpenRouter"), "", "sk-openrouter"},
		"another vendor, selected with the default global: its own":      {lent("OpenRouter"), "OPENAI_API_KEY", "sk-openrouter"},
		"a vendor, selected with a variable the operator made for it":    {lent("Anthropic"), "AII_TEST_OWN_KEY", "sk-own"},
		"the operator's own endpoint, selected with their variable":      {providerEntry{Name: "Gateway"}, "AII_TEST_OWN_KEY", "sk-own"},
		"the operator's own endpoint, selected with the default global":  {providerEntry{Name: "Gateway"}, "OPENAI_API_KEY", ""},
		"the operator's own endpoint, not selected":                      {providerEntry{Name: "Gateway"}, "", ""},
		"a local server, not selected":                                   {providerEntry{Name: "Local server", Local: true}, "", ""},
		"an entry that names a variable keeps it over the block's":       {providerEntry{Name: "Gateway", APIKeyEnv: "OPENROUTER_API_KEY"}, "AII_TEST_OWN_KEY", "sk-openrouter"},
		"a stored key is the entry's answer":                             {providerEntry{Name: "Gateway", APIKey: "sk-stored"}, "AII_TEST_OWN_KEY", "sk-stored"},
		"a vendor's variable named by the operator for that same vendor": {providerEntry{Name: "OpenAI", URL: "https://api.openai.com/v1"}, "OPENAI_API_KEY", "sk-openai"},
		"a vendor's name at another address, selected with its variable": {providerEntry{Name: "OpenAI", URL: "https://relay.example/v1"}, "OPENAI_API_KEY", ""},
		"a vendor's name at another address, the variable named on it":   {providerEntry{Name: "OpenAI", URL: "https://relay.example/v1", APIKeyEnv: "OPENAI_API_KEY"}, "", "sk-openai"},
		// The rule is held where the variable is read: a copy of the
		// vendor's entry whose address was put in after the load still
		// carries the variable it was lent, and it is not read there.
		"a copy at another address, the lent variable still on it":       {providerEntry{Name: "OpenAI", URL: "https://relay.example/v1", VendorKeyEnv: "OPENAI_API_KEY"}, "", ""},
		"the same copy, selected with the vendor's variable":             {providerEntry{Name: "OpenAI", URL: "https://relay.example/v1", VendorKeyEnv: "OPENAI_API_KEY"}, "OPENAI_API_KEY", ""},
		"the operator's own naming in that field, wherever the entry is": {providerEntry{Name: "OpenAI", URL: "https://relay.example/v1", VendorKeyEnv: "AII_TEST_OWN_KEY"}, "", "sk-own"},
	} {
		if got := providerAPIKey(c.entry, "", c.blockEnv); got != c.want {
			t.Errorf("%s: resolved %q, want %q", name, got, c.want)
		}
	}
}

// A BLOCK'S VARIABLE IS FOR THE PROVIDER THE BLOCK SELECTS: the named
// one, or the default-flagged entry when the block names none.
func TestABlocksVariableIsForTheProviderItSelects(t *testing.T) {
	a, b := providerEntry{Name: "alpha", Default: true}, providerEntry{Name: "beta"}
	for name, c := range map[string]struct {
		selected, env string
		entry         providerEntry
		want          string
	}{
		"the named provider":                   {"beta", "V", b, "V"},
		"another entry":                        {"beta", "V", a, ""},
		"no provider named: the default entry": {"", "V", a, "V"},
		"no provider named: a non-default one": {"", "V", b, ""},
		"no variable":                          {"beta", "", b, ""},
	} {
		if got := keyEnvFor(c.selected, c.env, c.entry); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// THE FINDING, ON THE WIRE. The default global is exported and OpenAI is
// the selected provider. Every hosted vendor the registry ships is then
// probed, as the status surface probes them, at the address it ships with,
// against a server that records what each request carried
// (recordEveryHost). Only OpenAI's request carries the key; before this
// each of the others carried it too, as a bearer or as the Anthropic
// dialect's header. A role routed to another provider resolves with no
// key of OpenAI's either.
func TestAProbeOfEveryVendorSendsTheGlobalKeyOnlyToItsOwn(t *testing.T) {
	const key = "sk-openai-the-operators-real-key"
	t.Setenv("OPENAI_API_KEY", key)
	rec := recordEveryHost(t)

	// The operator's file as an identity holds it: the shipped vendors by
	// name and address, with no key variable written on any (the scaffold
	// writes none).
	var entries []providerEntry
	host := map[string]string{}
	for _, e := range embeddedRegistry().Providers {
		if e.VendorKeyEnv == "" {
			continue
		}
		os.Unsetenv(e.VendorKeyEnv)
		u, err := url.Parse(e.URL)
		if err != nil {
			t.Fatal(err)
		}
		host[e.Name] = u.Host
		entries = append(entries, providerEntry{Name: e.Name, APIType: e.APIType, URL: e.URL, DefaultModel: "m-1"})
	}
	t.Setenv("OPENAI_API_KEY", key)
	if len(entries) < 9 {
		t.Fatalf("only %d shipped vendors name a key variable", len(entries))
	}
	dir := t.TempDir()
	writeTestProviders(t, dir, entries...)
	a := resolverApp(dir, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"})
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		if _, _, err := a.discoverMetaForEntry(context.Background(), e, ""); err != nil {
			t.Logf("%s: the probe answered %v (what it carried is what is asked)", e.Name, err)
		}
	}
	for name, h := range host {
		asks := rec.to(h)
		if len(asks) == 0 {
			t.Errorf("%s was never probed", name)
			continue
		}
		got := false
		for _, r := range asks {
			got = got || strings.Contains(r.everything, key)
		}
		if want := name == "OpenAI"; got != want {
			t.Errorf("%s: the probe carried the OpenAI key = %v, want %v", name, got, want)
		}
	}

	for _, other := range []string{"Anthropic", "OpenRouter"} {
		cc, _, err := a.resolveRouteLLM(a.configSnapshot(), reg, other, "")
		if err != nil || cc.APIKey != "" {
			t.Errorf("a role routed to %s resolved the key %q (%v); it is not that provider's", other, cc.APIKey, err)
		}
	}
	if cc, _, err := a.resolveLLM(); err != nil || cc.APIKey != key {
		t.Errorf("the selected provider lost its own key: %q, %v", cc.APIKey, err)
	}

	// THE SAME WITH A VARIABLE THAT IS NO VENDOR'S. The operator made one
	// for the provider they selected; it is that provider's and reaches no
	// other entry, on a probe, on a role's route, or through the client
	// built for another entry. Here only the pairing protects: nothing
	// marks this variable as anyone's.
	const own = "sk-the-operators-own-variable"
	t.Setenv("AII_TEST_OWN_KEY", own)
	os.Unsetenv("OPENAI_API_KEY")
	b := resolverApp(dir, LLMConfig{Provider: "OpenAI", APIKeyEnv: "AII_TEST_OWN_KEY"})
	rec.forget()
	reg, err = b.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		_, _, _ = b.discoverMetaForEntry(context.Background(), e, "")
		if e.Name == "OpenAI" {
			continue
		}
		if cc, _, err := b.resolveRouteLLM(b.configSnapshot(), reg, e.Name, ""); err != nil || cc.APIKey != "" {
			t.Errorf("a role routed to %s resolved %q (%v) from the selected provider's variable", e.Name, cc.APIKey, err)
		}
		if cc, err := b.clientConfigForEntry(reg.eff, e, b.configSnapshot().LLM, "m-1", ""); err != nil || cc.APIKey != "" {
			t.Errorf("the client built for %s carries %q (%v) from the selected provider's variable", e.Name, cc.APIKey, err)
		}
	}
	for name, h := range host {
		got := false
		for _, r := range rec.to(h) {
			got = got || strings.Contains(r.credential, own)
		}
		if want := name == "OpenAI"; got != want {
			t.Errorf("%s: the probe carried the operator's own variable = %v, want %v", name, got, want)
		}
	}
}

// THE VARIABLE TO EXPORT IS THE ONE THAT IS READ. The start's line for a
// provider with no key names a variable; it named the block's whatever
// the provider was, and then the block's "as written" for an entry that no
// variable is read for at all: one named for a vendor and standing at
// another address was told to export the vendor's variable, which was set
// and is never read for it there.
//
// Each row is held to its name: with every variable in play set, the one
// the operator is told to export is the one the entry's key is read from,
// and where none is named, none is read and the line says what gives the
// entry a key instead.
func TestTheVariableAnOperatorIsToldToExportIsTheOneRead(t *testing.T) {
	for _, env := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "MINE"} {
		t.Setenv(env, "held-by-"+env)
	}
	const relay = "https://relay.example/v1"
	anthropic, openai := shippedEntry(t, "Anthropic"), shippedEntry(t, "OpenAI")
	named := anthropic
	named.APIKeyEnv = "MINE"
	block := LLMConfig{Provider: "Anthropic", APIKeyEnv: "OPENAI_API_KEY"}
	for name, c := range map[string]struct {
		entry providerEntry
		block LLMConfig
		want  string
	}{
		"a vendor selected with the default global": {anthropic, block, "ANTHROPIC_API_KEY"},
		"an entry that names its own":               {named, block, "MINE"},
		"a vendor selected with the operator's own": {anthropic, LLMConfig{Provider: "Anthropic", APIKeyEnv: "MINE"}, "MINE"},
		"OpenAI selected with its own":              {openai, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"}, "OPENAI_API_KEY"},
		"a vendor's name at another address, selected with the vendor's variable": {
			providerEntry{Name: "OpenAI", URL: relay}, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"}, ""},
		"a copy at another address, the lent variable still on it": {
			providerEntry{Name: "OpenAI", URL: relay, VendorKeyEnv: "OPENAI_API_KEY"}, LLMConfig{Provider: "OpenAI", APIKeyEnv: "OPENAI_API_KEY"}, ""},
		"the operator's own endpoint, selected with the default global": {
			providerEntry{Name: "Gateway", URL: relay}, LLMConfig{Provider: "Gateway", APIKeyEnv: "OPENAI_API_KEY"}, ""},
		"the operator's own endpoint, selected with a variable of its own": {
			providerEntry{Name: "Gateway", URL: relay}, LLMConfig{Provider: "Gateway", APIKeyEnv: "MINE"}, "MINE"},
	} {
		got := keyEnvToExport(c.entry, c.block)
		if got != c.want {
			t.Errorf("%s: told to export %q, want %q", name, got, c.want)
			continue
		}
		read := providerAPIKey(c.entry, "", keyEnvFor(c.block.Provider, c.block.APIKeyEnv, c.entry))
		remedy := keyRemedy(c.entry, c.block)
		if c.want == "" {
			if read != "" || remedy != "name a variable for this entry (api_key_env) in providers.json" {
				t.Errorf("%s: no variable is named for export, yet one is read = %v, and the line says %q", name, read != "", remedy)
			}
			continue
		}
		if read != "held-by-"+c.want || remedy != "export "+c.want {
			t.Errorf("%s: told to %q, and the key is read from that variable = %v", name, remedy, read == "held-by-"+c.want)
		}
	}
}

// THE VENDOR'S VARIABLE IS LENT, NEVER WRITTEN. An operator's file that
// says nothing of it reads with the variable on each shipped vendor's
// entry and none on an entry of the operator's own; writing the registry
// back leaves the file without it; an operator's own value on an entry
// stands; and the file a new identity is given carries none.
func TestTheVendorsKeyVariableIsLentAndNotWritten(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "OpenAI", URL: "https://api.openai.com/v1", DefaultModel: "m", Default: true},
		providerEntry{Name: "Gateway", URL: "https://gateway.example/v1", DefaultModel: "m"},
		providerEntry{Name: "Groq", URL: "https://api.groq.com/openai/v1", DefaultModel: "m", VendorKeyEnv: "MY_GROQ"},
	)
	a := resolverApp(dir, LLMConfig{})
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range reg.Providers {
		got[e.Name] = e.VendorKeyEnv
	}
	if got["OpenAI"] != "OPENAI_API_KEY" || got["Gateway"] != "" || got["Groq"] != "MY_GROQ" {
		t.Fatalf("lent variables: %v", got)
	}
	back, err := json.Marshal(reg.operatorView())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(back), "OPENAI_API_KEY") || !strings.Contains(string(back), "MY_GROQ") {
		t.Fatalf("written back: a lent variable was kept, or the operator's own was dropped: %s", back)
	}
	if strings.Contains(string(scaffoldProviders()), "vendor_key_env") {
		t.Fatal("a new identity's providers.json carries the vendors' key variables")
	}
	if _, err := os.Stat(filepath.Join(dir, "providers.json")); err != nil {
		t.Fatal(err)
	}
}
