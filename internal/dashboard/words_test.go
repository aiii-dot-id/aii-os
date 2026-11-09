package dashboard

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE WORDS MEET THE STANDARD (spec section 2; the guard of section 8, from
// step 4 on). Every string the page says lives in static/strings/, and what
// a script can check of the standard is checked here, as the annex's
// check.py checks the drafts: the page never speaks as the identity (no
// "I", "me", "my", "mine"); no run of three all-caps words; no
// configuration key, file name or technical term outside the technical
// words (settings-technical.js, settings-technical-forms.js, glossary.js); help (a key ending in Hint,
// Line, Note, Explained, heading, intro, Does, Warns, Confirm or Risk) at
// most 25 words. A longer string that is not help is a reader's to judge,
// as the drafts' own check lists it, not a failure.

var (
	wordsHelpKey     = regexp.MustCompile(`(Hint|Line|Note|Explained|heading|intro|Does|Warns|Confirm|Risk)$`)
	wordsFirstPerson = regexp.MustCompile(`\b(I|me|my|mine)\b`)
	wordsCapsRun     = regexp.MustCompile(`\b[A-Z]{2,}\b(?:\s+\b[A-Z]{2,}\b){2,}`)
	wordsKeyish      = regexp.MustCompile(`\b[a-z]+\.[a-z_]+_[a-z_]+\b|\b[a-z_]+\.json\b`)
	wordsTechnical   = regexp.MustCompile(`\b(witness|seq|ticks|unanchored|SAFE|substrate)\b`)
	wordsString      = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	wordsKey         = regexp.MustCompile(`^([A-Za-z0-9_]+):\s*`)
	wordsSlot        = regexp.MustCompile(`\{[A-Za-z_]+\}`)
)

type wordsLine struct {
	file, key, text string
	line            int
}

// pageWords reads every string of static/strings/, with the key it is under.
func pageWords(t *testing.T) []wordsLine {
	t.Helper()
	var out []wordsLine
	err := fs.WalkDir(staticFS, "static/strings", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		key := ""
		for n, raw := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(raw)
			if strings.HasPrefix(s, "//") {
				continue
			}
			if m := wordsKey.FindStringSubmatch(s); m != nil {
				key = m[1]
			}
			for _, m := range wordsString.FindAllStringSubmatch(s, -1) {
				out = append(out, wordsLine{file: strings.TrimPrefix(p, "static/strings/"), key: key, text: m[1], line: n + 1})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 100 {
		t.Fatalf("only %d strings found under static/strings: this guard is not reading where the words are", len(out))
	}
	return out
}

func TestWordsMeetTheStandard(t *testing.T) {
	var fails []string
	for _, w := range pageWords(t) {
		shown := wordsSlot.ReplaceAllString(w.text, "")
		where := w.file + ":" + strconv.Itoa(w.line) + " " + w.key
		technical := w.file == "settings-technical.js" || w.file == "settings-technical-forms.js" || w.file == "glossary.js"
		if wordsCapsRun.MatchString(shown) {
			fails = append(fails, where+": an all-caps run: "+w.text)
		}
		glossed := strings.Contains(shown, "calls their ")
		if !technical && !glossed && (wordsKeyish.MatchString(shown) || wordsTechnical.MatchString(shown)) {
			fails = append(fails, where+": a technical word outside the technical words: "+w.text)
		}
		if w.key != "" && wordsHelpKey.MatchString(w.key) && len(strings.Fields(w.text)) > 25 {
			fails = append(fails, where+": help over 25 words: "+w.text)
		}
	}
	sort.Strings(fails)
	for _, f := range fails {
		t.Error(f)
	}
}

// THE PAGE NEVER SPEAKS AS THEM (spec section 2 and 8). No string of the
// page's says "I", "me", "my" or "mine": their words are theirs, sent by
// the server, never written into the page.
func TestPageNeverSpeaksAsThem(t *testing.T) {
	for _, w := range pageWords(t) {
		if w.file == "glossary.js" {
			continue
		}
		// The operator's own answer on a button, in their words, not the identity's.
		if w.key == "askVoiceYes" || w.key == "askVoiceNo" {
			continue
		}
		if wordsFirstPerson.MatchString(wordsSlot.ReplaceAllString(w.text, "")) {
			t.Errorf("%s:%d %s speaks as the identity: %s", w.file, w.line, w.key, w.text)
		}
	}
}

// SIGN IN IS A VERB, AND THE TOKEN IS NEVER NAMED. A
// device signs in; nothing the page says calls a thing "the sign-in", and
// no line names the access token, which a person never sees.
var wordsSignInNoun = regexp.MustCompile(`(?i)\b(the|a|this) sign-in\b|\bsign-ins\b|\baccess token\b`)

func TestThePageSaysSignInAsAVerbAndNeverTheToken(t *testing.T) {
	for _, w := range pageWords(t) {
		if wordsSignInNoun.MatchString(w.text) {
			t.Errorf("%s:%d %s: %q", w.file, w.line, w.key, w.text)
		}
	}
}
