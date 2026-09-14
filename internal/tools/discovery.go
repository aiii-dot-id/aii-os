package tools

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

import (
	"fmt"
	"sort"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .
// .
const MaxOffered = 8

// .
// .
// .
const (
	// .
	MaxBriefFamilies = 16
	// .
	MaxFamilyNames = 6
	// .
	DefaultSearchLimit = 8
	MaxSearchLimit     = 16
)

// .
type State string

const (
	// .
	StateOffered State = "offered"
	// .
	StateInspectOnly State = "inspect-only"
	// .
	// .
	StateHidden State = "hidden"
)

// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
	OperatorConfirms bool
}

// .
type Discoverable interface {
	Discovery() Discovery
}

// .
// .
func FamilyOf(operation string) string {
	if i := strings.IndexByte(operation, '.'); i > 0 {
		return operation[:i]
	}
	return operation
}

// .
// .
// .
// .
// .
func ReceiptRule(effects string) string {
	switch effects {
	case "read.internal":
		return "no host effect: the result is the plugin's own text, and there is nothing beyond it to prove"
	case "read.external":
		return "each outward read the plugin makes is a host-written receipt; the result text is the plugin's account of what it read"
	case "write.local":
		return "each write to the plugin's scoped store is a host-written receipt; the receipt, not the result text, is the proof the write happened"
	case "write.external":
		return "each external effect is a host-written receipt; a result claiming an effect without its receipt is unproven"
	case "exec":
		return "each invocation is a host-written receipt; trusted native only"
	}
	return "undeclared effect class: treat every claim in the result as unproven"
}

// .
type Family struct {
	Name  string
	Count int
	Names []string
	More  int
}

// .
type Brief struct {
	Total        int
	Offered      int
	Unavailable  int
	Families     []Family
	MoreFamilies int
	Offered_     []string
	OfferedNames []string
}

// .
type Hit struct {
	Name      string
	Operation string
	Plugin    string
	Summary   string
	Effects   string
	State     State
}

// .
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
	State          State
	Reason         string
	Receipt        string
	Parameters     map[string]interface{}
}

// .
type dynEntry struct {
	name string
	tool Tool
	disc Discovery
}

// .
// .
// .
// .
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
	if d.Operation == "" {
		d.Operation = name
	}
	if d.Family == "" {
		d.Family = FamilyOf(d.Operation)
	}
	if d.Summary == "" {
		d.Summary = t.Description()
	}
	return d
}

// .
// .
// .
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

// .
// .
// .
// .
func (r *Registry) PromptNames() []string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if r.hostOnly[name] {
			continue
		}
		if src := r.sources[name]; src == "builtin" || r.offered[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// .
// .
// .
func (r *Registry) State(name string) (state State, reason string, ok bool) {
	r.regMu.RLock()
	_, exists := r.tools[name]
	src := r.sources[name]
	host := r.hostOnly[name]
	offered := r.offered[name]
	r.regMu.RUnlock()
	if !exists || src == "" || src == "builtin" || host {
		return "", "", false
	}
	if !r.ToolEnabled(name) {
		return StateHidden, "disabled by your operator", true
	}
	if r.safeSource != nil {
		if why, safe := r.safeSource(); safe {
			return StateHidden, "suspended in safe mode: " + why, true
		}
	}
	if offered {
		return StateOffered, "", true
	}
	return StateInspectOnly, "", true
}

// .
// .
// .
// .
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

// .
// .
// .
func (r *Registry) Offer(ref string) (string, error) {
	name, err := r.Resolve(ref)
	if err != nil {
		return "", err
	}
	state, reason, _ := r.State(name)
	switch state {
	case StateHidden:
		return name, fmt.Errorf("%s cannot be offered: %s", name, reason)
	case StateOffered:
		return name, nil
	}
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if _, still := r.tools[name]; !still {
		return name, fmt.Errorf("%s was deactivated", name)
	}
	if len(r.offerOrder) >= MaxOffered {
		return name, fmt.Errorf("the offer is full (%d of %d): release one of %s first", len(r.offerOrder), MaxOffered, strings.Join(r.offerOrder, ", "))
	}
	if r.offered == nil {
		r.offered = map[string]bool{}
	}
	r.offered[name] = true
	r.offerOrder = append(r.offerOrder, name)
	return name, nil
}

// .
func (r *Registry) Release(ref string) (string, error) {
	name, err := r.Resolve(ref)
	if err != nil {
		return "", err
	}
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if !r.offered[name] {
		return name, fmt.Errorf("%s is not in the offer", name)
	}
	r.unofferLocked(name)
	return name, nil
}

// .
func (r *Registry) unofferLocked(name string) {
	delete(r.offered, name)
	for i, n := range r.offerOrder {
		if n == name {
			r.offerOrder = append(r.offerOrder[:i:i], r.offerOrder[i+1:]...)
			break
		}
	}
}

// .
func (r *Registry) Offered() []string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	return append([]string(nil), r.offerOrder...)
}

// .
// .
func (r *Registry) Brief() Brief {
	var b Brief
	fams := map[string]*Family{}
	for _, e := range r.dynamicEntries() {
		state, _, _ := r.State(e.name)
		if state == StateHidden {
			b.Unavailable++
			continue
		}
		b.Total++
		if state == StateOffered {
			b.Offered++
		}
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
	for _, name := range r.Offered() {
		op := name
		if t, ok := r.lookup(name); ok {
			op = discoveryOf(name, t, "").Operation
		}
		b.OfferedNames = append(b.OfferedNames, op+" ("+name+")")
	}
	return b
}

// .
// .
// .
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
		state, _, _ := r.State(e.name)
		if state == StateHidden {
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
			out = append(out, scored{score: score, hit: Hit{Name: e.name, Operation: e.disc.Operation, Plugin: e.disc.Plugin, Summary: e.disc.Summary, Effects: e.disc.Effects, State: state}})
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

// .
// .
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
	state, reason, _ := r.State(name)
	return Card{
		Name: name, Operation: d.Operation, Plugin: d.Plugin, Version: d.Version, Tier: d.Tier,
		Family: d.Family, Summary: d.Summary, Effects: d.Effects, Capabilities: d.Capabilities,
		MaxResultBytes: d.MaxResultBytes, Examples: d.Examples, State: state, Reason: reason,
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
