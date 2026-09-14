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
package dashboard

import "strings"

// .
// .
// .
// .
// .
// .
func DeclaredThemeTokens() map[string]bool {
	raw, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		return map[string]bool{}
	}
	return declaredTokensIn(string(raw))
}

// .
// .
func declaredTokensIn(css string) map[string]bool {
	out := map[string]bool{}
	body, ok := rootBlock(stripCSSComments(css))
	if !ok {
		return out
	}
	// .
	// .
	// .
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

// .
// .
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
			return b.String()
		}
		css = rest[j+2:]
	}
}

// .
// .
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
