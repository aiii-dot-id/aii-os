package dashboard

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
var embeddedCitation = regexp.MustCompile(strings.Join([]string{
	`\bR\d+\b`, `\bD\d+\b`, `\bF-\d+\b`,
	`[A-Za-z_][A-Za-z0-9_]*\.md\b`,
	`\bdocs/[A-Za-z0-9_.-]+`,
	`[A-Za-z_][A-Za-z0-9_/]*\.(?:c|h|go|toml|kt|swift)\b`,
	`\.(?:c|h|go):\d`,
	`ADR-\d+`, `\bAUDIT\b`,
	`§\s?\d+`,
	`\d{4}-\d{2}-\d{2}`,
	`/home/user`, `/opt/(?:go|tinygo)`, `/root/`,
	`\bbatch \d+`, `\bexternal review\b`, `\bGO\d{2,3}\b`,
	`\bV-[A-Za-z]+\d+\b`, `\bhandoff req \d+\b`,
	`\b10\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`,
	`PLUGIN_SDK|SECURE_CONTEXT|RULINGS|IDENTITY_SEMANTICS|UI_FRAME|THREAT_MODEL|PLUGIN_REVOCATION|BETA1|GO_CANON`,
}, "|"))

// .
// .
// .
var hexWord = regexp.MustCompile(`\b[0-9a-f]{7,8}\b`)

func citationIn(body string) bool {
	if embeddedCitation.MatchString(body) {
		return true
	}
	for _, w := range hexWord.FindAllString(body, -1) {
		if strings.ContainsAny(w, "abcdef") && strings.ContainsAny(w, "0123456789") {
			return true
		}
	}
	return false
}

// .
// .
// .
func commentBodies(text string, block bool) map[int]string {
	out := map[int]string{}
	inBlock := false
	for i, line := range strings.Split(text, "\n") {
		s := strings.TrimLeft(line, " \t")
		switch {
		case inBlock:
			if end := strings.Index(s, "*/"); end >= 0 {
				out[i+1] = s[:end]
				inBlock = false
			} else {
				out[i+1] = s
			}
		case strings.HasPrefix(s, "/*"):
			if end := strings.Index(s, "*/"); end >= 0 {
				out[i+1] = s[2:end]
			} else {
				out[i+1] = s[2:]
				inBlock = true
			}
		case !block && strings.HasPrefix(s, "//"):
			out[i+1] = s[2:]
		case !block:
			// .
			at := -1
			for j := 0; j+1 < len(line); j++ {
				if line[j] == '/' && line[j+1] == '/' && (j == 0 || (line[j-1] != ':' && line[j-1] != '/')) {
					at = j
				}
			}
			if at >= 0 {
				out[i+1] = line[at+2:]
			}
		}
	}
	return out
}

func TestEmbeddedAssetsCarryNoCitationTheDerivationStrips(t *testing.T) {
	var hits []string
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		block := strings.HasSuffix(path, ".css")
		if !block && !strings.HasSuffix(path, ".js") {
			return nil
		}
		raw, rerr := staticFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		bodies := commentBodies(string(raw), block)
		for n := 1; n <= len(strings.Split(string(raw), "\n")); n++ {
			if body, ok := bodies[n]; ok && citationIn(body) {
				hits = append(hits, path+":"+strconv.Itoa(n)+": "+strings.TrimSpace(body))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("embedded assets carry citations the public derivation strips, so the public binary would differ from this one:\n%s", strings.Join(hits, "\n"))
	}
}
