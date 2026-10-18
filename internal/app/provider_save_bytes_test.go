package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestASaveWritesTheOperatorsFileByteForByte(t *testing.T) {
	shippedVersion := shippedEntry(t, "Claude (Max/Pro)").CredentialOptions["client_version"]
	if shippedVersion == "" {
		t.Fatal("the shipped Claude entry names no client_version; the fixture cannot be built")
	}
	cases := []struct{ name, file, want string }{
		{"an operator's own registry, nothing lent", saveOwnFile, saveOwnWant},
		// Every lend at once beside what the operator wrote: the missing
		// Claude options, its formatted values and its OAuth name; ElevenLabs'
		// speech mapping; zAI's catalogue author; the dialect floor lent to
		// an entry nobody ships; the ChatGPT options the file leaves out —
		// with a broken entry kept where it stood.
		{"every lend at once", saveEveryFile, saveEveryWant},
		// The shipped client_version is the shipped default, not a choice.
		{"the shipped version", fmt.Sprintf(`{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code","credential_options":{"client_version":%q},"default_model":"claude-opus-5"}]}`, shippedVersion), saveShippedVersionWant},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "providers.json")
		if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
			t.Fatal(err)
		}
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, err := saveProvidersFile(path, reg); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got, _ := os.ReadFile(path); string(got) != tc.want {
			t.Errorf("%s: the save wrote\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}

	// The same through the one load-modify-save boundary: each edit changes
	// what it says, and nothing lent reaches the file.
	replaced := func(s, old, new string) string {
		t.Helper()
		if strings.Count(s, old) != 1 {
			t.Fatalf("the expected file does not hold %q exactly once", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	mine := `    {
      "name": "Mine",
      "api_type": "openai",
      "url": "https://mine.example/v1",
      "api_key": "k",
      "default_model": "m"
    },
`
	for _, tc := range []struct {
		name string
		edit func(*App) error
		want string
	}{
		{"a card edit", func(a *App) error {
			return a.setProviderFromCard(providerEntry{Name: "zAI", APIType: "openai", URL: "https://api.z.ai/api/paas/v4", DefaultModel: "glm-5.4"}, true, true)
		}, replaced(saveEveryWant, `"glm-5.3"`, `"glm-5.4"`)},
		{"a switch to a credential", func(a *App) error {
			return a.setProviderFromCard(providerEntry{Name: "Mine", APIType: "anthropic", URL: "https://api.anthropic.com", Credential: "claude-code", DefaultModel: "claude-opus-5"}, false, true)
		}, replaced(saveEveryWant, mine, `    {
      "name": "Mine",
      "api_type": "anthropic",
      "url": "https://api.anthropic.com",
      "credential": "claude-code",
      "default_model": "claude-opus-5"
    },
`)},
		{"a removal", func(a *App) error { return a.deleteProvider("Mine") }, replaced(saveEveryWant, mine, "")},
	} {
		a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
		if err := os.WriteFile(a.providersPath(), []byte(saveEveryFile), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := tc.edit(a); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got, _ := os.ReadFile(a.providersPath()); string(got) != tc.want {
			t.Errorf("%s: the save wrote\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}
}

const saveOwnFile = `{"providers":[{"name":"Mine","api_type":"openai","effort_levels":["low"],"url":"https://mine.example/v1","api_key":"k","default_model":"m","default":true}],
"model_capabilities":{"m":{"effort":["low"]}},"dialect_effort_floor":{"openai":["low"]}}`

const saveEveryFile = `{"providers":[
{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code","credential_options":{"client_version":"9.9.9","header_x-app":"mine","billing_text":"the operator's own"},"default_model":"claude-opus-5","default":true},
{"name":"ElevenLabs","url":"https://api.elevenlabs.io","api_key":"el"},
{"name":"broken","url":"https://broken.example","no_such_field":1},
{"name":"zAI","api_type":"openai","url":"https://api.z.ai/api/paas/v4","api_key":"z","default_model":"glm-5.3","effort_levels":["low","high"]},
{"name":"Mine","api_type":"openai","url":"https://mine.example/v1","api_key":"k","default_model":"m"},
{"name":"ChatGPT (Plus/Pro)","api_type":"openai","url":"https://chatgpt.com/backend-api/codex","credential":"codex","oauth":"openai-chatgpt","credential_options":{"base_url":"https://chatgpt.com/backend-api/codex"},"default_model":"gpt-5.5"}],
"model_capabilities":{"m":{"thinking":"enabled"}},"dialect_effort_floor":{"openai":["low","medium"]}}`

const saveOwnWant = `{
  "providers": [
    {
      "name": "Mine",
      "api_type": "openai",
      "effort_levels": [
        "low"
      ],
      "url": "https://mine.example/v1",
      "api_key": "k",
      "default_model": "m",
      "default": true
    }
  ],
  "model_capabilities": {
    "m": {
      "effort": [
        "low"
      ]
    }
  },
  "dialect_effort_floor": {
    "openai": [
      "low"
    ]
  }
}`

const saveEveryWant = `{
  "providers": [
    {
      "name": "Claude (Max/Pro)",
      "api_type": "anthropic",
      "url": "https://api.anthropic.com",
      "credential": "claude-code",
      "credential_options": {
        "client_version": "9.9.9",
        "header_x-app": "mine"
      },
      "default_model": "claude-opus-5",
      "default": true
    },
    {
      "name": "ElevenLabs",
      "api_type": "openai",
      "url": "https://api.elevenlabs.io",
      "api_key": "el"
    },
    {
      "name": "broken",
      "url": "https://broken.example",
      "no_such_field": 1
    },
    {
      "name": "zAI",
      "api_type": "openai",
      "effort_levels": [
        "low",
        "high"
      ],
      "url": "https://api.z.ai/api/paas/v4",
      "api_key": "z",
      "default_model": "glm-5.3"
    },
    {
      "name": "Mine",
      "api_type": "openai",
      "url": "https://mine.example/v1",
      "api_key": "k",
      "default_model": "m"
    },
    {
      "name": "ChatGPT (Plus/Pro)",
      "api_type": "openai",
      "url": "https://chatgpt.com/backend-api/codex",
      "credential": "codex",
      "oauth": "openai-chatgpt",
      "credential_options": {
        "base_url": "https://chatgpt.com/backend-api/codex"
      },
      "default_model": "gpt-5.5"
    }
  ],
  "model_capabilities": {
    "m": {
      "thinking": "enabled",
      "effort": null
    }
  },
  "dialect_effort_floor": {
    "openai": [
      "low",
      "medium"
    ]
  }
}`

const saveShippedVersionWant = `{
  "providers": [
    {
      "name": "Claude (Max/Pro)",
      "api_type": "anthropic",
      "url": "https://api.anthropic.com",
      "credential": "claude-code",
      "default_model": "claude-opus-5"
    }
  ]
}`

// AN EDIT'S CANDIDATE HAS ITS OWN RECORD OF WHAT WAS LENT. Binding the
// candidate's OAuth lends a contract name to an entry the edit just moved
// onto a credential; written into the source's record instead, the loaded
// registry no longer equals a fresh load of the unchanged file, and the
// recheck after a substrate probe refuses the edit as a concurrent change.
func TestAnEditsCandidateLeavesItsSourceAsLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(saveEveryFile), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := candidateRegistry(before, func(r *providerRegistry) error {
		e := entryNamed(r, "Mine")
		e.APIType, e.Credential, e.URL = "anthropic", "claude-code", "https://api.anthropic.com"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if entryNamed(candidate, "Mine").OAuth == "" {
		t.Fatal("the candidate was lent no OAuth contract; the test proves nothing")
	}
	again, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, again) {
		t.Fatal("building an edit's candidate changed the registry it was built from")
	}
}
