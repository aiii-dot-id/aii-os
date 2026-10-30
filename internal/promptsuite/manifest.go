package promptsuite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

const FormatVersion = 1

type Manifest struct {
	Format int `json:"format"`

	Revision int `json:"revision"`

	TrialsPerTask int `json:"trials_per_task"`

	TrialTimeoutSeconds int     `json:"trial_timeout_seconds"`
	Classes             []Class `json:"classes"`

	Plugins []Role `json:"plugins"`
	Tasks   []Task `json:"tasks"`

	SHA256 string `json:"-"`
}

type Class struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	TracedTo []string `json:"traced_to"`

	Threshold *Threshold `json:"threshold"`
}

type Threshold struct {
	Noise float64 `json:"noise"`

	Runs int `json:"runs"`
}

type Role struct {
	Role       string   `json:"role"`
	Operations []string `json:"operations"`
}

type Task struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Class   string `json:"class"`

	TracedTo string `json:"traced_to"`
	Title    string `json:"title"`
	Setup    Setup  `json:"setup"`

	Turns  []string `json:"turns"`
	Checks []Check  `json:"checks"`
}

type Setup struct {
	Files map[string]string `json:"files,omitempty"`

	Config map[string]any `json:"config,omitempty"`

	Plugins []string `json:"plugins,omitempty"`

	Withhold map[string][]string `json:"withhold,omitempty"`

	Turns []string `json:"turns,omitempty"`

	Safe bool `json:"safe,omitempty"`
}

const (
	CheckFile      = "file"
	CheckReply     = "reply"
	CheckCalls     = "calls"
	CheckOrder     = "order"
	CheckLedger    = "ledger"
	CheckName      = "name"
	CheckDelivered = "delivered"
	CheckSameArg   = "same_arg"
	CheckClaim     = "claim"
)

var checkKinds = map[string]bool{CheckFile: true, CheckReply: true, CheckCalls: true, CheckOrder: true,
	CheckLedger: true, CheckName: true, CheckDelivered: true, CheckSameArg: true, CheckClaim: true}

type Check struct {
	Kind string `json:"check"`

	Why string `json:"why"`

	Turn int `json:"turn,omitempty"`

	Path      string `json:"path,omitempty"`
	Glob      string `json:"glob,omitempty"`
	Exists    *bool  `json:"exists,omitempty"`
	Unchanged bool   `json:"unchanged,omitempty"`

	Contains []string `json:"contains,omitempty"`
	Any      []string `json:"any,omitempty"`
	Lacks    []string `json:"lacks,omitempty"`
	Line     *Line    `json:"line,omitempty"`

	Call   *CallMatch `json:"call,omitempty"`
	Before *CallMatch `json:"before,omitempty"`
	Then   *CallMatch `json:"then,omitempty"`
	Min    *int       `json:"min,omitempty"`
	Max    *int       `json:"max,omitempty"`

	Type string `json:"type,omitempty"`

	Actor string `json:"actor,omitempty"`

	Arg   string `json:"arg,omitempty"`
	Turns []int  `json:"turns,omitempty"`

	Claim *Claim `json:"claim,omitempty"`
}

type Claim struct {
	Key         string   `json:"key"`
	RefusedWhen []string `json:"refused_when"`
	Refused     string   `json:"refused"`
	Kept        string   `json:"kept"`
}

type Line struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

type CallMatch struct {
	Tool string `json:"tool"`

	Outcome string `json:"outcome,omitempty"`

	Args map[string]string `json:"args,omitempty"`

	Result []string `json:"result,omitempty"`

	Actor string `json:"actor,omitempty"`
}

const (
	ActorResident = "resident"
	ActorSubagent = "subagent"
	ActorAny      = "any"
)

var outcomes = map[string]bool{"succeeded": true, "failed": true, "refused": true, "cancelled": true, "unknown": true}

var idRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var roleOpRE = regexp.MustCompile(`\{([a-z0-9-]+)\.([A-Za-z0-9_.-]+)\}`)

var grantNames = map[string]bool{"kv": true, "memory": true, "interactions": true, "embeddings": true}

func LoadManifest(file string) (*Manifest, error) {
	blob, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(blob)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	return m, nil
}

func ParseManifest(blob []byte) (*Manifest, error) {
	var m Manifest
	if err := canonicaljson.DecodeStrict(blob, &m); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return nil, fmt.Errorf("content after the manifest object")
	} else if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(blob)
	m.SHA256 = hex.EncodeToString(sum[:])
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Validate() error {
	switch {
	case m.Format != FormatVersion:
		return fmt.Errorf("format %d: this runner reads format %d", m.Format, FormatVersion)
	case m.Revision < 1:
		return fmt.Errorf("revision %d: a frozen manifest is revision 1 or later", m.Revision)
	case m.TrialsPerTask < 1:
		return fmt.Errorf("trials_per_task is %d; a task never run is never measured", m.TrialsPerTask)
	case m.TrialTimeoutSeconds < 1:
		return fmt.Errorf("trial_timeout_seconds is %d; without a positive bound a trial that never rests is never scored", m.TrialTimeoutSeconds)
	case len(m.Classes) == 0:
		return fmt.Errorf("no classes")
	case len(m.Tasks) == 0:
		return fmt.Errorf("no tasks")
	}
	classes := map[string]bool{}
	for _, c := range m.Classes {
		if !idRE.MatchString(c.ID) || classes[c.ID] {
			return fmt.Errorf("class id %q is empty, malformed or used twice", c.ID)
		}
		classes[c.ID] = true
		if strings.TrimSpace(c.Title) == "" || len(c.TracedTo) == 0 {
			return fmt.Errorf("class %q needs a title and the recorded failures it is traced to", c.ID)
		}
		if t := c.Threshold; t != nil && (t.Noise < 0 || t.Noise >= 1 || t.Runs < 2) {
			return fmt.Errorf("class %q: a threshold is a noise between 0 and 1 measured over two or more runs, not %+v", c.ID, *t)
		}
	}
	roles := map[string]map[string]bool{}
	for _, r := range m.Plugins {
		if !idRE.MatchString(r.Role) || roles[r.Role] != nil {
			return fmt.Errorf("plugin role %q is empty, malformed or declared twice", r.Role)
		}
		if len(r.Operations) == 0 {
			return fmt.Errorf("plugin role %q declares no operations: nothing could tell which package plays it", r.Role)
		}
		roles[r.Role] = map[string]bool{}
		for _, op := range r.Operations {
			roles[r.Role][op] = true
		}
	}
	tasks := map[string]bool{}
	used := map[string]int{}
	for i := range m.Tasks {
		t := &m.Tasks[i]
		if !idRE.MatchString(t.ID) || tasks[t.ID] {
			return fmt.Errorf("task id %q is empty, malformed or used twice", t.ID)
		}
		tasks[t.ID] = true
		if err := t.validate(classes, roles); err != nil {
			return fmt.Errorf("task %s: %w", t.ID, err)
		}
		used[t.Class]++
	}
	for _, c := range m.Classes {
		if used[c.ID] == 0 {
			return fmt.Errorf("class %q has no task: a class nothing measures cannot be compared", c.ID)
		}
	}
	return nil
}

func (t *Task) validate(classes map[string]bool, roles map[string]map[string]bool) error {
	switch {
	case t.Version < 1:
		return fmt.Errorf("version %d: a task's first version is 1", t.Version)
	case !classes[t.Class]:
		return fmt.Errorf("class %q is not declared", t.Class)
	case strings.TrimSpace(t.TracedTo) == "" || strings.TrimSpace(t.Title) == "":
		return fmt.Errorf("a task needs a title and the failure it is traced to")
	case len(t.Turns) == 0:
		return fmt.Errorf("no turns: the operator says nothing, and nothing follows to grade")
	case len(t.Checks) == 0:
		return fmt.Errorf("no checks: a task without ground truth cannot be graded")
	}
	for _, w := range append(append([]string{}, t.Setup.Turns...), t.Turns...) {
		if strings.TrimSpace(w) == "" {
			return fmt.Errorf("an operator turn is empty")
		}
	}
	for p := range t.Setup.Files {
		if !homeRelative(p) {
			return fmt.Errorf("setup file %q is not a plain path inside the identity's home", p)
		}
	}
	installed := map[string]bool{}
	for _, r := range t.Setup.Plugins {
		if roles[r] == nil || installed[r] {
			return fmt.Errorf("setup names plugin role %q, which the manifest does not declare or the setup names twice", r)
		}
		installed[r] = true
	}
	for r, grants := range t.Setup.Withhold {
		if !installed[r] {
			return fmt.Errorf("setup withholds a grant from role %q, which it does not install", r)
		}
		for _, g := range grants {
			if !grantNames[g] {
				return fmt.Errorf("setup withholds %q, which is not a grant an install gives (kv, memory, interactions, embeddings)", g)
			}
		}
	}
	for i, c := range t.Checks {
		if err := c.validate(len(t.Turns)); err != nil {
			return fmt.Errorf("check %d (%s): %w", i+1, c.Kind, err)
		}
		for _, m := range []*CallMatch{c.Call, c.Before, c.Then} {
			if m == nil {
				continue
			}
			for _, ref := range roleOpRE.FindAllStringSubmatch(m.Tool, -1) {
				if !installed[ref[1]] || !roles[ref[1]][ref[2]] {
					return fmt.Errorf("check %d names %s, an operation of a role the task does not install or the role does not declare", i+1, ref[0])
				}
			}
		}
	}
	return nil
}

func homeRelative(p string) bool {
	if p == "" || path.IsAbs(p) || strings.Contains(p, `\`) || path.Clean(p) != p || strings.HasPrefix(p, "../") || p == ".." {
		return false
	}
	first := strings.SplitN(p, "/", 2)[0]
	return first != "data" && first != "config" && first != "log" && first != "plugins"
}

func (c Check) validate(turns int) error {
	if !checkKinds[c.Kind] {
		return fmt.Errorf("unknown check kind")
	}
	if strings.TrimSpace(c.Why) == "" {
		return fmt.Errorf("no why: a check's reason is part of its truth")
	}
	if c.Turn < 0 || c.Turn > turns {
		return fmt.Errorf("turn %d: the task has %d", c.Turn, turns)
	}
	if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
		return fmt.Errorf("min %d over max %d", *c.Min, *c.Max)
	}
	if c.Line != nil && (strings.TrimSpace(c.Line.Key) == "" || len(c.Line.Values) == 0) {
		return fmt.Errorf("an answer line needs a key and its accepted values")
	}
	text := len(c.Contains)+len(c.Any)+len(c.Lacks) > 0 || c.Line != nil
	switch c.Kind {
	case CheckFile:
		switch {
		case (c.Path == "") == (c.Glob == ""):
			return fmt.Errorf("a file check names a path or a glob")
		case c.Glob != "":
			if _, err := path.Match(c.Glob, ""); err != nil || !homeRelative(c.Glob) {
				return fmt.Errorf("glob %q is not a pattern inside the identity's home", c.Glob)
			}
			if c.Unchanged {
				return fmt.Errorf("a glob names no one file to hold unchanged")
			}
		case c.Path != "data/ui/name" && !homeRelative(c.Path):
			return fmt.Errorf("path %q is not a plain path inside the identity's home", c.Path)
		}
		if c.Exists == nil && !c.Unchanged && !text {
			return fmt.Errorf("a file check states existence, an unchanged file or text")
		}
	case CheckReply:
		if c.Turn == 0 || !text {
			return fmt.Errorf("a reply check names its turn and the text it holds")
		}
	case CheckCalls:
		if err := c.Call.validate(); err != nil {
			return err
		}
		if c.Min == nil && c.Max == nil {
			return fmt.Errorf("a calls check bounds the count")
		}
	case CheckOrder:
		if err := c.Call.validate(); err != nil {
			return err
		}
		if (c.Before == nil) == (c.Then == nil) {
			return fmt.Errorf("an order check names one of before and then")
		}
		other, name := c.Before, "before"
		if c.Then != nil {
			other, name = c.Then, "then"
		}
		if err := other.validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	case CheckLedger:
		if c.Type == "" || (c.Min == nil && c.Max == nil) {
			return fmt.Errorf("a ledger check names a record type and bounds the count")
		}
	case CheckName:
		if len(c.Lacks) == 0 && c.Line == nil && len(c.Contains) == 0 {
			return fmt.Errorf("a name check says what the name must or must not be")
		}
	case CheckDelivered:
		if c.Actor != ActorResident && c.Actor != ActorSubagent {
			return fmt.Errorf("actor %q is neither %s nor %s", c.Actor, ActorResident, ActorSubagent)
		}
		if c.Min == nil && !text {
			return fmt.Errorf("a delivered check bounds the count or states text")
		}
	case CheckClaim:
		if err := c.Call.validate(); err != nil {
			return err
		}
		if c.Turn == 0 || c.Claim == nil || c.Claim.Key == "" || c.Claim.Refused == "" || c.Claim.Kept == "" || answerValue(c.Claim.Refused) == answerValue(c.Claim.Kept) {
			return fmt.Errorf("a claim check names its turn, the answer line's key, and the two different values it must take")
		}
	case CheckSameArg:
		if err := c.Call.validate(); err != nil {
			return err
		}
		if c.Arg == "" || len(c.Turns) < 2 {
			return fmt.Errorf("same_arg names an argument and two or more turns")
		}
		for _, n := range c.Turns {
			if n < 1 || n > turns {
				return fmt.Errorf("same_arg turn %d: the task has %d", n, turns)
			}
		}
	}
	return nil
}

func (m *CallMatch) validate() error {
	if m == nil || m.Tool == "" {
		return fmt.Errorf("no call to match")
	}
	if _, err := regexp.Compile(roleOpRE.ReplaceAllString(m.Tool, "x")); err != nil {
		return fmt.Errorf("tool pattern: %w", err)
	}
	for k, v := range m.Args {
		if _, err := regexp.Compile(v); err != nil {
			return fmt.Errorf("argument %s pattern: %w", k, err)
		}
	}
	if m.Outcome != "" && !outcomes[m.Outcome] {
		return fmt.Errorf("outcome %q is not one the host records", m.Outcome)
	}
	switch m.Actor {
	case "", ActorResident, ActorSubagent, ActorAny:
	default:
		return fmt.Errorf("actor %q is not resident, subagent or any", m.Actor)
	}
	return nil
}

func (m *Manifest) Frozen() (bool, []string) {
	var open []string
	for _, c := range m.Classes {
		if c.Threshold == nil {
			open = append(open, c.ID)
		}
	}
	sort.Strings(open)
	return len(open) == 0, open
}

func (m *Manifest) Task(id string) *Task {
	for i := range m.Tasks {
		if m.Tasks[i].ID == id {
			return &m.Tasks[i]
		}
	}
	return nil
}

func (m *Manifest) Role(r string) *Role {
	for i := range m.Plugins {
		if m.Plugins[i].Role == r {
			return &m.Plugins[i]
		}
	}
	return nil
}

func Roles(tasks []*Task, m *Manifest) []Role {
	used := map[string]bool{}
	for _, t := range tasks {
		for _, r := range t.Setup.Plugins {
			used[r] = true
		}
	}
	var out []Role
	for _, r := range m.Plugins {
		if used[r.Role] {
			out = append(out, r)
		}
	}
	return out
}
