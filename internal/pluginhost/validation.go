package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type CheckLanes struct {
	Operation func(ctx context.Context, operation string, args map[string]any) (tools.Result, error)
	Embed     func(ctx context.Context, kind, text string) ([]float32, error)

	Session func(ctx context.Context, id string, c packagefmt.Check) (SessionAnswer, error)
}

type CheckResult struct {
	Name    string
	Outcome pluginfacility.CheckOutcome
	Cosine  *float64
	Took    time.Duration
	Words   string
	Err     error
}

func RunChecks(ctx context.Context, checks []packagefmt.Check, lanes CheckLanes, now func() time.Time) []CheckResult {
	if now == nil {
		now = time.Now
	}
	var out []CheckResult
	for i, c := range checks {
		r := runCheck(ctx, i+1, c, lanes, now)
		out = append(out, r)
		if r.Outcome.Fixed() {
			break
		}
	}
	return out
}

func Verdict(results []CheckResult) *pluginfacility.CheckFailure {
	var first *pluginfacility.CheckFailure
	for _, r := range results {
		if r.Outcome == pluginfacility.CheckPassed {
			continue
		}
		f := &pluginfacility.CheckFailure{Check: r.Name, Outcome: r.Outcome, Words: r.Words}
		if r.Outcome.Fixed() {
			return f
		}
		if first == nil {
			first = f
		}
	}
	return first
}

func runCheck(ctx context.Context, n int, c packagefmt.Check, lanes CheckLanes, now func() time.Time) CheckResult {
	r := CheckResult{Name: c.Name}
	if c.Unresolved != "" {
		r.Outcome, r.Words = pluginfacility.CheckUnresolved, c.Unresolved
		return r
	}
	if err := ctx.Err(); err != nil {
		r.Outcome, r.Err, r.Words = pluginfacility.CheckUnanswered, err, "not asked: the start it belonged to ended"
		return r
	}
	sent := now()
	switch c.Lane {
	case packagefmt.LaneEmbeddings:
		if lanes.Embed == nil {
			r.Outcome, r.Words = pluginfacility.CheckUnanswered, "not asked: this activation answers no embeddings lane"
			return r
		}
		got, err := lanes.Embed(ctx, c.Kind, c.Text)
		r.Took = now().Sub(sent)
		judgeVector(&r, c, got, err)
	case packagefmt.LaneOperation:
		if lanes.Operation == nil {
			r.Outcome, r.Words = pluginfacility.CheckUnanswered, "not asked: this activation answers no operation lane"
			return r
		}
		res, err := lanes.Operation(ctx, c.Operation, argsCopy(c.Arguments))
		r.Took = now().Sub(sent)
		judgeAnswer(&r, c, res, err)
	case packagefmt.LaneSession:
		if lanes.Session == nil {
			r.Outcome, r.Words = pluginfacility.CheckUnanswered, "not asked: this activation answers no session lane"
			return r
		}
		ans, err := lanes.Session(ctx, fmt.Sprintf("check-%d", n), c)
		r.Took = ans.Took
		judgeSessionCheck(&r, c, ans, err)
	default:

		r.Outcome, r.Words = pluginfacility.CheckUnanswered, fmt.Sprintf("not run by this host: its lane %q is one this host has no path for", c.Lane)
		return r
	}
	if r.Outcome == pluginfacility.CheckPassed && c.Within > 0 && r.Took > c.Within {
		r.Outcome = pluginfacility.CheckTimedOut
		r.Words = fmt.Sprintf("answered as its publisher says in %s, past its bound of %s", millis(r.Took), millis(c.Within))
	}
	return r
}

func judgeVector(r *CheckResult, c packagefmt.Check, got []float32, err error) {
	var reply *EmbedReplyError
	var call *EmbedCallError
	switch {
	case err == nil:
		cos := vec.FloatCosine(got, c.Expect.Cosine.Reference)
		r.Cosine = &cos

		if cos >= c.Expect.Cosine.Min {
			r.Outcome = pluginfacility.CheckPassed
			return
		}
		r.Outcome = pluginfacility.CheckWrong
		r.Words = fmt.Sprintf("came back at %.3f of its reference, under the %.3f it asks", cos, c.Expect.Cosine.Min)
	case errors.As(err, &reply):
		r.Outcome, r.Err = pluginfacility.CheckWrong, err
		r.Words = "answered with something that is not a vector: " + reply.Detail
	case errors.As(err, &call) && call.Kind == EmbedTimedOut:
		r.Outcome, r.Err = pluginfacility.CheckTimedOut, err
		r.Words = fmt.Sprintf("did not answer within the %s a call is given", millis(r.Took))
	default:
		r.Outcome, r.Err = pluginfacility.CheckUnanswered, err
		r.Words = "not answered: " + err.Error()
	}
}

func judgeSessionCheck(r *CheckResult, c packagefmt.Check, ans SessionAnswer, err error) {
	var unended *SessionUnendedError
	if errors.As(err, &unended) {
		r.Err, err = unended, unended.Cause
	}
	var formats *FormatRefusedError
	switch {
	case errors.As(err, &formats):
		r.Outcome, r.Err = pluginfacility.CheckWrong, err
		r.Words = "the engine admitted the session with audio formats the host cannot carry: " + excerptText(formats.Error())
	case IsAdmissionUnknown(err):
		r.Outcome, r.Err = pluginfacility.CheckTimedOut, err
		r.Words = "a control's admission was unknown at its bound: " + excerptText(err.Error())
	case err != nil:
		r.Outcome, r.Err = pluginfacility.CheckUnanswered, err
		r.Words = "not answered: " + err.Error()
	default:
		r.Outcome, r.Words = judgeSession(c.Expect.Events, c.Text != "", ans)
	}
	if unended != nil && r.Outcome != pluginfacility.CheckWrong {
		r.Outcome = pluginfacility.CheckTimedOut
		if r.Words != "" {
			r.Words += "; "
		}
		r.Words += "the engine did not end the check's session after its abort"
	}
}

func judgeAnswer(r *CheckResult, c packagefmt.Check, res tools.Result, err error) {
	var timeout *supervisor.InvokeTimeoutError
	var contract *ResponseContractError
	switch {
	case errors.As(err, &timeout):
		r.Outcome, r.Err = pluginfacility.CheckTimedOut, err
		r.Words = fmt.Sprintf("did not answer within the %s a call is given", millis(r.Took))
		return
	case errors.As(err, &contract):
		r.Outcome, r.Err = pluginfacility.CheckWrong, err
		r.Words = "answered with something that is not a response: " + contract.Requirement
		return
	case err != nil:
		r.Outcome, r.Err = pluginfacility.CheckUnanswered, err
		r.Words = "not answered: " + err.Error()
		return
	case res.Refused || res.ReasonCode == ReasonCancelledBeforeDispatch:
		r.Outcome = pluginfacility.CheckUnanswered
		r.Words = "not answered: the host refused the call before anything ran: " + res.Error
		return
	case res.Error != "":
		r.Outcome = pluginfacility.CheckWrong
		r.Words = "answered with an error: " + excerptText(res.Error)
		return
	}
	if words := expectHolds(c.Expect, res.Output); words != "" {
		r.Outcome, r.Words = pluginfacility.CheckWrong, words
		return
	}
	r.Outcome = pluginfacility.CheckPassed
}

func expectHolds(e packagefmt.Expect, output string) string {
	if len(e.Contains) > 0 {
		for _, w := range e.Contains {
			if !strings.Contains(output, w) {
				return fmt.Sprintf("its answer lacks %q: %s", w, excerptText(output))
			}
		}
		return ""
	}
	j := e.JSON
	if j == nil {
		return "the check expects nothing an operation answers"
	}
	var doc any
	if err := json.Unmarshal([]byte(output), &doc); err != nil {
		return "its answer is not JSON: " + excerptText(output)
	}
	got, found := packagefmt.Pointer(doc, j.Path)
	if !found {
		return fmt.Sprintf("its answer holds nothing at %q: %s", j.Path, excerptText(output))
	}
	if j.HasEquals() {
		if !packagefmt.JSONEqual(got, j.Equals) {
			want, _ := json.Marshal(j.Equals)
			have, _ := json.Marshal(got)
			return fmt.Sprintf("the value at %q is %s, not %s", j.Path, excerptText(string(have)), excerptText(string(want)))
		}
		return ""
	}
	n, isNum := got.(float64)
	switch {
	case !isNum:
		return fmt.Sprintf("the value at %q is not a number", j.Path)
	case !(n >= *j.AtLeast):
		return fmt.Sprintf("the value at %q is %g, under %g", j.Path, n, *j.AtLeast)
	}
	return ""
}

func argsCopy(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func excerptText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= 160 {
		return s
	}
	return string([]rune(s)[:160]) + "…"
}

func millis(d time.Duration) string { return fmt.Sprintf("%d ms", d.Milliseconds()) }

type ValidationError struct {
	PluginID string
	Detail   string
	Unread   bool
	Cause    error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("plugin %s: its publisher's checks are refused: %s", e.PluginID, e.Detail)
}

func (e *ValidationError) Unwrap() error { return e.Cause }

func (e *ValidationError) WaitsOnPackage() bool { return !e.Unread }

func loadValidation(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest, descs *descriptorSet, source *Embeddings) (*packagefmt.Validation, error) {
	if _, present := res.FileDigests[packagefmt.ValidationFile]; !present {
		return nil, nil
	}
	refuse := func(err error) (*packagefmt.Validation, error) {
		var unread *EntrypointDigestError
		return nil, &ValidationError{PluginID: m.ID, Detail: err.Error(), Unread: errors.As(err, &unread), Cause: err}
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, packagefmt.ValidationFile)
	if err != nil {
		return refuse(err)
	}
	v, err := packagefmt.ParseValidation(raw, func(rel string) ([]byte, bool, error) {
		if _, listed := res.FileDigests[rel]; !listed {
			return nil, false, nil
		}
		b, err := loadVerifiedMember(pkgPath, res, held, rel)
		return b, err == nil, err
	})
	if err != nil {
		return refuse(err)
	}
	declared := map[string]bool{}
	for _, d := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		if d.ID == SessionInterfaceID {
			continue
		}
		for _, method := range d.Methods {
			declared[method] = true
		}
	}
	for i := range v.Checks {
		c := &v.Checks[i]
		named := fmt.Sprintf("check %q", c.Name)
		for _, s := range c.Sets {
			if s != packagefmt.AllSets && variantByID(m, s) == nil {
				return refuse(fmt.Errorf("%s names component set %q, which the package does not declare", named, s))
			}
		}
		switch c.Lane {
		case packagefmt.LaneSession:

			if !servesSessionLane(m) {
				return refuse(fmt.Errorf("%s is sent on the session lane, and the package's activation has no session lane", named))
			}
			for _, s := range c.Sets {
				for _, vr := range m.Variants {
					if (s == packagefmt.AllSets || s == vr.VariantID) && vr.ExecutionRuntime != "native_t3_component" {
						return refuse(fmt.Errorf("%s names set %q, which is not a native component set: a resident session runs on one alone", named, vr.VariantID))
					}
				}
			}
		case packagefmt.LaneEmbeddings:
			if source == nil {
				return refuse(fmt.Errorf("%s is sent on the embeddings lane, and the package is not a source of vectors", named))
			}
			if c.Unresolved != "" {
				continue
			}
			switch {
			case c.Kind != EmbedKindMemory && c.Kind != EmbedKindCue:
				c.Unresolved = fmt.Sprintf("input: kind %q is neither %s nor %s", c.Kind, EmbedKindMemory, EmbedKindCue)
			case utf8.RuneCountInString(c.Text) > MaxEmbedChars || strings.TrimSpace(c.Text) == "":
				c.Unresolved = fmt.Sprintf("input: the text must be 1..%d characters, not white space only", MaxEmbedChars)
			case len(c.Expect.Cosine.Reference) != source.Decl.Dimension:
				c.Unresolved = fmt.Sprintf("its reference holds %d numbers; the signed dimension is %d", len(c.Expect.Cosine.Reference), source.Decl.Dimension)
			}
		case packagefmt.LaneOperation:
			d := descs.ops[c.Operation]
			switch {
			case !declared[c.Operation]:
				return refuse(fmt.Errorf("%s calls operation %q, which the package does not declare", named, c.Operation))
			case d == nil || !broker.IsReadEffect(d.effects):
				effects := "none"
				if d != nil && d.effects != "" {
					effects = d.effects
				}
				return refuse(fmt.Errorf("%s calls operation %q, whose declared effects are %s: a check calls only an operation that reads", named, c.Operation, effects))
			case d.operatorConfirms:
				return refuse(fmt.Errorf("%s calls operation %q, which waits for the operator's confirmation: a check has no one to confirm it", named, c.Operation))
			}
			if c.Unresolved == "" && d.input != nil {
				if verr := d.input.Validate(jsonValue(c.Arguments)); verr != nil {
					c.Unresolved = "call.arguments are outside the operation's declared input: " + verr.Error()
				}
			}
		}
	}
	return v, nil
}

func (ap *ActivePlugin) checkLanes(embedDeadline func(kind string) time.Duration) CheckLanes {
	tool := func(operation string) *operationTool {
		for _, pt := range ap.pending {
			if pt.tool.operation == operation {
				return pt.tool
			}
		}
		return nil
	}
	call := func(ctx context.Context, operation string, args map[string]any, deadline time.Duration) (tools.Result, error) {
		t := tool(operation)
		if t == nil {
			return tools.Result{}, fmt.Errorf("plugin %s: %q is not an operation this release answers", ap.ID, operation)
		}
		ctx, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()
		return t.check(ctx, args)
	}
	lanes := CheckLanes{Operation: func(ctx context.Context, operation string, args map[string]any) (tools.Result, error) {
		return call(ctx, operation, args, bbb.DefaultInvokeTimeout)
	}}
	lanes.Session = ap.sessionLane()
	if src := ap.Embeddings; src != nil {
		lanes.Embed = func(ctx context.Context, kind, text string) ([]float32, error) {
			deadline := bbb.DefaultInvokeTimeout
			if embedDeadline != nil {
				deadline = embedDeadline(kind)
			}
			res, err := call(ctx, MethodEmbed, map[string]any{"kind": kind, "text": text}, deadline)
			return src.answer(ctx, res, err)
		}
	}
	return lanes
}

func (h *Runtime) Validate(ctx context.Context, r pluginfacility.Running) (pluginfacility.Checks, error) {
	act, ok := r.(*Activation)
	if !ok || act.Plugin == nil {
		return pluginfacility.Checks{}, nil
	}
	ap := act.Plugin
	out := pluginfacility.Checks{Variant: ap.VariantID}
	if ap.validation == nil {
		return out, nil
	}
	checks := slices.DeleteFunc(slices.Clone(ap.validation.Checks), func(c packagefmt.Check) bool { return !c.For(ap.VariantID) })
	if len(checks) == 0 {
		return out, nil
	}
	results := RunChecks(ctx, checks, ap.checkLanes(h.w.EmbedDeadline), nil)
	out.Ran, out.Failed = len(checks), Verdict(results)
	var unended error
	for _, res := range results {
		cos := ""
		if res.Cosine != nil {
			cos = fmt.Sprintf(" cosine %.3f", *res.Cosine)
		}
		logsink.Info("plugins.decision", "plugin %s %s, set %s: its publisher's check %q %s in %s%s %s", ap.ID, ap.Version, ap.VariantID, res.Name, res.Outcome, millis(res.Took), cos, res.Words)
		var u *SessionUnendedError
		if unended == nil && errors.As(res.Err, &u) {
			unended = u
		}
	}
	ap.checked.Store(&out)

	return out, unended
}

func (ap *ActivePlugin) Checked() *pluginfacility.Checks { return ap.checked.Load() }

func (ap *ActivePlugin) DeclaresChecks() bool {
	if ap.validation == nil {
		return false
	}
	return slices.ContainsFunc(ap.validation.Checks, func(c packagefmt.Check) bool { return c.For(ap.VariantID) })
}
