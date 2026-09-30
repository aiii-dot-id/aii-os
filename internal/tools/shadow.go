package tools

import (
	"fmt"
	"sort"
	"strings"
)

type Category struct {
	Family string

	Enabled bool

	Minimum []string

	Blurb string
}

var categories = []Category{
	{
		Family:  "memory",
		Enabled: true,
		Minimum: []string{"memory.store", "memory.search", "memory.recent"},
		Blurb: "Your Ring 4 working memory — the current task's notes, findings and threads. " +
			"Transient by design: it holds what you need now and is not part of your permanent record. " +
			"Use it freely and often. What belongs in your history instead — an experience, something that changed you — goes to note.",
	},

	{Family: "voice", Enabled: false, Minimum: []string{"voice.speak"},
		Blurb: "Your voice — speaking, listening and the session in between."},
	{Family: "planning", Enabled: false, Minimum: []string{"planning.plan"},
		Blurb: "Plans you are keeping — steps, their order, and what each one is waiting on."},
}

func CategoryNames() []string {
	out := make([]string, 0, len(categories))
	for _, c := range categories {
		out = append(out, c.Family)
	}
	sort.Strings(out)
	return out
}

const maxShadowActions = 12

type ShadowAction struct {
	Action           string
	Tool             string
	Plugin           string
	Summary          string
	OperatorConfirms bool
}

type ShadowDoor struct {
	Name        string
	Description string
	Actions     []ShadowAction

	Overflow int

	Conflict []string
}

func (d ShadowDoor) Conflicted() bool { return len(d.Conflict) > 1 }

func (r *Registry) Shadows() []ShadowDoor {
	entries := r.dynamicEntries()
	var out []ShadowDoor
	for _, c := range categories {
		if !c.Enabled {
			continue
		}
		d, _, refusal := r.door(c, entries)
		if refusal != nil && refusal.Code != ReasonDoorConflicted {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type hiddenAction struct {
	act          ShadowAction
	code, reason string
}

func (r *Registry) door(c Category, entries []dynEntry) (ShadowDoor, []hiddenAction, *DoorError) {
	var acts []ShadowAction
	var hidden []hiddenAction
	plugins := map[string]bool{}
	for _, e := range entries {
		if e.disc.Family != c.Family {
			continue
		}
		act := ShadowAction{
			Action:           actionName(c.Family, e.disc.Operation, e.name),
			Tool:             e.name,
			Plugin:           e.disc.Plugin,
			Summary:          e.disc.Summary,
			OperatorConfirms: e.disc.OperatorConfirms,
		}
		if state, code, reason, _ := r.State(e.name); state == StateHidden {
			hidden = append(hidden, hiddenAction{act: act, code: code, reason: reason})
			continue
		}
		plugins[e.disc.Plugin] = true
		acts = append(acts, act)
	}
	if missing := missingMinimum(acts, c.Minimum); len(missing) > 0 || len(acts) == 0 {
		return ShadowDoor{}, hidden, absentDoor(c.Family, missing, hidden)
	}

	if len(plugins) > 1 {
		names := sortedKeys(plugins)
		return ShadowDoor{Name: c.Family, Conflict: names}, hidden, &DoorError{Door: c.Family, Code: ReasonDoorConflicted,
			Detail: fmt.Sprintf("%s is published by more than one plugin (%s) — until one is retired there is no single %s to reach",
				c.Family, strings.Join(names, " and "), c.Family)}
	}
	door := ShadowDoor{Name: c.Family, Description: c.Blurb}
	if len(acts) > maxShadowActions {
		kept := acts[:0:0]
		for _, a := range acts {
			if containsStr(c.Minimum, a.Tool) || containsStr(c.Minimum, a.Action) || minimumHas(c.Minimum, a) {
				kept = append(kept, a)
			}
		}
		door.Overflow = len(acts) - len(kept)
		acts = kept
		door.Description += fmt.Sprintf(" (%d more operations in this family are in `tools brief`.)", door.Overflow)
	}
	uniqueActions(acts)
	sort.Slice(acts, func(i, j int) bool { return acts[i].Action < acts[j].Action })
	door.Actions = acts
	return door, hidden, nil
}

func absentDoor(family string, missing []string, hidden []hiddenAction) *DoorError {
	code := ""
	var reasons []string
	ops := map[string][]string{}
	for _, op := range missing {
		h, ok := hiddenProvider(op, hidden)
		if !ok {
			return &DoorError{Door: family, Code: ReasonDoorAbsent,
				Detail: fmt.Sprintf("no %s is available here — no plugin beside you publishes %s", family, op)}
		}
		if code == "" {
			code = h.code
		}
		if ops[h.reason] == nil {
			reasons = append(reasons, h.reason)
		}
		ops[h.reason] = append(ops[h.reason], fmt.Sprintf("%s (%s)", op, h.act.Tool))
	}
	if code == "" {
		return &DoorError{Door: family, Code: ReasonDoorAbsent,
			Detail: fmt.Sprintf("no %s is available here — no plugin beside you publishes the %s family", family, family)}
	}
	causes := make([]string, len(reasons))
	for i, why := range reasons {
		verb := "is"
		if len(ops[why]) > 1 {
			verb = "are"
		}
		causes[i] = fmt.Sprintf("%s %s hidden: %s", strings.Join(ops[why], ", "), verb, why)
	}
	return &DoorError{Door: family, Code: code,
		Detail: fmt.Sprintf("no %s is available here — %s", family, strings.Join(causes, "; "))}
}

func hiddenProvider(op string, hidden []hiddenAction) (hiddenAction, bool) {
	for _, h := range hidden {
		if minimumHas([]string{op}, h.act) {
			return h, true
		}
	}
	return hiddenAction{}, false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const (
	ReasonDoorAbsent = "DOOR_ABSENT"

	ReasonDoorConflicted = "DOOR_CONFLICTED"

	ReasonDoorAction = "DOOR_ACTION"
)

type DoorError struct {
	Door, Code, Detail string
}

func (e *DoorError) Error() string { return e.Detail }

func IsDoor(name string) bool {
	_, ok := litCategory(name)
	return ok
}

func litCategory(name string) (Category, bool) {
	for _, c := range categories {
		if c.Enabled && c.Family == name {
			return c, true
		}
	}
	return Category{}, false
}

func (r *Registry) ShadowTool(family, action string) (string, error) {
	c, ok := litCategory(family)
	if !ok {
		return "", &DoorError{Door: family, Code: ReasonDoorAbsent, Detail: fmt.Sprintf("%s is not a door here", family)}
	}
	d, hidden, refusal := r.door(c, r.dynamicEntries())
	if refusal != nil {
		return "", refusal
	}
	names := make([]string, 0, len(d.Actions))
	for _, a := range d.Actions {
		if a.Action == action {
			return a.Tool, nil
		}
		names = append(names, a.Action)
	}
	if action == "" {
		return "", &DoorError{Door: family, Code: ReasonDoorAction,
			Detail: fmt.Sprintf("%s needs an action — it has %s", family, strings.Join(names, ", "))}
	}

	for _, h := range hidden {
		if h.act.Action == action || h.act.Tool == action {
			return "", &DoorError{Door: family, Code: h.code,
				Detail: fmt.Sprintf("%s's action %q (%s) is hidden: %s", family, action, h.act.Tool, h.reason)}
		}
	}
	return "", &DoorError{Door: family, Code: ReasonDoorAction,
		Detail: fmt.Sprintf("%s has no action %q — it has %s", family, action, strings.Join(names, ", "))}
}

func actionName(family, operation, tool string) string {
	if operation == "" {
		return tool
	}
	if s, ok := strings.CutPrefix(operation, family+"."); ok && s != "" {
		return s
	}
	return operation
}

func uniqueActions(acts []ShadowAction) {
	count := map[string]int{}
	tool := map[string]bool{}
	for _, a := range acts {
		count[a.Action]++
		tool[a.Tool] = true
	}
	for i, a := range acts {
		if count[a.Action] > 1 || (a.Action != a.Tool && tool[a.Action]) {
			acts[i].Action = a.Tool
		}
	}
}

func minimumHas(minimum []string, a ShadowAction) bool {
	for _, m := range minimum {
		if m == a.Tool {
			return true
		}
		if i := strings.IndexByte(m, '.'); i > 0 && m[i+1:] == a.Action {
			return true
		}
	}
	return false
}

func missingMinimum(acts []ShadowAction, minimum []string) []string {
	var missing []string
	for _, m := range minimum {
		found := false
		for _, a := range acts {
			if minimumHas([]string{m}, a) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, m)
		}
	}
	return missing
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
