package promptsuite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonOf is v as JSON. A value is never spliced into JSON text: a
// Windows path's backslashes are escapes there (C:\Users read as \U).
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEachModelKindNamesItsCredentialAndNoOther(t *testing.T) {
	cred := filepath.Join(t.TempDir(), "auth.json")
	for name, c := range map[string]struct {
		spec string
		ok   bool
	}{
		"claude by its key's variable":         {`{"kind":"anthropic","model":"claude-opus-5-5","key_env":"ANTHROPIC_API_KEY"}`, true},
		"claude by the subscription's file":    {jsonOf(t, map[string]string{"kind": "claude-subscription", "model": "claude-opus-5-5", "credential_file": cred}), true},
		"chatgpt by its credential's file":     {jsonOf(t, map[string]string{"kind": "chatgpt", "model": "gpt-5.6-sol", "credential_file": cred}), true},
		"gemini by its key's variable":         {`{"kind":"gemini","model":"gemini-3.8-flash","key_env":"GEMINI_API_KEY"}`, true},
		"a local endpoint with no key":         {`{"kind":"openai-compatible","model":"qwen","url":"http://127.0.0.1:1081/v1"}`, true},
		"a local endpoint with a key variable": {`{"kind":"openai-compatible","model":"qwen","url":"http://127.0.0.1:1081/v1","key_env":"LOCAL_KEY"}`, true},
		"a key written into the spec":          {`{"kind":"anthropic","model":"m","key_env":"K","api_key":"sk-written-here"}`, false},
		"a key where a variable's name goes":   {`{"kind":"anthropic","model":"m","key_env":"sk-ant-0123"}`, false},
		"a subscription by a relative path":    {`{"kind":"chatgpt","model":"m","credential_file":"auth.json"}`, false},
		"a subscription by a variable":         {`{"kind":"chatgpt","model":"m","key_env":"K"}`, false},
		"an address carrying a user":           {`{"kind":"openai-compatible","model":"m","url":"http://user:pw@127.0.0.1/v1"}`, false},
		"an address carrying a query":          {`{"kind":"openai-compatible","model":"m","url":"http://127.0.0.1/v1?key=sk"}`, false},
		"an unknown kind":                      {`{"kind":"judge","model":"m","key_env":"K"}`, false},
		"no model":                             {`{"kind":"gemini","key_env":"K"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSpec([]byte(c.spec))
			if (err == nil) != c.ok {
				t.Fatalf("want ok=%v, got %v", c.ok, err)
			}
			if err != nil && strings.Contains(err.Error(), "sk-") {
				t.Fatalf("a refusal repeats what was written where a credential would be: %v", err)
			}
		})
	}
}

func TestASpecsLabelCarriesNoCredentialAndNoNameOfOne(t *testing.T) {
	cred := filepath.Join(t.TempDir(), "codex-auth.json")
	for _, raw := range []string{
		`{"kind":"anthropic","model":"claude-opus-5-5","key_env":"SECRET_HOLDER"}`,
		jsonOf(t, map[string]string{"kind": "chatgpt", "model": "gpt-5.6-sol", "credential_file": cred}),
		`{"kind":"openai-compatible","model":"qwen","url":"http://127.0.0.1:1081/v1","key_env":"SECRET_HOLDER"}`,
	} {
		s, err := ParseSpec([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		label := s.Label()
		for _, leak := range []string{"SECRET_HOLDER", cred, "codex-auth"} {
			if strings.Contains(label, leak) {
				t.Fatalf("the label %q names where a credential is kept (%q)", label, leak)
			}
		}
		if !strings.Contains(label, s.Model) || !strings.HasPrefix(label, s.Kind) {
			t.Fatalf("the label says the kind and the model: %q", label)
		}
	}
}

func TestASpecIsReadyOnlyWhenItsCredentialIsThereAndItsValueIsNeverSaid(t *testing.T) {
	s, err := ParseSpec([]byte(`{"kind":"gemini","model":"g","key_env":"PROMPTSUITE_TEST_KEY"}`))
	if err != nil {
		t.Fatal(err)
	}
	const secret = "sk-promptsuite-sentinel-0123456789"
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if err := s.Ready(getenv); err == nil || !strings.Contains(err.Error(), "PROMPTSUITE_TEST_KEY") {
		t.Fatalf("a variable that is not set is named: %v", err)
	}
	env["PROMPTSUITE_TEST_KEY"] = secret
	if err := s.Ready(getenv); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "auth.json")
	sub, err := ParseSpec([]byte(jsonOf(t, map[string]string{"kind": "chatgpt", "model": "g", "credential_file": file})))
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Ready(getenv); err == nil {
		t.Fatal("a credential file that is not there is not ready")
	}
	if err := os.WriteFile(file, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sub.Ready(getenv); err != nil {
		t.Fatal(err)
	}
	t.Setenv(SpecEnv, `{"kind":"gemini","model":"g","key_env":"PROMPTSUITE_TEST_KEY"}`)
	if got, err := LoadSpec("", os.Getenv); err != nil || got.KeyEnv != "PROMPTSUITE_TEST_KEY" {
		t.Fatalf("a spec is read from %s: %v %v", SpecEnv, got, err)
	}
}

func TestACredentialPathWithBackslashesReadsBackAsWritten(t *testing.T) {
	odd := filepath.Join(t.TempDir(), `C:\Users\"op"`, "auth.json")
	s, err := ParseSpec([]byte(jsonOf(t, map[string]string{"kind": "chatgpt", "model": "g", "credential_file": odd})))
	if err != nil || s.CredentialFile != odd {
		t.Fatalf("the path must read back whole: %v %q", err, s)
	}
}
