package tools

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func (r *Registry) floorRefusal(policy *firewall.Policy, tokens []shellToken, c globbed, denied, base string) string {
	if c.glob == "" {

		for _, tok := range tokens {
			if tok.fragment {
				continue
			}
			if v := policy.Check("bash", tok.text); !v.Allowed && v.Rule != nil && v.Rule.Pattern == denied {
				c.text = tok.text
				break
			}
		}
		return c.text + " reaches the protected name " + denied + "; the sandbox is " + r.sandbox +
			" (only an ABSOLUTE path inside a granted root is exempt from the name check;" +
			" a relative path is matched on the name alone)"
	}
	why := c.said() + " reaches the protected name " + denied + "; the sandbox is " + r.sandbox
	where := "there"
	if base != r.sandbox {
		where = base
	}
	return why + " (a glob is expanded from " + where + " as the shell expands it, in either case;" +
		" if it is text in a program and not a path, this check cannot tell: give the program to its" +
		" interpreter in a here-document with a quoted delimiter, python3 - <<'EOF', which the shell" +
		" does not expand, or in a file the command names)"
}

type globbed struct {
	text, glob string

	literal bool
}

func (g globbed) said() string {
	if g.glob == "" {
		return g.text
	}
	return g.glob + " expands to " + g.text + ", which"
}

func (r *Registry) shellRefusal(cmd string) string {
	why, _ := r.shellWall(cmd)
	return why
}

func (r *Registry) shellWall(cmd string) (why, outside string) {
	return r.shellWallFrom(r.policy.Look(), r.sandbox, cmd)
}

func (r *Registry) shellBase(policy *firewall.Policy, args map[string]any) (string, *Result) {
	v, given := args["cwd"]
	if !given || v == nil || v == "" {
		delete(args, "cwd")
		return r.sandbox, nil
	}
	refuse := func(code, msg string) (string, *Result) {
		res := Refusal(code, msg)
		return "", &res
	}
	cwd, ok := v.(string)
	if !ok {
		return refuse(ReasonArgumentsRequired, "cwd must be a string: the directory to run in")
	}
	dir, rel, err := r.admitOwn(policy, "shell", cwd)
	if err != nil {
		return refuse(ReasonPathRefused, "access denied: cwd "+cwd+": "+err.Error())
	}
	resolved := filepath.Join(dir, rel)

	if r.holdsSubstrate(resolved) {
		return refuse(ReasonPathRefused, "access denied: cwd "+cwd+" "+holdsSubstrateWhy+": the shell starts in a folder that does neither")
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return refuse(ReasonPathRefused, "access denied: cwd "+cwd+" is not a directory: the shell starts in a directory that exists (the sandbox is "+r.sandbox+")")
	}
	args["cwd"] = resolved
	return resolved, nil
}

func referencesHome(cmd string) bool {
	if strings.Contains(cmd, "$HOME") {
		return true
	}
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '~' {
			continue
		}
		if i == 0 {
			return true
		}
		switch p := cmd[i-1]; {
		case p >= 'a' && p <= 'z', p >= 'A' && p <= 'Z', p >= '0' && p <= '9',
			p == '^', p == '@', p == '_', p == '.', p == '-':
		default:
			return true
		}
	}
	return false
}

const ownLogWord = "log"

func gitSubcommandIndex(ws []string) int {
	for i := 1; i < len(ws); i++ {
		if !strings.HasPrefix(ws[i], "-") {
			return i
		}
		if gitValueOptions[ws[i]] {
			i++
		}
	}
	return -1
}

func shellPathlessWords(cmd string, tokens []shellToken) map[int]bool {
	out := make(map[int]bool)

	if strings.ContainsAny(cmd, ";&`\n<>") || strings.Contains(cmd, "$(") || strings.Contains(cmd, "||") {
		return out
	}

	var words []string
	for i, v := range shellVersions(cmd) {
		var found, all []string
		stages, ok := shellStages(cmd, v)
		if !ok {
			return out
		}
		for _, ws := range stages {
			all = append(all, ws...)
			if len(ws) < 2 || !shellWordCommands[ws[0]] {
				continue
			}
			sub := 1
			if ws[0] == "git" {

				if sub = gitSubcommandIndex(ws); sub < 0 {
					continue
				}
			}
			found = append(found, ws[sub])
		}
		for _, word := range found {
			n := 0
			for _, w := range all {
				if w == word {
					n++
				}
			}
			if n != 1 {
				return out
			}
		}

		if i == 0 {
			words = found
		} else if !slices.Equal(words, found) {
			return out
		}
	}
	for _, word := range words {
		if word == "" ||
			strings.ContainsAny(word, "/\\") ||
			strings.Contains(word, "..") ||
			strings.ContainsAny(word, "*?[") ||
			strings.HasPrefix(word, "-") {
			continue
		}
		for i, tok := range tokens {
			if tok.text == word && !tok.cut && !tok.fragment && !tok.literal && !tok.code {
				out[i] = true
			}
		}
	}
	return out
}

func shellStages(cmd string, v bashVersion) (stages [][]string, ok bool) {
	var all []string
	for _, seg := range strings.Split(cmd, "|") {
		ws := shellWords(strings.TrimSpace(seg), v)
		if len(ws) == 0 {
			return nil, false
		}
		stages = append(stages, ws)
		all = append(all, ws...)
	}
	return stages, slices.Equal(all, shellWords(cmd, v))
}

const shellPatternFlags = "cvniwxlLhHrRsqoEFGPzZaIUbUuyk"

func shellPatternWords(cmd string, tokens []shellToken) map[int]bool {
	out := make(map[int]bool)
	if strings.ContainsAny(cmd, ";&`\n<>") || strings.Contains(cmd, "$(") || strings.Contains(cmd, "||") {
		return out
	}

	if strings.Contains(cmd, "''") || strings.Contains(cmd, `""`) {
		return out
	}
	var patterns []string
	for _, v := range shellVersions(cmd) {
		var found []string
		var all []string
		stages, ok := shellStages(cmd, v)
		if !ok {
			return out
		}
		for _, ws := range stages {
			all = append(all, ws...)
			if !shellPatternCommands[filepath.Base(ws[0])] {
				continue
			}
			pattern, after := "", len(ws)
			for i := 1; i < len(ws); i++ {
				w := ws[i]
				if w == "--" {
					if i+1 < len(ws) {
						pattern = ws[i+1]
					}
					break
				}
				if strings.HasPrefix(w, "--") {

					if name, _, has := strings.Cut(w, "="); has && shellPatternSelfArgs[name] {
						continue
					}
					return out
				}
				if strings.HasPrefix(w, "-") {
					for _, c := range w[1:] {
						if !strings.ContainsRune(shellPatternFlags, c) {
							return out
						}
					}
					continue
				}
				pattern, after = w, i+1
				break
			}
			if pattern == "" || strings.HasPrefix(pattern, "-") || strings.Contains(pattern, "..") {
				continue
			}

			for j := after; j < len(ws); j++ {
				w := ws[j]
				if w == "--" {
					break
				}
				if strings.HasPrefix(w, "--") {
					if name, _, has := strings.Cut(w, "="); has && shellPatternSelfArgs[name] {
						continue
					}
					pattern = ""
					break
				}
				if strings.HasPrefix(w, "-") {
					for _, c := range w[1:] {
						if !strings.ContainsRune(shellPatternFlags, c) {
							pattern = ""
						}
					}
				}
			}
			if pattern == "" {
				return out
			}
			found = append(found, pattern)
		}
		for _, p := range found {
			n := 0
			for _, w := range all {
				if w == p {
					n++
				}
			}
			if n != 1 {
				return out
			}
		}
		if patterns == nil {
			patterns = found
		} else if !slices.Equal(patterns, found) {
			return out
		}
	}
	for i, tok := range tokens {
		if slices.Contains(patterns, tok.text) && !tok.cut && !tok.fragment && !tok.literal && !tok.code {
			out[i] = true
		}
	}
	return out
}

func placeSaid(said, text string, rule *firewall.Rule) string {
	base := filepath.Base(rule.Path)
	if text == base {

		if !rule.Stem {
			if fi, err := os.Stat(rule.Path); err == nil {
				if fi.IsDir() {
					return said + " names the directory " + base + "/, which the host keeps: " + rule.Reason
				}
				if fi.Mode().IsRegular() {
					return said + " names the file " + base + ", which the host keeps: " + rule.Reason
				}
			}
		}
		return said + " names " + base + ", a place the host keeps: " + rule.Reason
	}
	return said + " is inside " + base + ", which the host keeps: " + rule.Reason
}

func ownLogCommand(cmd string) bool {
	if strings.ContainsAny(cmd, ";&|<>()`$\\\n") {
		return false
	}
	words := strings.Fields(cmd)
	return len(words) >= 2 && words[0] == "aii" && words[1] == ownLogWord
}

func (r *Registry) shellWallFrom(policy *firewall.Policy, base, cmd string) (why, outside string) {
	why, outside, _ = r.shellWallWord(policy, base, cmd)
	return why, outside
}

func (r *Registry) shellWallWord(policy *firewall.Policy, base, cmd string) (why, outside, literal string) {
	if referencesHome(cmd) {
		return "the command references ~ or $HOME, which this check cannot resolve — write the path out in full (the sandbox is " + r.sandbox + ")", "", ""
	}

	tokens := r.shellTokens(base, cmd)
	if ownLogCommand(cmd) {
		tokens = slices.DeleteFunc(tokens, func(t shellToken) bool { return t.text == ownLogWord })
	}

	if shellWallKeepsPlaces() {
		tokens = slices.DeleteFunc(tokens, func(t shellToken) bool { return t.code })
	}
	off := r.SandboxOff()

	for _, tok := range tokens {
		if rule := firewall.Unplaced(from(base, tok.text)); rule != nil {
			return rule.Reason, "", literalWord(tok.text, tok.literal)
		}
	}

	var checks []globbed
	for _, tok := range tokens {
		if tok.fragment || tok.cut && !tok.name || r.inExtraRoot(from(base, tok.text)) {
			continue
		}
		checks = append(checks, globbed{text: tok.text, literal: tok.literal})
		if !tok.literal {
			for _, m := range r.expandGlobsFrom(base, []string{tok.text}) {
				checks = append(checks, globbed{text: m, glob: tok.text})
			}
		}
	}

	for _, c := range checks {
		if v := policy.Check("bash", c.text); !v.Allowed {
			if v.Err != nil {
				return r.asTold(v.Err).Error(), "", ""
			}

			if v.Rule.Path != "" {
				return placeSaid(c.said(), c.text, v.Rule), "", literalWord(c.text, c.literal)
			}
			return r.floorRefusal(policy, tokens, c, v.Rule.Pattern, base), "", literalWord(c.text, c.literal)
		}
	}

	for _, tok := range tokens {
		if tok.fragment {
			continue
		}
		f := tok.text

		if invocationExempt(f) {
			continue
		}
		if !r.isOutsideSandbox(from(base, f)) {
			continue
		}
		if !off {
			return f + " is outside the sandbox " + r.sandbox + readFrom(r.sandbox, base), f, literalWord(f, tok.literal)
		}

		if !isRooted(f) && escapeShaped(f) {
			return f + " leaves " + base + " by a relative path: a path outside your home and granted roots is written in full", f, literalWord(f, tok.literal)
		}
	}

	var refs []globbed
	pathless := shellPathlessWords(cmd, tokens)
	for i := range shellPatternWords(cmd, tokens) {
		pathless[i] = true
	}
	for i, tok := range tokens {
		if pathless[i] || tok.fragment {
			continue
		}
		refs = append(refs, globbed{text: tok.text, literal: tok.literal})

		if !tok.literal && (!tok.cut || tok.name || off && isRooted(tok.text)) {
			for _, m := range r.expandGlobsFrom(base, []string{tok.text}) {
				refs = append(refs, globbed{text: m, glob: tok.text})
			}
		}
	}
	for _, ref := range refs {
		tok := ref.text
		abs := from(base, tok)
		for _, form := range []string{resolveForContainment(abs), abs} {
			rule, err := policy.Covered(form)
			if err != nil {
				return r.asTold(err).Error(), "", ""
			}
			if rule != nil {
				return placeSaid(ref.said(), ref.text, rule), "", literalWord(ref.text, ref.literal)
			}
		}

		if !r.inExtraRoot(abs) {
			if v := policy.CheckFileIdentity("shell", abs); !v.Allowed {
				return ref.said() + " reaches the protected name " + v.Rule.Pattern + "; the sandbox is " + r.sandbox, "", literalWord(ref.text, ref.literal)
			}
		}

		if off && !invocationExempt(tok) && r.holdsSubstrate(abs) {

			if ref.literal {
				return ref.said() + " " + holdsSubstrateWhy, tok, tok
			}
			return ref.said() + " " + holdsSubstrateWhy + ", and this check cannot see what a command does inside a folder: name a folder that does neither, or use read, grep or ls, which ask about every file they reach", tok, ""
		}
	}
	return "", "", ""
}

func (r *Registry) expandGlobs(tokens []string) []string {
	return r.expandGlobsFrom(r.sandbox, tokens)
}

func (r *Registry) expandGlobsFrom(base string, tokens []string) []string {

	var out []string
	for _, tok := range tokens {
		if !strings.ContainsAny(tok, "*?[") {
			continue
		}
		pattern := tok
		rooted := isRooted(tok)
		if !rooted {
			pattern = filepath.Join(base, tok)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		if lower := strings.ToLower(tok); lower != tok {
			lp := lower
			if rooted {

				meta := strings.IndexAny(tok, "*?[")
				prefix := strings.LastIndexAny(tok[:meta], `/\`) + 1
				lp = tok[:prefix] + strings.ToLower(tok[prefix:])
			}
			if !rooted {
				lp = filepath.Join(base, lower)
			}
			if lm, err := filepath.Glob(lp); err == nil {
				matches = append(matches, lm...)
			}
		}
		for _, m := range matches {
			if !rooted {
				if rel, err := filepath.Rel(base, m); err == nil {
					m = rel
				}
			}
			out = append(out, m)
		}
	}
	return out
}
