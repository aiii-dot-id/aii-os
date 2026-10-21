package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxBriefFamilies = 16

	MaxFamilyNames = 6

	DefaultSearchLimit = 8
	MaxSearchLimit     = 16
)

type Discovery struct {
	Plugin         string
	Version        string
	Tier           string
	Operation      string
	Family         string
	Summary        string
	Effects        string
	Capabilities   []string
	MaxResultBytes int
	Keywords       []string
	Examples       []string
	Primary        bool

	OperatorConfirms bool

	hostWords bool
}

type Discoverable interface {
	Discovery() Discovery
}

var nameGrammar = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

const MaxNameBytes = 64

var versionGrammar = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)

const UnprintedVersion = "(a version this host does not print)"

func PrintableName(name string) bool {
	return len(name) <= MaxNameBytes && nameGrammar.MatchString(name)
}

var ReservedIDWords = []string{"host", "system", "manager", "broker", "registry", "runtime", "facility", "sandbox", "operator", "aii", "aiios"}

func ReservedPluginID(id string) string {
	first := id
	if i := strings.IndexAny(id, ".-_"); i >= 0 {
		first = id[:i]
	}
	for _, w := range ReservedIDWords {
		if first == w {
			return w
		}
	}
	return ""
}

const MaxSummaryBytes = 256

func AuthorLine(plugin, summary, tool string) string {
	return "plugin " + plugin + " says: " + boundedQuote(summary, tool)
}

func boundedQuote(text, tool string) string {
	shown := text
	if len(shown) > MaxSummaryBytes {
		cut := MaxSummaryBytes
		for cut > 0 && !utf8.RuneStart(shown[cut]) {
			cut--
		}
		shown = shown[:cut]
	}
	quoted := quoteAuthor(shown)
	if len(shown) < len(text) {
		quoted += fmt.Sprintf(" (the first %d of its %d bytes; tools action=show name=%s shows it whole)", len(shown), len(text), tool)
	}
	return quoted
}

type ModelSchemer interface {
	ModelParameters() map[string]any
}

var formatToken = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func ModelSchema(schema map[string]any, tool string) (model map[string]any, prose bool) {
	model = make(map[string]any, len(schema))
	for k, v := range schema {
		switch node := v.(type) {
		case string:
			switch {
			case (k == "description" || k == "title") && node != "":
				v, prose = boundedQuote(node, tool), true
			case k == "$id" || k == "$schema":

				continue
			case k == "format" && !formatToken.MatchString(node):

				continue
			}
		case map[string]any:
			switch k {
			case "items":
				sub, has := ModelSchema(node, tool)
				v, prose = sub, prose || has
			case "properties":

				props := make(map[string]any, len(node))
				for name, p := range node {
					if ps, ok := p.(map[string]any); ok {
						sub, has := ModelSchema(ps, tool)
						p, prose = sub, prose || has
					}
					props[name] = p
				}
				v = props
			}
		}
		model[k] = v
	}
	return model, prose
}

func authorWords(plugin, text string) string {
	return "plugin " + plugin + " says: " + quoteAuthor(text)
}

const primes = "\u02ba\u02ee\u2033\u2036\u3003"

func quoteAuthor(s string) string {
	var b strings.Builder
	for _, r := range strconv.Quote(s) {
		switch {
		case r <= unicode.MaxASCII || !(unicode.Is(unicode.Quotation_Mark, r) || strings.ContainsRune(primes, r)):
			b.WriteRune(r)
		case r > 0xffff:
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

func (r *Registry) Declared(name string) (operation, summary string, ok bool) {
	t, found, _, origin := r.lookupWithHostOnly(name)
	if !found || origin == "builtin" {
		return "", "", false
	}
	if d, is := t.(Discoverable); is {
		disc := d.Discovery()
		operation, summary = disc.Operation, disc.Summary
	}
	return operation, summary, true
}

func FamilyOf(operation string) string {
	if i := strings.IndexByte(operation, '.'); i > 0 {
		return operation[:i]
	}
	return operation
}

func familyOf(plugin, operation string) string {
	if strings.IndexByte(operation, '.') > 0 || plugin == "" {
		return FamilyOf(operation)
	}
	if i := strings.LastIndexByte(plugin, '.'); i >= 0 && i < len(plugin)-1 {
		return plugin[i+1:]
	}
	return plugin
}

func ReceiptRule(effects string) string {
	switch effects {
	case "read.internal":
		return "declares an internal read; check the returned data, its scope and coverage before relying on it"
	case "read.external":
		return "for brokered reads, check the host outcome and returned source data; a refused or failed read does not establish retrieval"
	case "write.local":
		return "for brokered writes, check the host outcome and the operation's write result and persistence scope; a receipt can record a refusal"
	case "write.external":
		return "for brokered external effects, check the host outcome and evidence of the claimed effect; a successful invocation alone does not prove that effect"
	case "exec":
		return "trusted native only; inspect execution outcome and returned results, then verify the claimed task effects"
	}
	return "undeclared effect class: treat every claim in the result as unproven"
}

type Family struct {
	Name  string
	Count int
	Names []string
	More  int
}

type Brief struct {
	Total        int
	Unavailable  int
	Families     []Family
	MoreFamilies int
}

type Hit struct {
	Name      string
	Operation string
	Plugin    string
	Summary   string
	Effects   string
	Primary   bool
}

type Card struct {
	Name           string
	Operation      string
	Plugin         string
	Version        string
	Tier           string
	Family         string
	Summary        string
	Effects        string
	Capabilities   []string
	MaxResultBytes int
	Examples       []string
	Primary        bool
	Reason         string
	Receipt        string
	Parameters     map[string]any
}

type dynEntry struct {
	name string
	tool Tool
	disc Discovery
}

func (e dynEntry) line() string {
	if e.disc.hostWords {
		return e.disc.Summary
	}
	return AuthorLine(e.disc.Plugin, e.disc.Summary, e.name)
}

func (r *Registry) dynamicEntries() []dynEntry {
	r.regMu.RLock()
	out := make([]dynEntry, 0, len(r.tools))
	for name, t := range r.tools {
		src := r.sources[name]
		if src == "" || src == "builtin" || r.hostOnly[name] {
			continue
		}
		out = append(out, dynEntry{name: name, tool: t, disc: discoveryOf(name, t, src)})
	}
	r.regMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func discoveryOf(name string, t Tool, origin string) Discovery {
	var d Discovery
	if dd, ok := t.(Discoverable); ok {
		d = dd.Discovery()
	}
	if d.Plugin == "" {
		d.Plugin = origin
	}

	if !PrintableName(d.Operation) {
		d.Operation = name
	}

	if !PrintableName(d.Family) {
		d.Family = familyOf(d.Plugin, d.Operation)
	}
	if d.Version != "" && !(len(d.Version) <= MaxNameBytes && versionGrammar.MatchString(d.Version)) {
		d.Version = UnprintedVersion
	}
	if d.Summary == "" {
		d.Summary, d.hostWords = t.Description(), true
	}
	return d
}

func (r *Registry) HasDynamic() bool {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	for name := range r.tools {
		if src := r.sources[name]; src != "" && src != "builtin" && !r.hostOnly[name] {
			return true
		}
	}
	return false
}

func (r *Registry) PromptNames(room int) (names []string, unlisted int) {
	r.regMu.RLock()
	var builtins, primaries []string
	for name, t := range r.tools {
		switch src := r.sources[name]; {
		case r.hostOnly[name]:
		case src == "builtin":
			builtins = append(builtins, name)
		case discoveryOf(name, t, src).Primary:
			primaries = append(primaries, name)
		}
	}
	r.regMu.RUnlock()
	sort.Strings(builtins)
	sort.Strings(primaries)
	for _, name := range builtins {
		if r.ToolEnabled(name) {
			names = append(names, name)
		}
	}
	for _, name := range primaries {
		if code, _, _ := r.Unavailable(name); code != "" {
			continue
		}
		if len(names) >= room {
			unlisted++
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, unlisted
}

const (
	ReasonOperatorDisabled = "OPERATOR_DISABLED"
	ReasonSafeSuspended    = "SAFE_SUSPENDED"
	ReasonNotInToolList    = "NOT_IN_TOOL_LIST"
)

func (r *Registry) Unavailable(name string) (code, reason string, ok bool) {
	r.regMu.RLock()
	_, exists := r.tools[name]
	src := r.sources[name]
	host := r.hostOnly[name]
	r.regMu.RUnlock()
	if !exists || src == "" || src == "builtin" || host {
		return "", "", false
	}
	if !r.ToolEnabled(name) {
		return ReasonOperatorDisabled, "disabled by your operator", true
	}
	if r.safeSource != nil {
		if why, safe := r.safeSource(); safe {
			return ReasonSafeSuspended, "suspended in safe mode: " + why, true
		}
	}
	return "", "", true
}

func (r *Registry) Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("name a plugin operation: its tool name, or its operation id")
	}
	entries := r.dynamicEntries()
	for _, e := range entries {
		if e.name == ref {
			return e.name, nil
		}
	}
	var matches []string
	for _, e := range entries {
		if e.disc.Operation == ref {
			matches = append(matches, e.name)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("%s is not a plugin operation installed beside you", ref)
	}
	return "", fmt.Errorf("%s is declared by more than one plugin — name one of %s", ref, strings.Join(matches, ", "))
}

func (r *Registry) Brief() Brief {
	var b Brief
	fams := map[string]*Family{}
	entries := r.dynamicEntries()

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].disc.Primary && !entries[j].disc.Primary })
	for _, e := range entries {
		if code, _, _ := r.Unavailable(e.name); code != "" {
			b.Unavailable++
			continue
		}
		b.Total++
		f := fams[e.disc.Family]
		if f == nil {
			f = &Family{Name: e.disc.Family}
			fams[e.disc.Family] = f
		}
		f.Count++
		if len(f.Names) < MaxFamilyNames {
			f.Names = append(f.Names, e.disc.Operation)
		} else {
			f.More++
		}
	}
	names := make([]string, 0, len(fams))
	for n := range fams {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if len(b.Families) >= MaxBriefFamilies {
			b.MoreFamilies++
			continue
		}
		b.Families = append(b.Families, *fams[n])
	}
	return b
}

func (r *Registry) Search(query string, limit int) []Hit {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	type scored struct {
		hit   Hit
		score int
	}
	var out []scored
	for _, e := range r.dynamicEntries() {
		if code, _, _ := r.Unavailable(e.name); code != "" {
			continue
		}
		score := 0
		opSegs := tokenize(e.disc.Operation)
		summaryWords := tokenize(e.disc.Summary)
		pluginSegs := tokenize(e.disc.Plugin)
		for _, tok := range tokens {
			if tok == strings.ToLower(e.disc.Family) {
				score += 3
			}
			switch {
			case containsWord(opSegs, tok):
				score += 3
			case strings.Contains(strings.ToLower(e.disc.Operation), tok):
				score++
			}
			switch {
			case containsWord(summaryWords, tok):
				score += 2
			case strings.Contains(strings.ToLower(e.disc.Summary), tok):
				score++
			}
			for _, k := range e.disc.Keywords {
				if strings.ToLower(k) == tok {
					score += 2
				}
			}
			if containsWord(pluginSegs, tok) {
				score++
			}
		}
		if score > 0 {
			out = append(out, scored{score: score, hit: Hit{Name: e.name, Operation: e.disc.Operation, Plugin: e.disc.Plugin, Summary: e.line(), Effects: e.disc.Effects, Primary: e.disc.Primary}})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].hit.Name < out[j].hit.Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	hits := make([]Hit, 0, len(out))
	for _, s := range out {
		hits = append(hits, s.hit)
	}
	return hits
}

func (r *Registry) Show(ref string) (Card, error) {
	name, err := r.Resolve(ref)
	if err != nil {
		return Card{}, err
	}
	t, ok := r.lookup(name)
	if !ok {
		return Card{}, fmt.Errorf("%s was deactivated", name)
	}
	r.regMu.RLock()
	src := r.sources[name]
	r.regMu.RUnlock()
	d := discoveryOf(name, t, src)
	_, reason, _ := r.Unavailable(name)
	summary := d.Summary
	if !d.hostWords {
		summary = authorWords(d.Plugin, summary)
	}
	return Card{
		Name: name, Operation: d.Operation, Plugin: d.Plugin, Version: d.Version, Tier: d.Tier,
		Family: d.Family, Summary: summary, Effects: d.Effects, Capabilities: d.Capabilities,
		MaxResultBytes: d.MaxResultBytes, Examples: d.Examples, Primary: d.Primary, Reason: reason,
		Receipt: ReceiptRule(d.Effects), Parameters: t.Parameters(),
	}, nil
}

func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			cur.WriteRune(c)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func containsWord(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}
