package test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

var hostLiteralRoots = []string{"internal/publicname", "internal/certs", "internal/relay", "internal/dashboard", "config/config.json"}

// The test kits under those roots, by path: test code no product build
// imports. Only these are passed by; any other directory is read, whatever
// its name ends in.
var hostLiteralTestKits = []string{"internal/publicname/publicnametest", "internal/relay/relaytest"}

type hostLiteral struct{ file, literal string }

// The named list. count is how many times the literal stands in the file.
var hostLiteralsAllowed = map[hostLiteral]struct {
	count int
	why   string
}{
	{"internal/publicname/config.go", "https://certificate.aiii.id"}:                    {1, "the one default of certificate.server_url"},
	{"internal/publicname/config.go", "https://acme-v02.api.letsencrypt.org/directory"}: {1, "the one default of certificate.acme_directory"},
	{"internal/dashboard/qr.go", "http://www.w3.org/2000/svg"}:                          {1, "the SVG namespace: a name, never fetched"},
	{"internal/dashboard/static/views/settings.js", "http://localhost:8000/v1"}:         {3, "placeholder text for an operator's own model server"},
	{"internal/dashboard/static/views/plugins.js", "192.168.1.0"}:                       {1, "an example of a range, in the words beside a plugin's network list"},
	{"config/config.json", "https://witness.aiii.id"}:                                   {1, "the default of witness_server.url, a platform service this rule is not about"},
	{"config/config.json", "https://genesis.aiii.id"}:                                   {1, "the default of genesis.server_url, a platform service this rule is not about"},
	{"config/config.json", "https://firewall.aiii.id"}:                                  {1, "the default of genesis.firewall_url, a platform service this rule is not about"},
	{"config/config.json", "https://bootstrap.aiii.id"}:                                 {1, "the default of genesis.bootstrap_url, a platform service this rule is not about"},
}

var (
	schemedHost = regexp.MustCompile(`\b(?:https?|wss?)://[A-Za-z0-9\[][^\s"'` + "`" + `<>)\\]*|\b(?:stuns?|turns?):[A-Za-z0-9\[][^\s"'` + "`" + `<>)\\]*`)
	quotedHost  = regexp.MustCompile(`(?:["'` + "`" + `@]|//)((?:[a-z0-9-]+\.)+(?:id|com|net|org|io|dev|app|ai|cloud|co|me|info|xyz|network|tech|sh|to|us|uk|de|eu))(?:["'` + "`" + `:/]|\b)`)
	ipv4Literal = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
)

// hostLiteralsIn is every host or address literal in the non-test files
// under root's dirs (or in a root that is one file), by file
// (slash-separated, relative to root) and literal, with the lines it stands
// on.
func hostLiteralsIn(t *testing.T, root string, dirs []string) map[hostLiteral][]int {
	t.Helper()
	found := map[hostLiteral][]int{}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			name := d.Name()
			if d.IsDir() {
				// Test data, and a package's test kit named on the list, are
				// test code. A directory is not one by its name's ending.
				if name == "testdata" || slices.Contains(hostLiteralTestKits, rel) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(name, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.ContainsRune(string(raw), 0) {
				return nil // not text
			}
			text := string(raw)
			if strings.HasSuffix(name, ".go") {
				text = withoutImports(t, path, raw)
			}
			for i, line := range strings.Split(text, "\n") {
				seen := map[string]bool{}
				for _, m := range schemedHost.FindAllString(line, -1) {
					seen[strings.TrimRight(m, ".,;:")] = true
				}
				rest := schemedHost.ReplaceAllString(line, " ")
				for _, m := range quotedHost.FindAllStringSubmatch(rest, -1) {
					seen[m[1]] = true
				}
				for _, m := range ipv4Literal.FindAllString(rest, -1) {
					if !strings.HasPrefix(m, "127.") && m != "0.0.0.0" {
						seen[m] = true
					}
				}
				for literal := range seen {
					key := hostLiteral{rel, literal}
					found[key] = append(found[key], i+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return found
}

// withoutImports is a Go file's text with its import paths blanked: a
// module's path begins with a host and names nothing the program reaches.
func withoutImports(t *testing.T, path string, raw []byte) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, raw, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	out := []byte(string(raw))
	for _, spec := range file.Imports {
		for i := fset.Position(spec.Path.Pos()).Offset; i < fset.Position(spec.Path.End()).Offset; i++ {
			out[i] = ' '
		}
	}
	return string(out)
}

func TestNoHostIsWrittenIntoTheCodeThatReachesOne(t *testing.T) {
	found := hostLiteralsIn(t, repoRoot(t), hostLiteralRoots)
	var wrong []string
	for key, lines := range found {
		allowed, ok := hostLiteralsAllowed[key]
		switch {
		case !ok:
			wrong = append(wrong, fmt.Sprintf("%s: %s (line %v) is a host written into the code. What an identity reaches is a setting with one default; if this is that default, or reaches nothing, name it in hostLiteralsAllowed with its reason", key.file, key.literal, lines))
		case len(lines) != allowed.count:
			wrong = append(wrong, fmt.Sprintf("%s: %s stands on lines %v and the list allows it %d time(s) (%s): a second holder of an allowed literal is still a second holder", key.file, key.literal, lines, allowed.count, allowed.why))
		}
	}
	for key, allowed := range hostLiteralsAllowed {
		if _, ok := found[key]; !ok {
			wrong = append(wrong, fmt.Sprintf("%s: %s is on the list (%s) and no longer in the file: take it off the list", key.file, key.literal, allowed.why))
		}
	}
	for _, kit := range hostLiteralTestKits {
		if info, err := os.Stat(filepath.Join(repoRoot(t), filepath.FromSlash(kit))); err != nil || !info.IsDir() {
			wrong = append(wrong, fmt.Sprintf("%s: a test kit on hostLiteralTestKits that is no longer a directory: take it off the list", kit))
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Fatalf("%d:\n%s", len(wrong), strings.Join(wrong, "\n"))
	}
}

// THE READING FINDS WHAT IT IS FOR, in the four files a host would most
// easily be written into, in each form one takes, in the default
// configuration's dashboard block, and in a directory whose name ends in
// "test" and is no test kit; and it passes by what is not one: a test, a
// test kit, a module's import path, this machine's own address, a property
// read in the page's code.
func TestTheHostLiteralReadingFindsWhatItIsFor(t *testing.T) {
	root := t.TempDir()
	plant := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plant("internal/dashboard/static/ws.js", "const u = 'wss://relay.example.net/attach';\nconst own = location.protocol === 'https:' ? 'wss://' : 'ws://';\n")
	plant("internal/dashboard/static/views/settings.js", "const servers = [{ urls: ['turn:turn.example.org:3478'] }];\nconst id = flow.code.id, ok = S.app.info;\n")
	plant("internal/dashboard/channel_peer.go", "package dashboard\n\nimport \"github.com/pion/webrtc/v4\"\n\nvar _ webrtc.Configuration\n\nconst relayHost = \"relay.aiii.id\"\nconst bind = \"127.0.0.1\"\n")
	plant("internal/relay/client.go", "package relay\n\nvar fallback = \"203.0.113.7:3478\" // said to be a stun:, turn: or turns: address\n")
	plant("internal/relay/client_test.go", "package relay\n\nconst testRelay = \"https://relay.example.test\"\n")
	plant("internal/relay/relaytest/kit.go", "package relaytest\n\nconst Relay = \"wss://relay.example.net\"\n")
	plant("internal/publicname/testdata/status.json", `{"relay":"https://relay.example.net"}`)
	plant("internal/dashboard/static/latest/relay.js", "export const relay = 'wss://relay.example.net';\n")
	plant("config/config.json", "{\n  \"dashboard\": {\n    \"port\": 8180,\n    \"ice_servers\":[{\"urls\":[\"turn:turn.example.id:3478\"]}]\n  }\n}\n")
	found := hostLiteralsIn(t, root, []string{"internal/publicname", "internal/relay", "internal/dashboard", "config/config.json"})
	want := map[hostLiteral]int{
		{"internal/dashboard/static/ws.js", "wss://relay.example.net/attach"}:         1,
		{"internal/dashboard/static/views/settings.js", "turn:turn.example.org:3478"}: 1,
		{"internal/dashboard/channel_peer.go", "relay.aiii.id"}:                       7,
		{"internal/relay/client.go", "203.0.113.7"}:                                   3,
		{"internal/dashboard/static/latest/relay.js", "wss://relay.example.net"}:      1,
		{"config/config.json", "turn:turn.example.id:3478"}:                           4,
	}
	for key, line := range want {
		if lines := found[key]; len(lines) != 1 || lines[0] != line {
			t.Errorf("%s: %q was read on lines %v, want line %d: a host written there would pass", key.file, key.literal, lines, line)
		}
	}
	for key, lines := range found {
		if _, ok := want[key]; !ok {
			t.Errorf("%s: %q (lines %v) was read as a host and is not one this reading is for", key.file, key.literal, lines)
		}
	}
}
