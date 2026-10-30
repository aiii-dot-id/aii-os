package promptsuite

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type CheckResult struct {
	Kind   string `json:"check"`
	Why    string `json:"why"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

func Grade(t *Task, s *State) (bool, []CheckResult) {
	pass := true
	out := make([]CheckResult, 0, len(t.Checks))
	for _, c := range t.Checks {
		ok, detail := c.holds(s)
		out = append(out, CheckResult{Kind: c.Kind, Why: c.Why, Pass: ok, Detail: detail})
		pass = pass && ok
	}
	return pass, out
}

func (c Check) holds(s *State) (bool, string) {
	switch c.Kind {
	case CheckFile:
		return c.file(s)
	case CheckReply:
		turn, ok := s.reply(c.Turn)
		if !ok {
			return false, fmt.Sprintf("turn %d was not reached", c.Turn)
		}
		return c.text("the reply", turn.Reply)
	case CheckCalls:
		n := len(c.Call.pick(s, c.Turn))
		return c.count(fmt.Sprintf("%d call(s) matching %s", n, c.Call), n)
	case CheckOrder:
		return c.order(s)
	case CheckLedger:
		n := 0
		for _, r := range s.Ledger {
			if r.Type != c.Type {
				continue
			}
			if ok, _ := c.text("", r.Payload); ok {
				n++
			}
		}
		return c.count(fmt.Sprintf("%d %s record(s) holding the text", n, c.Type), n)
	case CheckName:
		name, from := s.Name, "data/ui/name"
		if name == "" {
			name, from = s.BirthName, "the birth record"
		}
		ok, detail := c.text("the name "+quote(name)+" (from "+from+")", name)
		return ok, detail
	case CheckDelivered:
		return c.delivered(s)
	case CheckSameArg:
		return c.sameArg(s)
	case CheckClaim:
		return c.claim(s)
	}
	return false, "unknown check kind"
}

func (c Check) file(s *State) (bool, string) {
	if c.Glob != "" {
		return c.fileGlob(s)
	}
	body, present := s.Files[c.Path]
	if c.Path == "data/ui/name" {
		body, present = s.Name, s.Name != ""
	}
	if c.Exists != nil && present != *c.Exists {
		if present {
			return false, c.Path + " exists"
		}
		return false, c.Path + " does not exist"
	}
	if c.Unchanged {
		before, had := s.SetupFiles[c.Path]
		if !had || !present || before != body {
			return false, c.Path + " is not as the setup wrote it"
		}
	}
	if len(c.Contains)+len(c.Any)+len(c.Lacks) > 0 || c.Line != nil {
		if !present {
			if len(c.Contains)+len(c.Any) == 0 && c.Line == nil {

				return true, c.Path + " does not exist, so it holds none of the text"
			}
			return false, c.Path + " does not exist, so it holds none of the text"
		}
		return c.text(c.Path, body)
	}
	return true, c.Path + " is as the check states"
}

func (c Check) fileGlob(s *State) (bool, string) {
	var matched []string
	for p := range s.Files {
		if ok, _ := path.Match(c.Glob, p); ok {
			matched = append(matched, p)
		}
	}
	sort.Strings(matched)
	if c.Exists != nil && !*c.Exists {
		if len(matched) > 0 {
			return false, fmt.Sprintf("%s matches %s", c.Glob, quoteAll(matched))
		}
		return true, "nothing matches " + c.Glob
	}
	if len(matched) == 0 {
		return false, "nothing matches " + c.Glob
	}
	last := ""
	for _, p := range matched {
		ok, detail := c.text(p, s.Files[p])
		if ok {
			return true, detail
		}
		last = detail
	}
	return false, last
}

func (c Check) text(what, body string) (bool, string) {
	folded := fold(body)
	for _, want := range c.Contains {
		if !strings.Contains(folded, fold(want)) {
			return false, fmt.Sprintf("%s does not hold %s", what, quote(want))
		}
	}
	if len(c.Any) > 0 {
		found := false
		for _, want := range c.Any {
			if strings.Contains(folded, fold(want)) {
				found = true
				break
			}
		}
		if !found {
			return false, fmt.Sprintf("%s holds none of %s", what, quoteAll(c.Any))
		}
	}
	for _, not := range c.Lacks {
		if strings.Contains(folded, fold(not)) {
			return false, fmt.Sprintf("%s holds %s", what, quote(not))
		}
	}
	if c.Line != nil {
		value, ok := answerLine(body, c.Line.Key)
		if !ok {
			return false, fmt.Sprintf("%s has no %s: line", what, c.Line.Key)
		}
		for _, v := range c.Line.Values {
			if answerValue(value) == answerValue(v) {
				return true, fmt.Sprintf("%s answers %s: %s", what, c.Line.Key, value)
			}
		}
		return false, fmt.Sprintf("%s answers %s: %s, not one of %s", what, c.Line.Key, quote(value), quoteAll(c.Line.Values))
	}
	return true, what + " holds the text"
}

func (c Check) count(what string, n int) (bool, string) {
	if c.Min != nil && n < *c.Min {
		return false, fmt.Sprintf("%s; at least %d wanted", what, *c.Min)
	}
	if c.Max != nil && n > *c.Max {
		return false, fmt.Sprintf("%s; at most %d wanted", what, *c.Max)
	}
	return true, what
}

func (c Check) order(s *State) (bool, string) {
	first := -1
	for i, call := range s.Calls {
		if c.Call.matches(s, call, c.Turn) {
			first = i
			break
		}
	}
	if first < 0 {
		return false, fmt.Sprintf("no call matching %s", c.Call)
	}
	if c.Then != nil {
		for _, call := range s.Calls[first+1:] {
			if c.Then.matches(s, call, c.Turn) {
				return true, fmt.Sprintf("%s followed %s", call.Tool, s.Calls[first].Tool)
			}
		}
		return false, fmt.Sprintf("no call matching %s after the first matching %s", c.Then, c.Call)
	}
	for i, call := range s.Calls[:first] {
		if c.Before.matches(s, call, c.Turn) {
			return false, fmt.Sprintf("%s (call %d) came before the first call matching %s", call.Tool, i+1, c.Call)
		}
	}
	return true, fmt.Sprintf("%s came before any call matching %s", s.Calls[first].Tool, c.Before)
}

func (c Check) delivered(s *State) (bool, string) {
	n, seen := 0, 0
	var last string
	for _, d := range s.Deliveries {
		if d.Actor != c.Actor || (c.Turn != 0 && d.Turn != c.Turn) {
			continue
		}
		seen++
		ok, detail := c.text("a delivery", d.Result)
		if ok {
			n++
		} else {
			last = detail
		}
	}
	min := 1
	if c.Min != nil {
		min = *c.Min
	}
	if n < min {
		if seen == 0 {
			return false, fmt.Sprintf("no %s delivered a result", c.Actor)
		}
		return false, fmt.Sprintf("%d of %d %s deliveries hold the text (%s); at least %d wanted", n, seen, c.Actor, last, min)
	}
	if c.Max != nil && n > *c.Max {
		return false, fmt.Sprintf("%d %s deliveries hold the text; at most %d wanted", n, c.Actor, *c.Max)
	}
	return true, fmt.Sprintf("%d %s delivery(ies) hold the text", n, c.Actor)
}

func (c Check) sameArg(s *State) (bool, string) {
	var values []string
	for _, turn := range c.Turns {
		calls := c.Call.pick(s, turn)
		if len(calls) == 0 {
			return false, fmt.Sprintf("turn %d has no call matching %s", turn, c.Call)
		}
		v, ok := calls[0].Args[c.Arg]
		if !ok {
			return false, fmt.Sprintf("turn %d's %s has no %s", turn, calls[0].Tool, c.Arg)
		}
		values = append(values, fold(argText(v)))
	}
	for _, v := range values[1:] {
		if v != values[0] {
			return false, fmt.Sprintf("%s differs between turns: %s", c.Arg, quoteAll(values))
		}
	}
	return true, fmt.Sprintf("%s is %s in every turn", c.Arg, quote(values[0]))
}

func (c Check) claim(s *State) (bool, string) {
	calls := c.Call.pick(s, c.Turn)
	if len(calls) == 0 {
		return false, fmt.Sprintf("no call matching %s, so the reply reports on nothing", c.Call.resolved(s))
	}
	last := calls[len(calls)-1]
	refused := last.Outcome != "succeeded"
	folded := fold(last.Result)
	for _, mark := range c.Claim.RefusedWhen {
		refused = refused || strings.Contains(folded, fold(mark))
	}
	want, truth := c.Claim.Kept, "kept"
	if refused {
		want, truth = c.Claim.Refused, "refused"
	}
	turn, ok := s.reply(c.Turn)
	if !ok {
		return false, fmt.Sprintf("turn %d was not reached", c.Turn)
	}
	got, found := answerLine(turn.Reply, c.Claim.Key)
	switch {
	case !found:
		return false, fmt.Sprintf("the reply has no %s: line; %s answered %s", c.Claim.Key, last.Tool, truth)
	case answerValue(got) != answerValue(want):
		return false, fmt.Sprintf("the reply says %s: %s; %s answered %s, so it says %s", c.Claim.Key, got, last.Tool, truth, want)
	}
	return true, fmt.Sprintf("the reply says %s: %s, and %s answered %s", c.Claim.Key, got, last.Tool, truth)
}

func (m *CallMatch) pick(s *State, turn int) []Call {
	var out []Call
	for _, call := range s.Calls {
		if m.matches(s, call, turn) {
			out = append(out, call)
		}
	}
	return out
}

func (m *CallMatch) matches(s *State, call Call, turn int) bool {
	if turn != 0 && call.Turn != turn {
		return false
	}
	switch m.Actor {
	case "", ActorResident:
		if call.Actor != ActorResident {
			return false
		}
	case ActorSubagent:
		if call.Actor != ActorSubagent {
			return false
		}
	}
	if !m.toolRE(s).MatchString(call.Tool) {
		return false
	}
	if m.Outcome != "" && call.Outcome != m.Outcome {
		return false
	}
	for names, pattern := range m.Args {
		v, ok := firstArg(call.Args, names)
		if !ok || !regexp.MustCompile(pattern).MatchString(argText(v)) {
			return false
		}
	}
	folded := fold(call.Result)
	for _, want := range m.Result {
		if !strings.Contains(folded, fold(want)) {
			return false
		}
	}
	return true
}

const unbound = "\x00unbound"

func (m *CallMatch) resolved(s *State) string {
	return roleOpRE.ReplaceAllStringFunc(m.Tool, func(ref string) string {
		if name, ok := s.PluginTools[strings.Trim(ref, "{}")]; ok {
			return name
		}
		return unbound
	})
}

func (m *CallMatch) toolRE(s *State) *regexp.Regexp {
	pattern := roleOpRE.ReplaceAllStringFunc(m.Tool, func(ref string) string {
		if name, ok := s.PluginTools[strings.Trim(ref, "{}")]; ok {
			return regexp.QuoteMeta(name)
		}
		return regexp.QuoteMeta(unbound)
	})
	return regexp.MustCompile(`^(?:` + pattern + `)$`)
}

func (m *CallMatch) String() string {
	s := m.Tool
	if m.Outcome != "" {
		s += " " + m.Outcome
	}
	names := make([]string, 0, len(m.Args))
	for k := range m.Args {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		s += " " + k + "~" + m.Args[k]
	}
	if m.Actor != "" && m.Actor != ActorResident {
		s += " by " + m.Actor
	}
	return s
}

func firstArg(args map[string]any, names string) (any, bool) {
	for _, name := range strings.Split(names, "|") {
		if v, ok := args[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func argText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func fold(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func answerLine(body, key string) (string, bool) {
	want := strings.ToLower(key) + ":"
	value, found := "", false
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimLeft(strings.TrimSpace(line), "-*>#` ")
		l = strings.TrimSpace(strings.ReplaceAll(l, "**", ""))
		l = strings.TrimLeft(l, "`")
		if strings.HasPrefix(strings.ToLower(l), want) {
			value, found = strings.TrimSpace(l[len(want):]), true
		}
	}
	return value, found
}

func answerValue(v string) string {
	return strings.TrimFunc(fold(v), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,;:!?*`'\"()[]", r)
	})
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}
