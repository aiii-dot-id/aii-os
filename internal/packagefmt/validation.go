package packagefmt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/speecheval"
)

const ValidationFile = "validation.json"

const (
	LaneOperation  = "operation"
	LaneEmbeddings = "embeddings"
	LaneSession    = "session"
)

func KnownLane(lane string) bool {
	return lane == LaneOperation || lane == LaneEmbeddings || lane == LaneSession
}

const (
	AllSets = "*"

	MaxChecks = 32

	MaxCheckNameChars = 120

	MaxCheckWithin = 10 * time.Minute
)

const (
	MaxCheckTextChars   = 1000
	MaxEventOrder       = 16
	MaxTranscriptWords  = 16
	MaxPhraseChars      = 120
	MaxAudioFloorMS     = 600000
	MinAudioFloorDBFS   = -120
	MaxCheckAudioBytes  = 8 << 20
	MinCheckRecording   = 100 * time.Millisecond
	MaxCheckRecording   = 30 * time.Second
	maxSessionEventName = 64
)

type Validation struct {
	Checks []Check
}

type Check struct {
	Name      string
	Sets      []string
	Lane      string
	Operation string
	Arguments map[string]any
	Kind      string
	Text      string
	Audio     string
	WAV       []byte
	Recording time.Duration
	Expect    Expect

	Within     time.Duration
	Unresolved string
}

func (c Check) For(variant string) bool {
	for _, s := range c.Sets {
		if s == AllSets || s == variant {
			return true
		}
	}
	return false
}

type Expect struct {
	Contains []string

	JSON *JSONExpect

	Cosine *CosineExpect

	Events *EventsExpect
}

type EventsExpect struct {
	Order              []string
	TranscriptContains []string
	AudioAtLeast       *AudioFloor
}

type AudioFloor struct {
	MS      int64
	RMSDBFS float64
}

var (
	telemetryEvents = map[string]bool{"vad_probability": true, "transcript_partial": true}
	terminalEvents  = map[string]bool{"session_end": true, "cancellation": true, "failure": true}
	inputEvents     = map[string]bool{"transcript_partial": true, "transcript_final": true, "speaker_observation": true, "input_finished": true}
)

func SessionEventClass(typ string) (telemetry, terminal, input bool) {
	return telemetryEvents[typ], terminalEvents[typ], inputEvents[typ]
}

type JSONExpect struct {
	Path    string
	Equals  any
	AtLeast *float64
}

func (j *JSONExpect) HasEquals() bool { return j.AtLeast == nil }

type CosineExpect struct {
	Reference []float64
	Min       float64
}

type ValidationError struct {
	Check  int
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Check == 0 {
		return ValidationFile + ": " + e.Detail
	}
	return fmt.Sprintf("%s: check %d: %s", ValidationFile, e.Check, e.Detail)
}

type MemberReader func(rel string) (raw []byte, present bool, err error)

type rawValidation struct {
	Checks []json.RawMessage `json:"checks"`
}

type rawCheck struct {
	Name     *string         `json:"name"`
	Sets     []string        `json:"sets"`
	Lane     *string         `json:"lane"`
	Input    json.RawMessage `json:"input"`
	Call     *rawCall        `json:"call"`
	Expect   *rawExpect      `json:"expect"`
	WithinMS json.RawMessage `json:"within_ms"`
}

type rawCall struct {
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments"`
}

type rawExpect struct {
	ResultContains      json.RawMessage `json:"result_contains"`
	JSON                *rawJSONExpect  `json:"json"`
	VectorCosineAtLeast *rawCosine      `json:"vector_cosine_at_least"`
	Events              json.RawMessage `json:"events"`
}

type rawJSONExpect struct {
	Path    string          `json:"path"`
	Equals  json.RawMessage `json:"equals"`
	AtLeast json.RawMessage `json:"at_least"`
}

type rawCosine struct {
	Reference json.RawMessage `json:"reference"`
	Min       json.RawMessage `json:"min"`
}

func ParseValidation(raw []byte, member MemberReader) (*Validation, error) {
	var doc rawValidation
	if err := canonicaljson.DecodeStrict(raw, &doc); err != nil {
		if errors.Is(err, canonicaljson.ErrTrailingContent) {
			return nil, &ValidationError{Detail: "trailing content after the checks"}
		}
		return nil, &ValidationError{Detail: fmt.Sprintf(`not an object whose one member is "checks", a list of checks: %v`, err)}
	}
	if len(doc.Checks) < 1 || len(doc.Checks) > MaxChecks {
		return nil, &ValidationError{Detail: fmt.Sprintf("checks must hold 1..%d entries", MaxChecks)}
	}
	r := &resolver{member: member, docs: map[string]any{}, raws: map[string]memberBytes{}, recordings: map[string]int{}}
	out := &Validation{}
	names := map[string]bool{}
	for i, raw := range doc.Checks {
		c, err := r.check(i+1, raw)
		if err != nil {
			return nil, err
		}
		if names[c.Name] {
			return nil, &ValidationError{Check: i + 1, Detail: fmt.Sprintf("the name %q is another check's", c.Name)}
		}
		names[c.Name] = true
		out.Checks = append(out.Checks, c)
	}

	total := 0
	for _, n := range r.recordings {
		total += n
	}
	if total > MaxCheckAudioBytes {
		return nil, &ValidationError{Detail: fmt.Sprintf("the session checks' recordings come to %d bytes; a package carries at most %d", total, MaxCheckAudioBytes)}
	}
	return out, nil
}

type unresolved struct{ why string }

func (u *unresolved) Error() string { return u.why }

type resolver struct {
	member MemberReader
	docs   map[string]any
	bad    map[string]string
	raws   map[string]memberBytes

	recordings map[string]int
}

type memberBytes struct {
	raw     []byte
	present bool
}

func (r *resolver) read(rel string) ([]byte, bool, error) {
	if m, ok := r.raws[rel]; ok {
		return m.raw, m.present, nil
	}
	raw, present, err := r.member(rel)
	if err != nil {
		return nil, false, err
	}
	r.raws[rel] = memberBytes{raw: raw, present: present}
	return raw, present, nil
}

func (r *resolver) check(n int, raw json.RawMessage) (Check, error) {
	refuse := func(format string, args ...any) (Check, error) {
		return Check{}, &ValidationError{Check: n, Detail: fmt.Sprintf(format, args...)}
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return refuse("not an object of the members a check has")
	}
	laneRaw, hasLane := members["lane"]
	if !hasLane || string(bytes.TrimSpace(laneRaw)) == "null" {
		return refuse("lane is missing")
	}
	var lane string
	if err := json.Unmarshal(laneRaw, &lane); err != nil {
		return refuse("lane must be a string")
	}
	var rc rawCheck
	if KnownLane(lane) {
		if err := canonicaljson.DecodeStrict(raw, &rc); err != nil {
			return refuse("not an object of the members a check on the %s lane has: %v", lane, err)
		}
	} else {
		var head struct {
			Name     *string         `json:"name"`
			Sets     []string        `json:"sets"`
			WithinMS json.RawMessage `json:"within_ms"`
		}

		for name, dst := range map[string]any{"name": &head.Name, "sets": &head.Sets, "within_ms": &head.WithinMS} {
			if v, ok := members[name]; ok {
				if err := json.Unmarshal(v, dst); err != nil {
					return refuse("%s is not what every check's %s is", name, name)
				}
			}
		}
		rc = rawCheck{Name: head.Name, Sets: head.Sets, Lane: &lane, WithinMS: head.WithinMS}
	}
	var c Check
	switch {
	case rc.Name == nil:
		return refuse("name is missing")
	case !validCheckName(*rc.Name):
		return refuse("name must be 1..%d characters of text, not white space only", MaxCheckNameChars)
	}
	c.Name = *rc.Name
	if len(rc.Sets) == 0 {
		return refuse("sets must name %q or the component sets the check is for", AllSets)
	}
	seen := map[string]bool{}
	for _, s := range rc.Sets {
		switch {
		case s == AllSets && len(rc.Sets) != 1:
			return refuse("sets: %q stands alone", AllSets)
		case s != AllSets && !reVariantID.MatchString(s):
			return refuse("sets: %q is not a component set's id", s)
		case seen[s]:
			return refuse("sets names %q twice", s)
		}
		seen[s] = true
	}
	c.Sets = append([]string(nil), rc.Sets...)
	if rc.Lane == nil {
		return refuse("lane is missing")
	}
	c.Lane = *rc.Lane
	known := KnownLane(c.Lane)
	if known {
		if rc.Expect == nil {
			return refuse("expect is missing")
		}
		forms := 0
		for _, set := range []bool{len(rc.Expect.ResultContains) > 0, rc.Expect.JSON != nil, rc.Expect.VectorCosineAtLeast != nil, len(rc.Expect.Events) > 0} {
			if set {
				forms++
			}
		}
		if forms != 1 {
			return refuse("expect holds exactly one of result_contains, json, vector_cosine_at_least, events; it holds %d", forms)
		}
	}
	switch c.Lane {
	case LaneOperation:
		switch {
		case rc.Call == nil:
			return refuse("a check on the operation lane carries call: the operation and its arguments")
		case len(rc.Input) > 0:
			return refuse("a check on the operation lane carries call, not input")
		case rc.Expect.VectorCosineAtLeast != nil || len(rc.Expect.Events) > 0:
			return refuse("a check on the operation lane expects result_contains or json")
		case rc.Call.Operation == "" || len(rc.Call.Operation) > 128 || strings.ContainsFunc(rc.Call.Operation, unicode.IsControl):
			return refuse("call.operation must name an operation")
		}
		c.Operation = rc.Call.Operation
	case LaneEmbeddings:
		switch {
		case len(rc.Input) == 0:
			return refuse("a check on the embeddings lane carries input: a kind and a text")
		case rc.Call != nil:
			return refuse("a check on the embeddings lane carries input, not call")
		case rc.Expect.VectorCosineAtLeast == nil:
			return refuse("a check on the embeddings lane expects vector_cosine_at_least")
		}
	case LaneSession:
		switch {
		case len(rc.Input) == 0:
			return refuse("a check on the session lane carries input: a recording the package carries, or a text")
		case rc.Call != nil:
			return refuse("a check on the session lane carries input, not call")
		case len(rc.Expect.Events) == 0:
			return refuse("a check on the session lane expects events")
		}
	}
	if len(rc.WithinMS) == 0 {
		return refuse("within_ms is missing: every check has its own bound")
	}
	if known && rc.Expect.JSON != nil && !validPointer(rc.Expect.JSON.Path) {
		return refuse(`expect.json.path must be a JSON pointer ("" or "/…")`)
	}
	if j := rc.Expect; known && j.JSON != nil && (len(j.JSON.Equals) > 0) == (len(j.JSON.AtLeast) > 0) {
		return refuse("expect.json holds exactly one of equals and at_least")
	}

	take := func(raw json.RawMessage, what string, hold func(any) error) error {
		v, viaRef, err := r.value(raw)
		var u *unresolved
		switch {
		case errors.As(err, &u):
			if c.Unresolved == "" {
				c.Unresolved = what + ": " + u.why
			}
			return nil
		case errors.Is(err, errRefForm):
			return &ValidationError{Check: n, Detail: fmt.Sprintf("%s %v", what, err)}
		case err != nil:
			return err
		}
		if herr := hold(v); herr != nil {
			if !viaRef {
				return &ValidationError{Check: n, Detail: fmt.Sprintf("%s %v", what, herr)}
			}
			if c.Unresolved == "" {
				c.Unresolved = fmt.Sprintf("%s: the value its $ref brings %v", what, herr)
			}
		}
		return nil
	}
	steps := []func() error{func() error {
		return take(rc.WithinMS, "within_ms", func(v any) error {
			ms, isInt := integer(v)
			if !isInt || ms < 1 || ms > MaxCheckWithin.Milliseconds() {
				return fmt.Errorf("must be a whole number of milliseconds, 1..%d", MaxCheckWithin.Milliseconds())
			}
			c.Within = time.Duration(ms) * time.Millisecond
			return nil
		})
	}}
	if !known {

		for _, step := range steps {
			if err := step(); err != nil {
				return Check{}, err
			}
		}
		return c, nil
	}
	switch c.Lane {
	case LaneOperation:
		args := rc.Call.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		steps = append(steps, func() error {
			return take(args, "call.arguments", func(v any) error {
				m, isObj := v.(map[string]any)
				if !isObj {
					return errors.New("must be an object")
				}
				c.Arguments = m
				return nil
			})
		})
	case LaneEmbeddings:
		steps = append(steps, func() error {
			return take(rc.Input, "input", func(v any) error {
				m, isObj := v.(map[string]any)
				kind, k := m["kind"].(string)
				text, t := m["text"].(string)
				if !isObj || len(m) != 2 || !k || !t || kind == "" || text == "" {
					return errors.New(`must be an object of two members, "kind" and "text", each a string`)
				}
				c.Kind, c.Text = kind, text
				return nil
			})
		})
	case LaneSession:
		steps = append(steps, func() error {
			return take(rc.Input, "input", func(v any) error {
				audio, text, err := sessionInput(v)
				if err == nil {
					c.Audio, c.Text = audio, text
				}
				return err
			})
		}, func() error {

			if c.Audio == "" || c.Unresolved != "" {
				return nil
			}
			why, err := r.recording(&c)
			if why != "" && c.Unresolved == "" {
				c.Unresolved = "input.audio: " + why
			}
			return err
		})
	}
	e := rc.Expect
	switch {
	case len(e.ResultContains) > 0:
		steps = append(steps, func() error {
			return take(e.ResultContains, "expect.result_contains", func(v any) error {
				words, err := containsList(v)
				c.Expect.Contains = words
				return err
			})
		})
	case e.JSON != nil:
		j := &JSONExpect{Path: e.JSON.Path}
		c.Expect.JSON = j
		steps = append(steps, func() error {
			if len(e.JSON.Equals) > 0 {
				return take(e.JSON.Equals, "expect.json.equals", func(v any) error {
					j.Equals = v
					return nil
				})
			}
			return take(e.JSON.AtLeast, "expect.json.at_least", func(v any) error {
				f, isNum := v.(float64)
				if !isNum {
					return errors.New("must be a number")
				}
				j.AtLeast = &f
				return nil
			})
		})
	case len(e.Events) > 0:
		steps = append(steps, func() error {
			return take(e.Events, "expect.events", func(v any) error {
				ev, err := sessionEvents(v, c.Audio != "", c.Text != "")
				c.Expect.Events = ev
				return err
			})
		}, func() error {

			if c.Unresolved == "" && c.Recording > c.Within {
				c.Unresolved = fmt.Sprintf("within_ms: %d ms is shorter than its recording (%d ms), which is sent in real time: it can never pass", c.Within.Milliseconds(), c.Recording.Milliseconds())
			}
			return nil
		})
	default:
		cos := &CosineExpect{}
		c.Expect.Cosine = cos
		if len(e.VectorCosineAtLeast.Reference) == 0 || len(e.VectorCosineAtLeast.Min) == 0 {
			return refuse("expect.vector_cosine_at_least holds reference and min")
		}
		steps = append(steps, func() error {
			return take(e.VectorCosineAtLeast.Reference, "expect.vector_cosine_at_least.reference", func(v any) error {
				ref, err := vector(v)
				cos.Reference = ref
				return err
			})
		}, func() error {
			return take(e.VectorCosineAtLeast.Min, "expect.vector_cosine_at_least.min", func(v any) error {
				f, isNum := v.(float64)
				if !isNum || f < -1 || f > 1 {
					return errors.New("must be a number from -1 to 1")
				}
				cos.Min = f
				return nil
			})
		})
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return Check{}, err
		}
	}
	return c, nil
}

func sessionInput(v any) (audioPath, text string, err error) {
	m, isObj := v.(map[string]any)
	if !isObj {
		return "", "", errors.New(`must be an object holding "audio", a recording the package carries, or "text", a text to speak`)
	}
	for k := range m {
		if k != "audio" && k != "text" {
			return "", "", fmt.Errorf("holds %q, which a session check's input does not have (audio, text)", k)
		}
	}
	a, hasAudio := m["audio"]
	t, hasText := m["text"]
	switch {
	case hasAudio == hasText:
		return "", "", errors.New(`holds exactly one of "audio" and "text"`)
	case hasAudio:
		p, isString := a.(string)
		if !isString || p == "" {
			return "", "", errors.New("audio must name a member of the package")
		}
		return p, "", nil
	}
	s, isString := t.(string)
	if !isString || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > MaxCheckTextChars {
		return "", "", fmt.Errorf("text must be 1..%d characters, not white space only", MaxCheckTextChars)
	}
	return "", s, nil
}

func sessionEvents(v any, hearsAudio, speaksText bool) (*EventsExpect, error) {
	m, isObj := v.(map[string]any)
	if !isObj || len(m) == 0 {
		return nil, errors.New("must be an object holding at least one of order, transcript_contains, audio_at_least")
	}
	for k := range m {
		if k != "order" && k != "transcript_contains" && k != "audio_at_least" {
			return nil, fmt.Errorf("holds %q, which expect.events does not have (order, transcript_contains, audio_at_least)", k)
		}
	}
	out := &EventsExpect{}
	if o, has := m["order"]; has {
		items, isList := o.([]any)
		if !isList || len(items) < 1 || len(items) > MaxEventOrder {
			return nil, fmt.Errorf("order must list 1..%d event types", MaxEventOrder)
		}
		for _, item := range items {
			typ, isString := item.(string)
			if !isString || !validEventName(typ) {
				return nil, fmt.Errorf("order: %v is not an event type", item)
			}
			telemetry, terminal, input := SessionEventClass(typ)
			switch {
			case telemetry:
				return nil, fmt.Errorf("order names %s, which an engine may shed under pressure: nothing may depend on it", typ)
			case terminal:
				return nil, fmt.Errorf("order names %s, which ends a session and never answers one", typ)
			case input && speaksText:
				return nil, fmt.Errorf("order names %s, which a text's check never hears: it has no input", typ)
			}
			out.Order = append(out.Order, typ)
		}
	}
	if p, has := m["transcript_contains"]; has {
		if speaksText {
			return nil, errors.New("transcript_contains is a recording's expectation; a text's check hears nothing")
		}
		items, isList := p.([]any)
		if !isList || len(items) < 1 || len(items) > MaxTranscriptWords {
			return nil, fmt.Errorf("transcript_contains must list 1..%d phrases", MaxTranscriptWords)
		}
		for _, item := range items {
			phrase, isString := item.(string)
			if !isString || phrase == "" || utf8.RuneCountInString(phrase) > MaxPhraseChars {
				return nil, fmt.Errorf("transcript_contains: each phrase is 1..%d characters", MaxPhraseChars)
			}
			if len(speecheval.Normalize(phrase)) == 0 {
				return nil, fmt.Errorf("transcript_contains: %q holds no word to listen for", phrase)
			}
			out.TranscriptContains = append(out.TranscriptContains, phrase)
		}
	}
	if a, has := m["audio_at_least"]; has {
		if hearsAudio {
			return nil, errors.New("audio_at_least is a text's expectation; a recording's check is judged by what the engine hears")
		}
		o, isObj := a.(map[string]any)
		ms, isInt := integer(o["ms"])
		db, isNum := o["rms_dbfs"].(float64)
		switch {
		case !isObj || len(o) != 2:
			return nil, errors.New(`audio_at_least must be an object of two members, "ms" and "rms_dbfs"`)
		case !isInt || ms < 1 || ms > MaxAudioFloorMS:
			return nil, fmt.Errorf("audio_at_least.ms must be a whole number of milliseconds, 1..%d", MaxAudioFloorMS)
		case !isNum || db < MinAudioFloorDBFS || db > 0:
			return nil, fmt.Errorf("audio_at_least.rms_dbfs must be a number from %d to 0", MinAudioFloorDBFS)
		}
		out.AudioAtLeast = &AudioFloor{MS: ms, RMSDBFS: db}
	}
	return out, nil
}

func validEventName(s string) bool {
	if s == "" || len(s) > maxSessionEventName || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_') {
			return false
		}
	}
	return true
}

func (r *resolver) recording(c *Check) (string, error) {
	rel := c.Audio
	switch {
	case rel == ValidationFile:
		return fmt.Sprintf("%q is validation.json itself; a recording is another member of the package", rel), nil
	case !insideRoot(rel):
		return fmt.Sprintf("%q leaves the install-root", rel), nil
	}
	raw, present, err := r.read(rel)
	if err != nil {
		return "", err
	}
	if !present {
		return fmt.Sprintf("names %s, which the package does not carry", rel), nil
	}
	r.recordings[rel] = len(raw)
	d, why := recordingOf(raw)
	if why != "" {
		return rel + " " + why, nil
	}
	c.WAV, c.Recording = raw, d
	return "", nil
}

func recordingOf(raw []byte) (time.Duration, string) {
	w, err := audio.ReadWAV(raw)
	if err != nil {
		return 0, "is not a WAV recording: " + err.Error()
	}
	if w.Tag != 1 || w.Bits != 16 {
		return 0, "is not 16-bit PCM"
	}
	if !w.Format.Valid() {
		return 0, fmt.Sprintf("holds %d channel(s) at %d Hz; a recording is one or two channels at 8 to 192 kHz", w.Format.Channels, w.Format.Rate)
	}
	if w.Size == 0 || w.Size == audio.WAVStream || int64(w.Size) != int64(len(w.PCM)) {
		return 0, "does not hold exactly the samples its data chunk declares"
	}
	frame := w.Format.BytesPerSample()
	if len(w.PCM)%frame != 0 {
		return 0, "ends inside a sample"
	}
	d := time.Duration(len(w.PCM)/frame) * time.Second / time.Duration(w.Format.Rate)
	if d < MinCheckRecording || d > MaxCheckRecording {
		return 0, fmt.Sprintf("is %d ms long; a recording is %d ms to %d s", d.Milliseconds(), MinCheckRecording.Milliseconds(), int(MaxCheckRecording.Seconds()))
	}
	return d, ""
}

var errRefForm = errors.New(`holds a "$ref" that is not an object of one member, a string`)

func (r *resolver) value(raw json.RawMessage) (v any, viaRef bool, err error) {
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, err
	}
	v, viaRef, err = r.walk(v)
	return v, viaRef, err
}

func (r *resolver) walk(v any) (any, bool, error) {
	switch t := v.(type) {
	case map[string]any:
		if ref, has := t["$ref"]; has {
			s, isString := ref.(string)
			if len(t) != 1 || !isString {
				return nil, false, errRefForm
			}
			got, err := r.ref(s)
			return got, true, err
		}
		via := false
		for k, item := range t {
			got, ref, err := r.walk(item)
			if err != nil {
				return nil, false, err
			}
			t[k], via = got, via || ref
		}
		return t, via, nil
	case []any:
		via := false
		for i, item := range t {
			got, ref, err := r.walk(item)
			if err != nil {
				return nil, false, err
			}
			t[i], via = got, via || ref
		}
		return t, via, nil
	}
	return v, false, nil
}

func (r *resolver) ref(s string) (any, error) {
	rel, pointer, hasFragment := strings.Cut(s, "#")
	if !hasFragment {
		pointer = ""
	}
	switch {
	case rel == "" || rel == ValidationFile:
		return nil, &unresolved{why: fmt.Sprintf("$ref %q names validation.json itself; a $ref names another member of the package", s)}
	case !insideRoot(rel):
		return nil, &unresolved{why: fmt.Sprintf("$ref %q leaves the install-root", s)}
	case !validPointer(pointer):
		return nil, &unresolved{why: fmt.Sprintf("$ref %q: %q is not a JSON pointer", s, pointer)}
	}
	doc, err := r.doc(rel)
	if err != nil {
		return nil, err
	}
	got, found := Pointer(doc, pointer)
	if !found {
		return nil, &unresolved{why: fmt.Sprintf("$ref %q points at nothing in %s", s, rel)}
	}
	return copyJSON(got), nil
}

func (r *resolver) doc(rel string) (any, error) {
	if d, ok := r.docs[rel]; ok {
		return d, nil
	}
	if why, ok := r.bad[rel]; ok {
		return nil, &unresolved{why: why}
	}
	raw, present, err := r.read(rel)
	if err != nil {
		return nil, err
	}
	var why string
	var d any
	switch {
	case !present:
		why = fmt.Sprintf("$ref names %s, which the package does not carry", rel)
	case canonicaljson.DecodeStrict(raw, &d) != nil:
		why = fmt.Sprintf("$ref names %s, which is not JSON", rel)
	}
	if why != "" {
		if r.bad == nil {
			r.bad = map[string]string{}
		}
		r.bad[rel] = why
		return nil, &unresolved{why: why}
	}
	r.docs[rel] = d
	return d, nil
}

func insideRoot(rel string) bool {
	return !strings.HasPrefix(rel, "/") && !strings.Contains(rel, `\`) && !strings.Contains(rel, ":") &&
		path.Clean(rel) == rel && rel != ".." && !strings.HasPrefix(rel, "../")
}

func validPointer(p string) bool {
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' && (i+1 >= len(p) || (p[i+1] != '0' && p[i+1] != '1')) {
			return false
		}
	}
	return true
}

func Pointer(doc any, p string) (any, bool) {
	if !validPointer(p) {
		return nil, false
	}
	if p == "" {
		return doc, true
	}
	cur := doc
	for _, token := range strings.Split(p[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch t := cur.(type) {
		case map[string]any:
			next, ok := t[token]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			if token == "" || (len(token) > 1 && token[0] == '0') {
				return nil, false
			}
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(t) || strconv.Itoa(i) != token {
				return nil, false
			}
			cur = t[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func JSONEqual(a, b any) bool {
	ra, err1 := json.Marshal(a)
	rb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ra, rb)
}

func copyJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = copyJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = copyJSON(item)
		}
		return out
	}
	return v
}

func validCheckName(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > MaxCheckNameChars {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

func integer(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

func containsList(v any) ([]string, error) {
	const want = "must be a string, or a list of strings, none empty"
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil, errors.New(want)
		}
		return []string{t}, nil
	case []any:
		if len(t) == 0 {
			return nil, errors.New(want)
		}
		out := make([]string, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, errors.New(want)
			}
			out[i] = s
		}
		return out, nil
	}
	return nil, errors.New(want)
}

func vector(v any) ([]float64, error) {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("must be a list of numbers")
	}
	out := make([]float64, len(items))
	var norm float64
	for i, item := range items {
		f, isNum := item.(float64)
		if !isNum {
			return nil, fmt.Errorf("must be a list of numbers; entry %d is not one", i+1)
		}
		out[i] = f
		norm += f * f
	}
	if norm == 0 || math.IsInf(norm, 0) {
		return nil, errors.New("must be a vector a cosine can be taken against: not all zeros, and its length a number")
	}
	return out, nil
}
