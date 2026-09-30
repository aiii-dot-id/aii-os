package store

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func declaredTableSQL(sqlText string) map[string]string {
	d, err := referenceSchema(sqlText)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for name, statement := range d.Objects["table"] {
		if d.Tables[name].Kind == "table" {
			out[name] = statement
		}
	}
	return out
}

func declaredIndexSQL(sqlText string) map[string][]string {
	out := map[string][]string{}
	decls := declaredIndexStatements(sqlText)
	for _, name := range sortedKeys(decls) {
		d := decls[name]
		out[d.Table] = append(out[d.Table], d.SQL)
	}
	return out
}

var replacesLegacyRe = regexp.MustCompile(`(?i)^--\s*replaces-legacy-when-column:\s*([a-z_][a-z_0-9]*)`)

var derivedPredecessorRe = regexp.MustCompile(`(?i)^--\s*replaces-derived-table:\s*([a-z_][a-z_0-9]*)`)

func declaredDerivedPredecessors(text string) map[string]string {
	out := map[string]string{}
	for table, body := range rawTableBodies(text) {
		for _, annotation := range schemaComments(body) {
			if m := derivedPredecessorRe.FindStringSubmatch(annotation.text); m != nil {
				out[table] = sqliteName(m[1])
			}
		}
	}
	return out
}

func validateSchemaAnnotations(text string, ref *schemaDescription) error {
	predecessors := map[string]string{}
	prov := lenientProvenance(text)
	for table, statement := range declaredTableSQL(text) {
		count := 0
		for _, annotation := range schemaComments(statement) {
			if m := derivedPredecessorRe.FindStringSubmatch(annotation.text); m != nil {
				count++
				previous := sqliteName(m[1])
				if count != 1 || prov[table] != Derived || previous == table {
					return fmt.Errorf("invalid derived predecessor for %s", table)
				}
				if _, declared := ref.Tables[previous]; declared {
					return fmt.Errorf("derived predecessor %s is still declared", previous)
				}
				if other, used := predecessors[previous]; used {
					return fmt.Errorf("derived predecessor %s is claimed by both %s and %s", previous, other, table)
				}
				predecessors[previous] = table
			}
		}
	}
	for table, renames := range declaredRenames(text) {
		sources := map[string]string{}
		for target, source := range renames {
			if _, still := ref.Shapes[table][source]; still && source != target {
				return fmt.Errorf("rename %s.%s still declares its source %s", table, target, source)
			}
			if other, used := sources[source]; used {
				return fmt.Errorf("columns %s.%s and %s both declare renamed-from: %s", table, target, other, source)
			}
			sources[source] = target
		}
	}
	return nil
}

func declaredReplacements(sqlText string) map[string]string {
	out := map[string]string{}
	for table, body := range rawTableBodies(sqlText) {
		for _, annotation := range schemaComments(body) {
			if m := replacesLegacyRe.FindStringSubmatch(annotation.text); m != nil {
				out[table] = strings.ToLower(m[1])
			}
		}
	}
	return out
}

var renamedFromRe = regexp.MustCompile(`(?i)^--\s*renamed-from:\s*([a-z_][a-z_0-9]*)`)

func declaredRenames(sqlText string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for table, body := range rawTableBodies(sqlText) {
		for _, annotation := range schemaComments(body) {
			m := renamedFromRe.FindStringSubmatch(annotation.text)
			if m == nil {
				continue
			}

			decl := annotation.prefix
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

func rawTableBodies(sqlText string) map[string]string {

	return declaredTableSQL(sqlText)
}

var (
	createBlockRe = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ([a-z_0-9]+)\s*\((.*?)\n\)[^\n;]*;`)
	provenanceRe  = regexp.MustCompile(`(?i)^--\s*provenance:\s*(derived|ephemeral)\s*(?:,\s*clear-order\s+([0-9]+))?\s*$`)
)

func parseProvenance(schemaText string) (map[string]TableEntry, error) {
	out := map[string]TableEntry{}
	orders := map[int]string{}
	for _, m := range createBlockRe.FindAllStringSubmatch(schemaText, -1) {
		name, body := m[1], m[2]
		var found []TableEntry
		for _, annotation := range schemaComments(body) {
			pm := provenanceRe.FindStringSubmatch(annotation.text)
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

var (
	sidecarDeclRe = regexp.MustCompile(`(?im)^[ \t]*--[ \t]*provenance:[ \t]*sidecar[ \t]+of[ \t]+([a-z_0-9]+)[ \t]*\r?\n[ \t]*CREATE[ \t]+VIRTUAL[ \t]+TABLE[ \t]+IF[ \t]+NOT[ \t]+EXISTS[ \t]+([a-z_0-9]+)[ \t]+USING[ \t]+fts5\(([^\r\n]*)\)[ \t]*;`)
	virtualHeadRe = regexp.MustCompile(`(?i)CREATE\s+VIRTUAL\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s+USING\s+([a-z0-9]+)\s*\(`)
	fts5ContentRe = regexp.MustCompile(`(?i)\bcontent\s*=\s*'([a-z_0-9]*)'`)
	viewHeadRe    = regexp.MustCompile(`(?is)CREATE\s+VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s+AS\s+(.*?);`)
	ifNotExistsRe = regexp.MustCompile(`(?i)\s+IF\s+NOT\s+EXISTS\s+`)
)

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

func declaredVirtualSQL(sqlText string) map[string]string {
	d, err := referenceSchema(sqlText)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for name, statement := range d.Objects["table"] {
		if d.Tables[name].Kind == "virtual" {
			out[name] = statement
		}
	}
	return out
}

func declaredTriggerSQL(sqlText string) map[string][]string {
	d, err := referenceSchema(sqlText)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, name := range sortedKeys(d.Objects["trigger"]) {
		table := d.Owners["trigger:"+name]
		out[table] = append(out[table], d.Objects["trigger"][name])
	}
	return out
}

func normalizeDeclaration(s string) string {
	s = ifNotExistsRe.ReplaceAllString(s, " ")
	s = strings.TrimSuffix(strings.TrimSpace(s), ";")
	return strings.Join(strings.Fields(s), " ")
}

func canonDDL(s string) string {
	text := stripSQLComments(s)

	if loc := createTableHead.FindStringSubmatchIndex(text); loc != nil {
		name := text[loc[2]:loc[3]]
		if len(name) > 1 {
			switch name[0] {
			case '"', '`':
				q := name[:1]
				name = strings.ReplaceAll(name[1:len(name)-1], q+q, q)
			case '[':
				name = name[1 : len(name)-1]
			}
		}
		text = "CREATE TABLE " + quoteIdentifier(sqliteName(name)) + text[loc[1]:]
	}
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
			for j < len(r) {
				if r[j] == end {
					if end != ']' && j+1 < len(r) && r[j+1] == end {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(r) {
				toks = append(toks, string(r[i:]))
				i = len(r)
				continue
			}
			toks = append(toks, string(r[i:j+1]))
			i = j + 1
		case unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.':
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_' || r[j] == '.') {
				j++
			}
			toks = append(toks, sqliteName(string(r[i:j])))
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

func stripSQLComments(s string) string {
	return visitSQLComments(s, nil)
}

type schemaComment struct{ prefix, text string }

func schemaComments(s string) []schemaComment {
	var out []schemaComment
	visitSQLComments(s, func(prefix, text string) { out = append(out, schemaComment{prefix, text}) })
	return out
}

func visitSQLComments(s string, visit func(string, string)) string {
	var out strings.Builder
	var quote byte
	lineStart := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' {
			lineStart = i + 1
		}
		if quote != 0 {
			out.WriteByte(c)
			if c == quote {
				if quote != ']' && i+1 < len(s) && s[i+1] == quote {
					i++
					out.WriteByte(s[i])
				} else {
					quote = 0
				}
			}
			continue
		}
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote = c
			out.WriteByte(c)
		case c == '[':
			quote = ']'
			out.WriteByte(c)
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			start := i
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if visit != nil {
				visit(s[lineStart:start], s[start:i])
			}
			lineStart = i + 1
			out.WriteByte('\n')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				if s[i] == '\n' {
					lineStart = i + 1
				}
				i++
			}
			i++
			out.WriteByte(' ')
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

type indexDecl struct {
	Table string
	SQL   string
}

func declaredIndexStatements(sqlText string) map[string]indexDecl {
	d, err := referenceSchema(sqlText)
	if err != nil {
		return nil
	}
	out := map[string]indexDecl{}
	for name, statement := range d.Objects["index"] {
		out[name] = indexDecl{Table: d.Owners["index:"+name], SQL: statement}
	}
	return out
}

func declaredTriggerStatements(sqlText string) map[string]string {
	return schemaObjects(sqlText, "trigger")
}

func declaredViewSQL(sqlText string) map[string]string {
	return schemaObjects(sqlText, "view")
}

var retiredTableRe = regexp.MustCompile(`(?im)^\s*--\s*retired-table:\s*([a-z_][a-z_0-9]*)\b`)

func declaredRetiredTables(sqlText string) []string {
	seen := map[string]bool{}
	var out []string
	for _, annotation := range schemaComments(sqlText) {
		m := retiredTableRe.FindStringSubmatch(annotation.text)
		if m == nil {
			continue
		}
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

func lenientProvenance(sqlText string) map[string]Provenance {
	out := map[string]Provenance{}
	for table, body := range rawTableBodies(sqlText) {
		for _, annotation := range schemaComments(body) {
			pm := provenanceRe.FindStringSubmatch(annotation.text)
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
