package pluginhost

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// declarationReader is one declaration member's reader, with a declaration
// it takes.
type declarationReader struct {
	read func(raw string) error
	good string
	// member is one member of good, as written there: the tests respell
	// its name and give it twice.
	member string
}

// declarationReaders is every declaration member a package carries. A
// member added to the package joins this table
// (TestEveryDeclarationMemberHasItsReaderInTheTable), and the tests below
// hold its reader to the one strict read.
func declarationReaders() map[string]declarationReader {
	hash := strings.Repeat("a", 64)
	methods := []string{"mail.sync", "log.event", "sms.inbound"}
	settings := []SettingDecl{{Key: "auth_token", Type: SettingSecret, Title: "Auth token"}}
	return map[string]declarationReader{
		ModelsFile: {
			read:   func(raw string) error { _, err := ParseModels([]byte(raw)); return err },
			good:   `[{"name":"stt-int8.bin","url":"https://models.example.test/stt.bin","sha256":"` + hash + `","size":1024}]`,
			member: `"size":1024`,
		},
		AcceleratorFile: {
			read: func(raw string) error {
				_, err := ParseAccelerators([]byte(raw), []string{"macos-arm64-native"})
				return err
			},
			good:   `{"macos-arm64-native":{"os":"macos","arch":"arm64","backend":"mlx","precision":"int8","models":["silero-vad"],"memory_bytes":6442450944,"session_limit":1,"fallback":"reported"}}`,
			member: `"backend":"mlx"`,
		},
		ScheduleFile: {
			read:   func(raw string) error { _, err := ParseSchedule([]byte(raw), methods); return err },
			good:   `[{"operation":"mail.sync","every_seconds":600}]`,
			member: `"every_seconds":600`,
		},
		SubscriptionsFile: {
			read:   func(raw string) error { _, err := ParseSubscriptions([]byte(raw), methods); return err },
			good:   `[{"operation":"log.event","topic":"tool.called"}]`,
			member: `"topic":"tool.called"`,
		},
		WebhooksFile: {
			read:   func(raw string) error { _, err := ParseWebhooks([]byte(raw), methods, settings); return err },
			good:   `[{"path":"sms","operation":"sms.inbound","signature":{"scheme":"twilio","header":"X-Twilio-Signature","secret_setting":"auth_token"}}]`,
			member: `"header":"X-Twilio-Signature"`,
		},
		SettingsFile: {
			read:   func(raw string) error { _, err := ParseSettings([]byte(raw)); return err },
			good:   `[{"key":"repo","type":"string","title":"Repository"}]`,
			member: `"title":"Repository"`,
		},
		EmbeddingsFile: {
			read:   func(raw string) error { _, err := ParseEmbeddings([]byte(raw)); return err },
			good:   `{"dimension":2,"form":1,"floor":0.3,"model":"m.onnx","probe_bound":0.95,"probes":[{"kind":"cue","text":"where is the harbor","vector":[0.6,0.8]},{"kind":"memory","text":"the ship reached the harbor at dusk","vector":[0.8,0.6]}]}`,
			member: `"dimension":2`,
		},
		RuntimesFile: {
			read:   func(raw string) error { _, err := ParseRuntimes([]byte(raw)); return err },
			good:   `{"runtimes":[{"variant_id":"win","url":"https://x/y.tar.gz","sha256":"` + hash + `","size":1,"installed_bytes":2,"files":3,"inventory_sha256":"` + strings.Repeat("b", 64) + `"}]}`,
			member: `"files":3`,
		},
	}
}

// A declaration member is named by a constant of this package, `XFile =
// "x.json"`. Each one has its reader in the table above, so a member that
// is added cannot be read some other way unnoticed.
func TestEveryDeclarationMemberHasItsReaderInTheTable(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	member := regexp.MustCompile(`(?m)^const \w+File = "([a-z_]+\.json)"`)
	var named []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range member.FindAllStringSubmatch(string(src), -1) {
			named = append(named, m[1])
		}
	}
	var tabled []string
	for file := range declarationReaders() {
		tabled = append(tabled, file)
	}
	sort.Strings(named)
	sort.Strings(tabled)
	if len(named) < 7 {
		t.Fatalf("the scan found %d declaration members (%v): it is not reading the package", len(named), named)
	}
	if strings.Join(named, " ") != strings.Join(tabled, " ") {
		t.Fatalf("the package names these declaration members:\n  %v\nand the table reads these:\n  %v", named, tabled)
	}
}

func TestEveryDeclarationIsReadExactly(t *testing.T) {
	const exact = "must match its field exactly and appear once"
	for file, r := range declarationReaders() {
		if err := r.read(r.good); err != nil {
			t.Errorf("%s: the good declaration is refused, so nothing below is the mutation's: %v", file, err)
			continue
		}
		if strings.Count(r.good, r.member) != 1 {
			t.Fatalf("%s: the member %s is not in the good declaration once", file, r.member)
		}
		name, value, _ := strings.Cut(r.member, ":")
		respelled := strings.ToUpper(name[:2]) + name[2:] // `"Size"` for `"size"`
		if respelled == name {
			t.Fatalf("%s: the member's name has no letter to respell: %s", file, name)
		}
		closer := r.good[len(r.good)-1:]
		for what, tc := range map[string]struct{ raw, want string }{
			"its own closer again":             {r.good + closer, ""},
			"the other closer after it":        {r.good + map[string]string{"]": "}", "}": "]"}[closer], ""},
			"a second value after it":          {r.good + " " + r.good, ""},
			"a member named in another case":   {strings.Replace(r.good, r.member, respelled+":"+value, 1), exact},
			"a member given twice, the same":   {strings.Replace(r.good, r.member, r.member+","+r.member, 1), ""},
			"a member given in both spellings": {strings.Replace(r.good, r.member, respelled+":"+value+","+r.member, 1), ""},
		} {
			err := r.read(tc.raw)
			if err == nil {
				t.Errorf("%s: %s is read as a declaration: %s", file, what, tc.raw)
				continue
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %s is refused, but not for its name: %v", file, what, err)
			}
		}
	}
}

// What follows the value is refused in each member's own words, as it was
// for a second value: a stray closer is content after the value too.
func TestAStrayCloserIsContentAfterTheDeclaration(t *testing.T) {
	want := map[string]string{
		ModelsFile:      "trailing content",
		AcceleratorFile: "trailing content",
		ScheduleFile:    "trailing content after the list",
		WebhooksFile:    "trailing content after the list",
		SettingsFile:    "trailing content after the list",
		RuntimesFile:    "content after the JSON value",
		EmbeddingsFile:  "trailing content after the embeddings declaration",
	}
	readers := declarationReaders()
	for file, text := range want {
		r, ok := readers[file]
		if !ok {
			t.Fatalf("%s has no reader in the table", file)
		}
		for _, stray := range []string{"]", "}"} {
			if err := r.read(r.good + stray); err == nil || err.Error() != text {
				t.Errorf("%s followed by %s: refusal %v, want %q", file, stray, err, text)
			}
		}
	}
	// The subscriptions member is canonical JSON first, and that check is
	// the one that meets the closer; its refusal is its own.
	if r := readers[SubscriptionsFile]; r.read(r.good+"]") == nil {
		t.Errorf("%s followed by a closer is read", SubscriptionsFile)
	}
	if len(readers) != len(want)+1 {
		t.Fatalf("%d readers in the table, %d accounted for here", len(readers), len(want)+1)
	}
}
