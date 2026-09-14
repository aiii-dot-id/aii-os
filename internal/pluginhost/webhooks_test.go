package pluginhost

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestWebhookDeclarationsAreHeldToThePackage(t *testing.T) {
	methods := []string{"sms.inbound", "sms.send"}
	settings := []SettingDecl{{Key: "auth_token", Type: SettingSecret, Title: "Auth token"}, {Key: "repo", Type: SettingString, Title: "Repo"}}
	good := `[{"path":"sms","operation":"sms.inbound","signature":{"scheme":"twilio","header":"X-Twilio-Signature","secret_setting":"auth_token"},"max_body_bytes":65536}]`
	decls, err := ParseWebhooks([]byte(good), methods, settings)
	if err != nil || len(decls) != 1 || decls[0].Signature.Scheme != SchemeTwilio {
		t.Fatalf("a good declaration parses: %v %+v", err, decls)
	}
	for name, tc := range map[string]struct{ raw, want string }{
		"no signature":         {`[{"path":"sms","operation":"sms.inbound"}]`, "signature is required"},
		"unknown scheme":       {`[{"path":"sms","operation":"sms.inbound","signature":{"scheme":"md5","header":"X","secret_setting":"auth_token"}}]`, "scheme"},
		"undeclared operation": {`[{"path":"sms","operation":"sms.forward","signature":{"scheme":"token","header":"X","secret_setting":"auth_token"}}]`, "not a method"},
		"non-secret setting":   {`[{"path":"sms","operation":"sms.inbound","signature":{"scheme":"token","header":"X","secret_setting":"repo"}}]`, "secret-typed"},
		"bad path":             {`[{"path":"../x","operation":"sms.inbound","signature":{"scheme":"token","header":"X","secret_setting":"auth_token"}}]`, "path"},
		"duplicate path":       {`[{"path":"a","operation":"sms.inbound","signature":{"scheme":"token","header":"X","secret_setting":"auth_token"}},{"path":"a","operation":"sms.inbound","signature":{"scheme":"token","header":"X","secret_setting":"auth_token"}}]`, "twice"},
		"unknown member":       {`[{"path":"a","operation":"sms.inbound","retries":3,"signature":{"scheme":"token","header":"X","secret_setting":"auth_token"}}]`, "unknown field"},
		"body over the cap":    {`[{"path":"a","operation":"sms.inbound","max_body_bytes":9999999,"signature":{"scheme":"token","header":"X","secret_setting":"auth_token"}}]`, "max_body_bytes"},
		"not a list":           {`{"path":"a"}`, "not a list"},
	} {
		_, err := ParseWebhooks([]byte(tc.raw), methods, settings)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
	if ToolNameFor("org.example.sms", "sms.inbound") != "pl_org_example_sms_sms_inbound" {
		t.Fatal("the tool name derivation is the registration's")
	}
}
