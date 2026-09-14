package store

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
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
// .
func declaredTableSQL(sqlText string) map[string]string {
	out := map[string]string{}
	for _, m := range tableHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := strings.ToLower(sqlText[m[2]:m[3]])
		rest := sqlText[m[1]-1:]
		body, ok := balancedBody(rest)
		if !ok {
			continue
		}
		// .
		// .
		after := rest[len(body)+2:]
		if semi := strings.Index(after, ";"); semi >= 0 {
			after = after[:semi]
		}
		after = strings.TrimSpace(stripSQLComments(after))
		opts := ""
		if after != "" && tableOptionsRe.MatchString(after) {
			opts = " " + strings.ToUpper(strings.Join(strings.Fields(after), " "))
		}
		out[name] = "CREATE TABLE " + name + " (" + body + ")" + opts
	}
	return out
}

// .
var indexHeadRe = regexp.MustCompile(`(?is)CREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s+ON\s+([a-z_0-9]+)\s*\(`)

// .
// .
// .
// .
func declaredIndexSQL(sqlText string) map[string][]string {
	out := map[string][]string{}
	decls := declaredIndexStatements(sqlText)
	for _, name := range sortedKeys(decls) {
		d := decls[name]
		out[d.Table] = append(out[d.Table], d.SQL)
	}
	return out
}

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
// .
// .
// .
// .
var replacesLegacyRe = regexp.MustCompile(`(?i)--\s*replaces-legacy-when-column:\s*([a-z_][a-z_0-9]*)`)

// .
// .
func declaredReplacements(sqlText string) map[string]string {
	out := map[string]string{}
	for table, body := range rawTableBodies(sqlText) {
		for _, line := range strings.Split(body, "\n") {
			if m := replacesLegacyRe.FindStringSubmatch(line); m != nil {
				out[table] = strings.ToLower(m[1])
			}
		}
	}
	return out
}

var renamedFromRe = regexp.MustCompile(`(?i)--\s*renamed-from:\s*([a-z_][a-z_0-9]*)`)

// .
// .
// .
// .
// .
func declaredRenames(sqlText string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for table, body := range rawTableBodies(sqlText) {
		for _, line := range strings.Split(body, "\n") {
			m := renamedFromRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// .
			// .
			// .
			decl := line
			if i := strings.Index(decl, "--"); i >= 0 {
				decl = decl[:i]
			}
			nm := leadingIdentRe.FindStringSubmatch(strings.TrimSpace(decl))
			if nm == nil {
				continue
			}
			newCol := strings.ToLower(nm[1])
			if tableConstraint[newCol] {
				continue
			}
			if out[table] == nil {
				out[table] = map[string]string{}
			}
			out[table][newCol] = strings.ToLower(m[1])
		}
	}
	return out
}

// .
// .
// .
func rawTableBodies(sqlText string) map[string]string {
	out := map[string]string{}
	for _, loc := range tableHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := strings.ToLower(sqlText[loc[2]:loc[3]])
		if body, ok := balancedBody(sqlText[loc[1]-1:]); ok {
			out[name] = body
		}
	}
	return out
}

// .
// .
// .
// .
// .

var (
	createBlockRe = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ([a-z_0-9]+)\s*\((.*?)\n\)[^\n;]*;`)
	provenanceRe  = regexp.MustCompile(`(?i)^--\s*provenance:\s*(derived|ephemeral)\s*(?:,\s*clear-order\s+([0-9]+))?\s*$`)
)

// .
// .
func parseProvenance(schemaText string) (map[string]TableEntry, error) {
	out := map[string]TableEntry{}
	orders := map[int]string{}
	for _, m := range createBlockRe.FindAllStringSubmatch(schemaText, -1) {
		name, body := m[1], m[2]
		var found []TableEntry
		for _, line := range strings.Split(body, "\n") {
			pm := provenanceRe.FindStringSubmatch(strings.TrimSpace(line))
			if pm == nil {
				continue
			}
			e := TableEntry{}
			if strings.EqualFold(pm[1], "derived") {
				e.Provenance = Derived
			} else {
				e.Provenance = Ephemeral
			}
			if pm[2] != "" {
				n, err := strconv.Atoi(pm[2])
				if err != nil || n <= 0 {
					return nil, fmt.Errorf("table %s: clear-order %q is not a positive integer", name, pm[2])
				}
				e.ClearOrder = n
			}
			found = append(found, e)
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("table %s declares %d provenance lines; exactly one is required (-- provenance: derived, clear-order N | -- provenance: ephemeral)", name, len(found))
		}
		e := found[0]
		switch e.Provenance {
		case Derived:
			if e.ClearOrder == 0 {
				return nil, fmt.Errorf("derived table %s declares no clear-order", name)
			}
			if other, dup := orders[e.ClearOrder]; dup {
				return nil, fmt.Errorf("derived tables %s and %s both declare clear-order %d", other, name, e.ClearOrder)
			}
			orders[e.ClearOrder] = name
		case Ephemeral:
			if e.ClearOrder != 0 {
				return nil, fmt.Errorf("ephemeral table %s declares a clear-order; only derived tables are cleared", name)
			}
		}
		e.AlsoWrittenBy = writers[name]
		out[name] = e
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tables declared")
	}
	mirror, ok := out["ledger"]
	if !ok || mirror.Provenance != Derived {
		return nil, fmt.Errorf("the mirror (ledger) must be declared derived")
	}
	for name, e := range out {
		if e.Provenance == Derived && e.ClearOrder > mirror.ClearOrder {
			return nil, fmt.Errorf("derived table %s clears after the mirror; the mirror clears last", name)
		}
	}
	return out, nil
}

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
// .
// .

var (
	sidecarDeclRe = regexp.MustCompile(`(?im)^[ \t]*--[ \t]*provenance:[ \t]*sidecar[ \t]+of[ \t]+([a-z_0-9]+)[ \t]*\r?\n[ \t]*CREATE[ \t]+VIRTUAL[ \t]+TABLE[ \t]+IF[ \t]+NOT[ \t]+EXISTS[ \t]+([a-z_0-9]+)[ \t]+USING[ \t]+fts5\(([^\r\n]*)\)[ \t]*;`)
	virtualHeadRe = regexp.MustCompile(`(?i)CREATE\s+VIRTUAL\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s+USING\s+([a-z0-9]+)\s*\(`)
	fts5ContentRe = regexp.MustCompile(`(?i)\bcontent\s*=\s*'([a-z_0-9]*)'`)
	viewHeadRe    = regexp.MustCompile(`(?is)CREATE\s+VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s+AS\s+(.*?);`)
	triggerHeadRe = regexp.MustCompile(`(?is)CREATE\s+TRIGGER\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s+(?:BEFORE|AFTER|INSTEAD\s+OF)\s+(?:INSERT|DELETE|UPDATE(?:\s+OF\s+[a-z_0-9]+(?:\s*,\s*[a-z_0-9]+)*)?)\s+ON\s+([a-z_0-9]+)\b`)
	triggerEndRe  = regexp.MustCompile(`(?is)\bEND\s*;`)
	ifNotExistsRe = regexp.MustCompile(`(?i)\s+IF\s+NOT\s+EXISTS\s+`)
)

// .
// .
// .
// .
// .
// .
func parseSidecars(schemaText string, catalog map[string]TableEntry) (map[string]SidecarEntry, error) {
	views := map[string]string{}
	for _, m := range viewHeadRe.FindAllStringSubmatch(schemaText, -1) {
		views[strings.ToLower(m[1])] = m[2]
	}
	out := map[string]SidecarEntry{}
	for _, m := range sidecarDeclRe.FindAllStringSubmatch(schemaText, -1) {
		base, name, args := strings.ToLower(m[1]), strings.ToLower(m[2]), strings.TrimSpace(m[3])
		if _, ok := catalog[base]; !ok {
			return nil, fmt.Errorf("sidecar %s: base %s is not a catalogued table", name, base)
		}
		if !strings.HasPrefix(name, base+"_") {
			return nil, fmt.Errorf("sidecar %s must carry its base's name (%s_...)", name, base)
		}
		cm := fts5ContentRe.FindStringSubmatch(args)
		if cm == nil || cm[1] == "" {
			return nil, fmt.Errorf("sidecar %s must read its rows from %s (content='%s' or a view over it); an index that copies the text is not a sidecar", name, base, base)
		}
		content := strings.ToLower(cm[1])
		if content != base {
			body, isView := views[content]
			if !isView {
				return nil, fmt.Errorf("sidecar %s reads content=%q, which is neither its base %s nor a declared view", name, content, base)
			}
			if !regexp.MustCompile(`(?i)\bFROM\s+` + base + `\b`).MatchString(body) {
				return nil, fmt.Errorf("sidecar %s reads view %s, which does not select from its base %s", name, content, base)
			}
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("sidecar %s is declared twice", name)
		}
		out[name] = SidecarEntry{Base: base, Content: content, Args: args}
	}
	for _, m := range virtualHeadRe.FindAllStringSubmatch(schemaText, -1) {
		name := strings.ToLower(m[1])
		if !strings.EqualFold(m[2], "fts5") {
			return nil, fmt.Errorf("virtual table %s uses %s; only fts5 sidecars are declared", name, m[2])
		}
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("virtual table %s has no provenance line (-- provenance: sidecar of <base>, on the line before it)", name)
		}
	}
	return out, nil
}

// .
// .
// .
func declaredVirtualSQL(sqlText string) map[string]string {
	out := map[string]string{}
	for _, m := range virtualHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := strings.ToLower(sqlText[m[2]:m[3]])
		rest := sqlText[m[0]:]
		end := strings.Index(rest, ";")
		if end < 0 {
			continue
		}
		out[name] = strings.TrimSpace(rest[:end])
	}
	return out
}

// .
// .
// .
// .
// .
func declaredTriggerSQL(sqlText string) map[string][]string {
	out := map[string][]string{}
	for _, m := range triggerHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		table := strings.ToLower(sqlText[m[4]:m[5]])
		rest := sqlText[m[0]:]
		end := triggerEndRe.FindStringIndex(rest)
		if end == nil {
			continue
		}
		out[table] = append(out[table], rest[:end[1]])
	}
	return out
}

// .
// .
// .
func normalizeDeclaration(s string) string {
	s = ifNotExistsRe.ReplaceAllString(s, " ")
	s = strings.TrimSuffix(strings.TrimSpace(s), ";")
	return strings.Join(strings.Fields(s), " ")
}

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
func canonDDL(s string) string {
	text := stripSQLComments(s)
	var toks []string
	r := []rune(text)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '\'':
			var lit strings.Builder
			lit.WriteRune('\'')
			j := i + 1
			for j < len(r) {
				if r[j] == '\'' {
					if j+1 < len(r) && r[j+1] == '\'' {
						lit.WriteString("''")
						j += 2
						continue
					}
					break
				}
				lit.WriteRune(r[j])
				j++
			}
			lit.WriteRune('\'')
			toks = append(toks, lit.String())
			i = j + 1
		case c == '"' || c == '`' || c == '[':
			end := c
			if c == '[' {
				end = ']'
			}
			j := i + 1
			for j < len(r) && r[j] != end {
				j++
			}
			toks = append(toks, strings.ToLower(string(r[i+1:j])))
			i = j + 1
		case unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.':
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_' || r[j] == '.') {
				j++
			}
			toks = append(toks, strings.ToLower(string(r[i:j])))
			i = j
		default:
			toks = append(toks, string(c))
			i++
		}
	}
	out := make([]string, 0, len(toks))
	for k := 0; k < len(toks); k++ {
		if k+2 < len(toks) && toks[k] == "if" && toks[k+1] == "not" && toks[k+2] == "exists" {
			k += 2
			continue
		}
		out = append(out, toks[k])
	}
	for len(out) > 0 && out[len(out)-1] == ";" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, " ")
}

// .
// .
func stripSQLComments(s string) string {
	var b strings.Builder
	r := []rune(s)
	inStr := false
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case inStr:
			b.WriteRune(c)
			if c == '\'' {
				inStr = false
			}
		case c == '\'':
			inStr = true
			b.WriteRune(c)
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
			b.WriteRune('\n')
		case c == '/' && i+1 < len(r) && r[i+1] == '*':
			j := i + 2
			for j+1 < len(r) && !(r[j] == '*' && r[j+1] == '/') {
				j++
			}
			i = j + 1
			b.WriteRune(' ')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// .
// .
// .
func statementEnd(s string) int {
	depth := 0
	inStr := false
	for i, c := range s {
		switch {
		case inStr:
			if c == '\'' {
				inStr = false
			}
		case c == '\'':
			inStr = true
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ';' && depth <= 0:
			return i
		}
	}
	return len(s)
}

// .
var tableOptionsRe = regexp.MustCompile(`(?i)^(strict|without\s+rowid)(\s*,\s*(strict|without\s+rowid))?$`)

// .
// .
// .
// .
// .
type indexDecl struct {
	Table string
	SQL   string
}

// .
func declaredIndexStatements(sqlText string) map[string]indexDecl {
	out := map[string]indexDecl{}
	for _, m := range indexHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := strings.ToLower(sqlText[m[4]:m[5]])
		table := strings.ToLower(sqlText[m[6]:m[7]])
		rest := sqlText[m[0]:]
		out[name] = indexDecl{Table: table, SQL: strings.TrimSpace(rest[:statementEnd(rest)])}
	}
	return out
}

// .
// .
func declaredTriggerStatements(sqlText string) map[string]string {
	out := map[string]string{}
	for _, m := range triggerHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := strings.ToLower(sqlText[m[2]:m[3]])
		rest := sqlText[m[0]:]
		end := triggerEndRe.FindStringIndex(rest)
		if end == nil {
			continue
		}
		out[name] = rest[:end[1]]
	}
	return out
}

// .
// .
func declaredViewSQL(sqlText string) map[string]string {
	out := map[string]string{}
	for _, m := range viewHeadRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := strings.ToLower(sqlText[m[2]:m[3]])
		rest := sqlText[m[0]:]
		out[name] = strings.TrimSpace(rest[:statementEnd(rest)])
	}
	return out
}

// .
// .
// .
// .
var retiredTableRe = regexp.MustCompile(`(?im)^\s*--\s*retired-table:\s*([a-z_][a-z_0-9]*)\b`)

// .
func declaredRetiredTables(sqlText string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range retiredTableRe.FindAllStringSubmatch(sqlText, -1) {
		name := strings.ToLower(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// .
// .
// .
// .
// .
// .
func lenientProvenance(sqlText string) map[string]Provenance {
	out := map[string]Provenance{}
	for table, body := range rawTableBodies(sqlText) {
		for _, line := range strings.Split(body, "\n") {
			pm := provenanceRe.FindStringSubmatch(strings.TrimSpace(line))
			if pm == nil {
				continue
			}
			if strings.EqualFold(pm[1], "derived") {
				out[table] = Derived
			} else {
				out[table] = Ephemeral
			}
		}
	}
	return out
}

// .
// .
// .
func sidecarsIn(sqlText string) map[string]SidecarEntry {
	catalog := map[string]TableEntry{}
	for name := range declaredTableSQL(sqlText) {
		catalog[name] = TableEntry{}
	}
	sc, err := parseSidecars(sqlText, catalog)
	if err != nil {
		return map[string]SidecarEntry{}
	}
	return sc
}
