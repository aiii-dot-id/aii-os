package pluginhost

import (
	"strings"
	"testing"
)

// A webhook declaration is held to the package: a signature is
// required, the scheme is one the host verifies, the operation is a
// declared method, and the secret is a declared secret-typed setting.
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

// google-oidc has no shared secret: it is Authorization: Bearer, verified for
// one service account the operator sets, and it is refused any other way of
// being declared — as are the other schemes when they name that account.
func TestAGoogleOIDCWebhookIsDeclaredWithItsAccountAndNoSecret(t *testing.T) {
	methods := []string{"mail.notify"}
	settings := []SettingDecl{{Key: "auth_token", Type: SettingSecret, Title: "Token"}, {Key: "push_account", Type: SettingString, Title: "Pub/Sub service account"}, {Key: "handle", Type: SettingSecret, Title: "Handle"}}
	decl := func(sig string) string {
		return `[{"path":"notify","operation":"mail.notify","signature":` + sig + `}]`
	}
	good := decl(`{"scheme":"google-oidc","header":"Authorization","prefix":"Bearer ","email_setting":"push_account"}`)
	decls, err := ParseWebhooks([]byte(good), methods, settings)
	if err != nil || len(decls) != 1 || decls[0].Signature.Scheme != SchemeGoogleOIDC || decls[0].Signature.EmailSetting != "push_account" || decls[0].Signature.SecretSetting != "" {
		t.Fatalf("a google-oidc declaration parses: %v %+v", err, decls)
	}
	for name, tc := range map[string]struct{ sig, want string }{
		"a header that is not Authorization": {`{"scheme":"google-oidc","header":"X-Token","prefix":"Bearer ","email_setting":"push_account"}`, "Authorization: Bearer"},
		"no Bearer prefix":                   {`{"scheme":"google-oidc","header":"Authorization","email_setting":"push_account"}`, "Authorization: Bearer"},
		"a shared secret named":              {`{"scheme":"google-oidc","header":"Authorization","prefix":"Bearer ","secret_setting":"auth_token","email_setting":"push_account"}`, "no shared secret"},
		"no service account setting":         {`{"scheme":"google-oidc","header":"Authorization","prefix":"Bearer "}`, "email_setting"},
		"an account setting that is secret":  {`{"scheme":"google-oidc","header":"Authorization","prefix":"Bearer ","email_setting":"handle"}`, "string-typed"},
		"an undeclared account setting":      {`{"scheme":"google-oidc","header":"Authorization","prefix":"Bearer ","email_setting":"nothing"}`, "string-typed"},
		"another scheme naming an account":   {`{"scheme":"token","header":"X","secret_setting":"auth_token","email_setting":"push_account"}`, "google-oidc only"},
	} {
		_, err := ParseWebhooks([]byte(decl(tc.sig)), methods, settings)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
}
