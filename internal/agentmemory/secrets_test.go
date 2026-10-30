package agentmemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shapeSamples are made-up values, one or more in the shape of each of
// DefaultSecrets, named by the rule they follow. None opens anything.
func shapeSamples() map[string]string {
	r := func(n int) string { return strings.Repeat("Ab3", n)[:n] }
	lower := func(n int) string { return strings.Repeat("ab3", n)[:n] }
	hex := func(n int) string { return strings.Repeat("0a1b2c3d4e5f6789", n)[:n] }
	upper := func(n int) string { return strings.Repeat("AB3", n)[:n] }
	uuid := "0a1b2c3d-0a1b-0a1b-0a1b-0a1b2c3d4e5f"
	return map[string]string{
		"private-key":                   "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n",
		"putty":                         "PuTTY-User-Key-File-3: ssh-ed25519\n",
		"age-secret-key":                "AGE-SECRET-KEY-1" + upper(58),
		"anthropic-api-key":             "use sk-ant-api03-" + r(40) + " here",
		"huggingface-access-token":      "hf_" + r(34),
		"huggingface-organization":      "api_org_" + r(34),
		"github-pat":                    "ghp_" + r(36),
		"github-fine-grained-pat":       "github_pat_" + strings.Repeat("A1_", 28)[:82],
		"gitlab-pat":                    "glpat-" + r(20),
		"gitlab-ptt":                    "glptt-" + hex(40),
		"gitlab-rrt":                    "GR1348941" + r(20),
		"gitlab-scim-token":             "glsoat-" + r(20),
		"gitlab-cicd-job-token":         "glcbt-1a2b_" + r(20),
		"gitlab-incoming-mail-token":    "glimt-" + r(25),
		"gitlab-kubernetes-agent-token": "glagent-" + r(50),
		"gitlab-oauth-app-secret":       "gloas-" + r(64),
		"gitlab-session-cookie":         "_gitlab_session=" + lower(32),
		"aws-access-token":              "AKIAABCDEFGHIJ234567",
		"aws-secret":                    "aws_secret_access_key = " + r(40),
		"bedrock-long-lived":            "ABSK" + r(120),
		"bedrock-short-lived":           "bedrock-api-key-YmVkcm9jay5hbWF6b25hd3MuY29t&Action=x",
		"slack-bot-token":               "xoxb-1234567890-1234567890-" + r(24),
		"slack-user-token":              "xoxp-1234567890-1234567890-1234567890-" + r(30),
		"slack-legacy-token":            "xoxo-1234-5678-9012-abcdef",
		"slack-app-token":               "xapp-1-A0123456789-1234567890-" + lower(30),
		"slack-webhook-url":             "https://hooks.slack.com/services/T00000000/B00000000/" + r(24),
		"stripe-access-token":           "sk_live_" + r(24),
		"gcp-api-key":                   "AIza" + r(35),
		"google-oauth-access-token":     "ya29." + r(60),
		"google-oauth-client-secret":    "GOCSPX-" + r(28),
		"npm-access-token":              "npm_" + lower(36),
		"telegram-bot-api-token":        "bot 1234567890:A" + r(34),
		"jwt":                           "eyJhbGciOiJIUzI1NiJ9x.eyJzdWIiOiIxMjM0NTY3OD.SflKxwRJSMeKKF2QT4fwpM",
		"jwt-base64":                    "ZXlKaGJHY2lPaU" + r(40),
		"azure-storage-key":             "DefaultEndpointsProtocol=https;AccountKey=" + r(86) + "==",
		"azure-ad-client-secret":        "abc1Q~" + r(32),
		"url-with-password":             "postgres://admin:hunter2-long-pass@db.example:5432/prod",
		"sidekiq-sensitive-url":         "https://0a1b2c3d:4e5f6789@gems.example.com/",
		"1password-secret-key":          "A3-ABCDEF-ABCDEFGHIJK-ABCDE-ABCDE-ABCDE",
		"1password-service-account":     "ops_eyJ" + r(260),
		"adobe-client-secret":           "p8e-" + r(32),
		"airtable-pat":                  "pat" + r(14) + "." + hex(64),
		"alibaba-access-key-id":         "LTAI" + r(20),
		"artifactory-api-key":           "AKCp" + r(69),
		"artifactory-reference-token":   "cmVmd" + r(59),
		"authress":                      "sc_" + lower(10) + "." + lower(5) + ".acc_" + lower(12) + "." + r(40),
		"clickhouse":                    "4b1d" + r(38),
		"clojars-api-token":             "CLOJARS_" + lower(60),
		"cloudflare-origin-ca-key":      "v1.0-" + hex(24) + "-" + hex(146),
		"databricks-api-token":          "dapi" + hex(32),
		"digitalocean-pat":              "dop_v1_" + hex(64),
		"doppler-api-token":             "dp.pt." + r(43),
		"duffel-api-token":              "duffel_test_" + r(43),
		"dynatrace-api-token":           "dt0c01." + r(24) + "." + r(64),
		"easypost-api-token":            "EZAK" + r(54),
		"facebook-page-access-token":    "EAAM" + r(110),
		"flutterwave-secret-key":        "FLWSECK_TEST-" + hex(32) + "-X",
		"flutterwave-public-key":        "FLWPUBK_TEST-" + hex(32) + "-X",
		"flyio-access-token":            "fo1_" + r(43),
		"frameio-api-token":             "fio-u-" + r(64),
		"grafana-api-key":               "eyJrIjoi" + r(80),
		"grafana-cloud-api-token":       "glc_" + r(40),
		"grafana-service-account-token": "glsa_" + r(32) + "_" + hex(8),
		"harness-api-key":               "pat." + r(22) + "." + r(24) + "." + r(20),
		"hashicorp-tf-api-token":        lower(14) + ".atlasv1." + r(64),
		"heroku-api-key-v2":             "HRKU-AA" + r(58),
		"infracost-api-token":           "ico-" + r(32),
		"intra42-client-secret":         "s-s4t2ud-" + hex(64),
		"linear-api-key":                "lin_api_" + r(40),
		"maxmind-license-key":           r(6) + "_" + r(29) + "_mmk",
		"microsoft-teams-webhook":       "https://acme.webhook.office.com/webhookb2/" + uuid + "@" + uuid + "/IncomingWebhook/" + hex(32) + "/" + uuid,
		"notion-api-token":              "ntn_12345678901" + r(35),
		"octopus-deploy-api-key":        "API-" + upper(26),
		"openshift-user-token":          "sha256~" + r(43),
		"perplexity-api-key":            "pplx-" + r(48),
		"planetscale-api-token":         "pscale_tkn_" + r(40),
		"postman-api-token":             "PMAK-" + hex(24) + "-" + hex(34),
		"prefect-api-token":             "pnu_" + r(36),
		"pulumi-api-token":              "pul-" + hex(40),
		"pypi-upload-token":             "pypi-AgEIcHlwaS5vcmc" + r(60),
		"readme-api-token":              "rdme_" + lower(70),
		"rubygems-api-token":            "rubygems_" + hex(48),
		"scalingo-api-token":            "tk-us-" + r(48),
		"sendgrid-api-token":            "SG." + r(22) + "." + r(43),
		"sendinblue-api-token":          "xkeysib-" + hex(64) + "-" + r(16),
		"sentry-org-token":              "sntrys_eyJpYXQiO" + r(20) + "LCJyZWdpb25fdXJs" + r(20) + "_" + r(43),
		"sentry-user-token":             "sntryu_" + hex(64),
		"settlemint-pat":                "sm_pat_" + r(16),
		"shippo-api-token":              "shippo_live_" + hex(40),
		"shopify-access-token":          "shpat_" + hex(32),
		"sourcegraph-access-token":      "sgp_" + hex(16) + "_" + hex(40),
		"square-access-token":           "sq0atp-" + r(22),
		"supabase-access-token":         "sbp_" + hex(40),
		"twilio-api-key":                "SK" + hex(32),
		"new-relic-user-api-key":        "NRAK-" + lower(27),
		"new-relic-insert-key":          "NRII-" + lower(32),
		"new-relic-browser-api-token":   "NRJS-" + hex(19),
		"defined-networking-api-token":  "dnkey-" + lower(26) + "-" + lower(52),
		"typeform-api-token":            "tfp_" + lower(59),
		"yandex-api-key":                "AQVN" + r(36),
		"yandex-access-token":           "t1." + r(10) + "." + r(86),
		"mailgun-pub-key":               "pubkey-" + hex(32),
		"dropbox-short-lived-api-token": "sl." + lower(135),
		"plaid-api-token":               "access-sandbox-" + uuid,
		"mapbox-api-token":              "pk." + lower(60) + "." + lower(22),
		"twitter-bearer-token":          strings.Repeat("A", 22) + r(90),
		"lob-api-key":                   "live_" + hex(35),
		"lob-pub-api-key":               "test_pub_" + hex(31),
		"sonar-api-token":               "squ_" + hex(40),
		"mailchimp-api-key":             hex(32) + "-us12",
		"vault-batch-token":             "hvb." + r(140),
		"vault-service-token":           "hvs." + r(95),
		"vault-legacy-token":            "token s." + r(24) + " here",
		"curl-auth-header":              `curl https://api.example.com -H "Authorization: Bearer ` + r(24) + `"`,
		"curl-auth-user":                "curl -u admin:hunter2pass https://example.com",
		"kubernetes-secret-yaml":        "kind: Secret\nmetadata:\n  name: db\ndata:\n  password: cGFzc3dvcmQxMjM=\n",
		"nuget-config-password":         `<add key="ClearTextPassword" value="hunter2pass" />`,
		"freemius-secret-key":           `'secret_key' => 'sk_` + r(29) + `'`,
	}
}

// EVERY BUILT-IN SHAPE TAKES ONE OF THE SAMPLES, AND EVERY SAMPLE IS TAKEN.
// A shape that takes nothing would be a rule said to be followed and not
// followed; a sample no shape takes, a token said to be left that is
// copied. Each built-in shape is RE2, so the configuration can name them
// back.
func TestEveryBuiltInShapeTakesASampleOfIt(t *testing.T) {
	shapes, err := CompileSecrets(DefaultSecrets)
	if err != nil {
		t.Fatal(err)
	}
	samples := shapeSamples()
	taken := map[string]bool{}
	for _, re := range shapes {
		found := false
		for name, sample := range samples {
			if re.MatchString(sample) {
				taken[name], found = true, true
			}
		}
		if !found {
			t.Errorf("no sample is in the shape %s", re)
		}
	}
	for name := range samples {
		if !taken[name] {
			t.Errorf("no shape takes the %s sample", name)
		}
	}
}

// A NOTE THAT HOLDS ANY OF GITLEAKS' PREFIXED TOKENS IS LEFT, and ordinary
// notes beside them, a git hash, a digest, an identifier, a command that
// names its token by a variable, are copied.
func TestANoteHoldingAPrefixedVendorTokenIsLeft(t *testing.T) {
	samples := shapeSamples()
	ordinary := map[string]string{
		"git-hash":     "fixed in " + strings.Repeat("0123456789abcdef", 3)[:40],
		"digest":       "sha256 " + strings.Repeat("0123456789abcdef", 4),
		"uuid":         "the session is 0a1b2c3d-0a1b-0a1b-0a1b-0a1b2c3d4e5f",
		"identifier":   "call this.someVeryLongIdentifierNameHere and API-routes",
		"curl-var":     `curl https://api.example.com -H "Authorization: Bearer $TOKEN"`,
		"config-map":   "kind: ConfigMap\ndata:\n  mode: production\n",
		"skill":        "SKILL.md lists the TASK steps",
		"short-prefix": "the pat. and sat. prefixes, and glc_ and dapi in prose",
		"md5-key":      "the cache key-" + strings.Repeat("0123456789abcdef", 2) + " is an MD5",
		"snake-live":   "set live_streaming_configuration_for_production_hosts to true",
		"yc-name":      "YCombinatorStartupDirectoryEntryNameField is a field",
		"sqa-name":     "SQA_REGRESSION_SUITE_FOR_THE_NIGHTLY_BUILD_RUN is a variable",
		"test-hash":    "test_" + strings.Repeat("0123456789abcdef", 3)[:40] + " ran",
		"base64-zeros": "data:image/png;base64,iVBORw0KGgo" + strings.Repeat("A", 30) + "+/" + strings.Repeat("Ab3", 20),
	}
	src := filepath.Join(t.TempDir(), "codex")
	for name, body := range samples {
		write(t, src, "memories/secret-"+name+".md", body)
	}
	for name, body := range ordinary {
		write(t, src, "memories/plain-"+name+".md", body)
	}
	dst := filepath.Join(t.TempDir(), Folder)
	report, err := Copy(context.Background(), []Store{{Name: "codex", Dir: src, Take: []string{"memories"}}}, dst, Limits{}, nil, born)
	if err != nil {
		t.Fatal(err)
	}
	for name := range samples {
		if _, err := os.Stat(filepath.Join(dst, "codex", "memories", "secret-"+name+".md")); err == nil {
			t.Errorf("a note holding a %s was copied", name)
		}
	}
	for name := range ordinary {
		if _, err := os.Stat(filepath.Join(dst, "codex", "memories", "plain-"+name+".md")); err != nil {
			t.Errorf("an ordinary note (%s) was taken for a secret: %v", name, err)
		}
	}
	if got := report.Stores[0].Left[LeftSecret]; got != len(samples) {
		t.Errorf("%d notes left as holding a secret, want %d", got, len(samples))
	}
}

// A SHAPE'S WORDS NEVER CHANGE WHAT IS LEFT. A note is read through a
// shape only when it holds one of the words every match of it holds; read
// through every shape regardless, each note here, the samples, ordinary
// notes and a key named in letters a case-blind shape takes for ASCII
// ones, is found to hold a secret or not the same.
func TestAShapesWordsNeverChangeWhatIsLeft(t *testing.T) {
	notes := []string{
		"aws_\u017fecret_access_key = " + strings.Repeat("Ab3", 14)[:40], // ſ, which (?i)s takes
		"AWS SECRET: " + strings.Repeat("Ab3", 14)[:40],
		"CLOJARS_" + strings.Repeat("AB3", 20),
		"an ordinary note, with https://example.com in it",
		"",
	}
	for _, sample := range shapeSamples() {
		notes = append(notes, sample, strings.ToUpper(sample), strings.ToLower(sample))
	}
	shapes, err := compileShapes(DefaultSecrets)
	if err != nil {
		t.Fatal(err)
	}
	every := make([]shape, len(shapes))
	for i, sh := range shapes {
		if sh.words == nil {
			t.Errorf("the shape %s has no words, so every note is read through it", sh.re)
		}
		every[i] = shape{re: sh.re}
	}
	read, readAll := &copier{secrets: shapes}, &copier{secrets: every}
	for _, note := range notes {
		if got, want := read.holdsSecret([]byte(note)), readAll.holdsSecret([]byte(note)); got != want {
			t.Errorf("%q: read through the shapes its words name, holds a secret %v; through every shape, %v", note, got, want)
		}
	}
	if !read.holdsSecret([]byte(notes[0])) {
		t.Error("a key named with ſ for s is not found")
	}
}

// THE SHAPES ARE READ IN TIME A BIRTH CAN SPEND. Every built-in shape over
// a note of the largest size copied by default (1 MiB) of ordinary
// Markdown.
func BenchmarkTheShapesOverALargeNote(b *testing.B) {
	line := "- The build host is shared; run `go test ./...` before a commit, and see https://example.com/docs#setup for the 3 steps.\n"
	note := []byte(strings.Repeat(line, DefaultMaxFileBytes/len(line)))
	shapes, err := compileShapes(DefaultSecrets)
	if err != nil {
		b.Fatal(err)
	}
	c := &copier{secrets: shapes}
	b.SetBytes(int64(len(note)))
	for b.Loop() {
		if c.holdsSecret(note) {
			b.Fatal("an ordinary note holds a secret")
		}
	}
}
