// theme_vocab.go — the frame's declared token vocabulary.
//
// theme.json (internal/app) accepts any well-formed CSS custom
// property name. Well-formed is not the same as CONSUMED: `--acent`
// passes every gate — valid name, valid value, stored, transmitted,
// applied to documentElement — and is read by nothing. It is the
// silent-inert class, the same defect as a ui-layout profile the
// frame can never select.
//
// Detecting it needs the vocabulary the frame actually declares, and
// that is theme.css, which THIS package owns (it is inside the
// go:embed). So the answer is derived from the shipped bytes at read
// time rather than duplicated into a Go list that would drift the
// moment a token is added. Nothing to keep in lockstep.
package dashboard

import "strings"

// DeclaredThemeTokens returns the custom properties declared in the
// compiled frame stylesheet's :root block, as a fresh map the caller
// owns. Empty means the stylesheet could not be read or has no :root —
// callers must treat empty as "unknown vocabulary" and NOT as "every
// token is inert", because an accidental empty must not turn a
// diagnostic into a wall of false warnings.
func DeclaredThemeTokens() map[string]bool {
	raw, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		return map[string]bool{}
	}
	return declaredTokensIn(string(raw))
}

// declaredTokensIn is the parser, split out so it can be tested on
// literal CSS rather than only on the shipped file.
func declaredTokensIn(css string) map[string]bool {
	out := map[string]bool{}
	body, ok := rootBlock(stripCSSComments(css))
	if !ok {
		return out
	}
	// Declarations are `--name: value` separated by `;`. Values may
	// themselves contain `:` and `--` (var(--x), gradients), so the
	// name is only ever the text BEFORE the first colon of a segment.
	for _, seg := range strings.Split(body, ";") {
		colon := strings.Index(seg, ":")
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(seg[:colon])
		if strings.HasPrefix(name, "--") && len(name) > 2 {
			out[name] = true
		}
	}
	return out
}

// stripCSSComments removes /* ... */ so commentary that mentions a
// token name cannot be mistaken for a declaration.
func stripCSSComments(css string) string {
	var b strings.Builder
	for {
		i := strings.Index(css, "/*")
		if i < 0 {
			b.WriteString(css)
			return b.String()
		}
		b.WriteString(css[:i])
		rest := css[i+2:]
		j := strings.Index(rest, "*/")
		if j < 0 {
			return b.String() // unterminated comment: the rest is comment
		}
		css = rest[j+2:]
	}
}

// rootBlock returns the text inside the first `:root {...}`, matching
// braces so a nested block cannot end it early.
func rootBlock(css string) (string, bool) {
	i := strings.Index(css, ":root")
	if i < 0 {
		return "", false
	}
	open := strings.Index(css[i:], "{")
	if open < 0 {
		return "", false
	}
	start := i + open + 1
	depth := 1
	for k := start; k < len(css); k++ {
		switch css[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[start:k], true
			}
		}
	}
	return "", false
}
