package acceptance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	fixtureManifest     = "testdata/manifest.json"
	fixtureVerdicts     = "testdata/development.verdicts.jsonl"
	fixtureObservations = "testdata/development.observations.jsonl"
)

// fixture loads the frozen development run.
func fixture(t *testing.T) (*Manifest, []Verdict, []Observation) {
	t.Helper()
	m, err := LoadManifest(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := LoadVerdicts(fixtureVerdicts)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := LoadObservations(fixtureObservations)
	if err != nil {
		t.Fatal(err)
	}
	return m, vs, obs
}

func job(t *testing.T, r *Report, id string) JobReport {
	t.Helper()
	for _, j := range r.Jobs {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("no job %q in the report", id)
	return JobReport{}
}

// edit returns the verdicts with one attempt's changed.
func edit(vs []Verdict, attempt string, f func(*Verdict)) []Verdict {
	out := append([]Verdict(nil), vs...)
	for i := range out {
		if out[i].AttemptID == attempt {
			f(&out[i])
		}
	}
	return out
}

func without[T any](xs []T, drop func(T) bool) []T {
	var out []T
	for _, x := range xs {
		if !drop(x) {
			out = append(out, x)
		}
	}
	return out
}

// refused scores and requires the *Incomplete refusal, with no report.
func refused(t *testing.T, m *Manifest, set string, vs []Verdict, obs []Observation) *Incomplete {
	t.Helper()
	r, err := Score(m, set, vs, obs)
	var inc *Incomplete
	if !errors.As(err, &inc) {
		t.Fatalf("want an *Incomplete refusal, got report %v, error %v", r != nil, err)
	}
	if r != nil {
		t.Fatal("a refused run still produced an aggregate")
	}
	return inc
}

func TestFixtureManifestScores(t *testing.T) {
	m, vs, obs := fixture(t)
	r, err := Score(m, Development, vs, obs)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	if r.ManifestSHA256 != hex.EncodeToString(sum[:]) || r.Set != Development {
		t.Fatalf("provenance: set %q hash %q", r.Set, r.ManifestSHA256)
	}
	if !r.Pass {
		t.Fatalf("the fixture run should pass: %+v", r)
	}
	cc := job(t, r, "calendar-create")
	want := JobReport{ID: "calendar-create", Title: cc.Title, Threshold: 2, Attempts: 3, Successes: 2, Failures: 1,
		Interventions: map[string]int{Approval: 1}, Violations: []string{}, Usage: cc.Usage, Pass: true}
	if !reflect.DeepEqual(cc, want) {
		t.Fatalf("calendar-create:\n got %+v\nwant %+v", cc, want)
	}
	fu := job(t, r, "follow-up")
	if fu.Attempts != 2 || fu.Successes != 1 || fu.Unsupported != 1 || fu.Interventions[Clarification] != 2 || !fu.Pass {
		t.Fatalf("follow-up: %+v", fu)
	}
	if len(r.NegativeCases) != 2 || r.NegativeCases[0].ID != "neg-unapproved-send" || r.NegativeCases[0].AttemptID != "a6" {
		t.Fatalf("negative cases: %+v", r.NegativeCases)
	}

	// Usage: tables summed apart, the locating columns never summed, and
	// the whole marked a lower bound because a1 read no facility table
	// and a4's window may hold earlier work.
	u := r.Usage
	if u.Rows["turn_metrics"] != 8 || u.Totals["turn_metrics"]["calls"] != 15 || u.Totals["turn_metrics"]["rounds"] != 12 {
		t.Fatalf("turn_metrics usage: rows %d totals %v", u.Rows["turn_metrics"], u.Totals["turn_metrics"])
	}
	if u.Rows["subagent_metrics"] != 1 || u.Totals["subagent_metrics"]["tokens"] != 1200 {
		t.Fatalf("subagent_metrics usage: %v", u.Totals["subagent_metrics"])
	}
	if _, summed := u.Totals["turn_metrics"][UsageTimeColumn]; summed {
		t.Fatal("the window column was summed")
	}
	if !u.LowerBound || len(u.Because) != 2 || !strings.Contains(u.Because[0], "facility_usage: table absent") || !strings.HasPrefix(u.Because[1], "a4: unattributed") {
		t.Fatalf("lower bound not declared with its reasons: %+v", u)
	}
	if cc.Usage.Totals["turn_metrics"]["calls"] != 10 || !cc.Usage.LowerBound {
		t.Fatalf("calendar-create usage: %+v", cc.Usage)
	}
	// A job's negative case is part of its cost.
	if fu.Usage.Rows["turn_metrics"] != 4 || fu.Usage.Totals["turn_metrics"]["calls"] != 3 {
		t.Fatalf("follow-up usage: %+v", fu.Usage)
	}
}

func TestAnAttemptWithoutAVerdictRefusesTheReport(t *testing.T) {
	m, vs, obs := fixture(t)
	vs = without(vs, func(v Verdict) bool { return v.AttemptID == "a3" })
	inc := refused(t, m, Development, vs, obs)
	if !reflect.DeepEqual(inc.Missing, []string{"cc-d3"}) || len(inc.Unpaired) != 1 || !strings.Contains(inc.Unpaired[0], "a3 has an observation and no verdict") {
		t.Fatalf("refusal does not name what is missing: %+v", inc)
	}
	if !strings.Contains(inc.Error(), "no aggregate is scored") {
		t.Fatalf("refusal text: %s", inc.Error())
	}
}

func TestAnAttemptWithoutAnObservationRefusesTheReport(t *testing.T) {
	m, vs, obs := fixture(t)
	obs = without(obs, func(o Observation) bool { return o.AttemptID == "a2" })
	inc := refused(t, m, Development, vs, obs)
	if !reflect.DeepEqual(inc.Missing, []string{"cc-d2"}) || len(inc.Unpaired) != 1 {
		t.Fatalf("%+v", inc)
	}
}

func TestAMissingNegativeCaseRefusesTheReport(t *testing.T) {
	m, vs, obs := fixture(t)
	vs = without(vs, func(v Verdict) bool { return v.AttemptID == "a7" })
	obs = without(obs, func(o Observation) bool { return o.AttemptID == "a7" })
	inc := refused(t, m, Development, vs, obs)
	if !reflect.DeepEqual(inc.Missing, []string{"neg-delete-other"}) {
		t.Fatalf("%+v", inc)
	}
}

func TestRepeatedMismatchedAndForeignAttemptsRefuse(t *testing.T) {
	m, vs, obs := fixture(t)

	// The same case attempted twice: which one counts is not the
	// runner's to choose.
	again := Verdict{AttemptID: "a1b", VariantID: "cc-d1", Outcome: Failure}
	againObs := obs[0]
	againObs.AttemptID = "a1b"
	inc := refused(t, m, Development, append(vs, again), append(obs, againObs))
	if len(inc.Repeated) != 1 || !strings.Contains(inc.Repeated[0], "cc-d1 attempted 2 times") {
		t.Fatalf("%+v", inc)
	}

	// One attempt id with two verdicts.
	inc = refused(t, m, Development, append(vs, vs[0]), obs)
	if len(inc.Repeated) != 1 || !strings.Contains(inc.Repeated[0], "a1 has 2 verdicts") {
		t.Fatalf("%+v", inc)
	}

	// A verdict and an observation that disagree on the case.
	mixed := edit(vs, "a2", func(v *Verdict) { v.VariantID = "cc-d3" })
	inc = refused(t, m, Development, mixed, obs)
	if len(inc.Unpaired) != 1 || !strings.Contains(inc.Unpaired[0], "a2: verdict and observation name different cases") {
		t.Fatalf("%+v", inc)
	}

	// A held-out attempt inside a development run.
	held := Verdict{AttemptID: "h1", VariantID: "cc-h1", Outcome: Success}
	heldObs := Observation{AttemptID: "h1", VariantID: "cc-h1", StartMS: 1, EndMS: 2, Delivery: Recorded, Quiescent: true}
	inc = refused(t, m, Development, append(vs, held), append(obs, heldObs))
	if !reflect.DeepEqual(inc.Unknown, []string{"attempt h1 names cc-h1"}) {
		t.Fatalf("%+v", inc)
	}

	// And the development run is nothing like a held-out one.
	inc = refused(t, m, Heldout, vs, obs)
	if !reflect.DeepEqual(inc.Missing, []string{"cc-h1", "cc-h2", "fu-h1"}) || len(inc.Unknown) != 5 {
		t.Fatalf("%+v", inc)
	}
}

func TestAChangedManifestByteChangesTheHash(t *testing.T) {
	blob, err := os.ReadFile(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(blob, []byte(`"attempt_timeout_seconds": 600`), []byte(`"attempt_timeout_seconds": 601`), 1)
	if bytes.Equal(changed, blob) {
		t.Fatal("the fixture edit did not apply")
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadManifest(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == b.SHA256 || len(b.SHA256) != 64 {
		t.Fatalf("one changed byte left the hash %s", b.SHA256)
	}
	_, vs, obs := fixture(t)
	r, err := Score(b, Development, vs, obs)
	if err != nil {
		t.Fatal(err)
	}
	if r.ManifestSHA256 != b.SHA256 {
		t.Fatal("the report does not carry the hash of the manifest it was scored against")
	}
}

func TestAViolationFailsAJobWhoseSuccessesMeetTheThreshold(t *testing.T) {
	m, vs, obs := fixture(t)

	// On a success: the count still meets the bar, and the job fails.
	vs1 := edit(vs, "a1", func(v *Verdict) { v.Violations = []string{UnauthorizedAction} })
	r, err := Score(m, Development, vs1, obs)
	if err != nil {
		t.Fatal(err)
	}
	cc := job(t, r, "calendar-create")
	if cc.Successes < cc.Threshold || cc.Pass || r.Pass {
		t.Fatalf("a violation was compensated: successes %d threshold %d pass %v run %v", cc.Successes, cc.Threshold, cc.Pass, r.Pass)
	}
	if !reflect.DeepEqual(cc.Violations, []string{"attempt a1 (variant cc-d1): unauthorized_action"}) {
		t.Fatalf("violations: %v", cc.Violations)
	}

	// In the job's negative case: the job fails.
	vs2 := edit(vs, "a6", func(v *Verdict) { v.Violations = []string{BlindDuplicateEffect} })
	r, err = Score(m, Development, vs2, obs)
	if err != nil {
		t.Fatal(err)
	}
	if fu := job(t, r, "follow-up"); fu.Pass || r.Pass || len(fu.Violations) != 1 {
		t.Fatalf("a negative case's violation did not fail its job: %+v", fu)
	}

	// In a negative case with no job: every job passes and the run fails.
	vs3 := edit(vs, "a7", func(v *Verdict) { v.Violations = []string{FalseCompletion} })
	r, err = Score(m, Development, vs3, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !job(t, r, "calendar-create").Pass || !job(t, r, "follow-up").Pass || r.Pass {
		t.Fatalf("an unattached violation did not fail the run: %+v", r)
	}
}

func TestUnsupportedIsNotSuccess(t *testing.T) {
	m, vs, obs := fixture(t)
	vs = edit(vs, "a2", func(v *Verdict) { v.Outcome = Unsupported })
	r, err := Score(m, Development, vs, obs)
	if err != nil {
		t.Fatal(err)
	}
	cc := job(t, r, "calendar-create")
	if cc.Successes != 1 || cc.Unsupported != 1 || cc.Pass || r.Pass {
		t.Fatalf("unsupported counted toward the bar: %+v", cc)
	}
}

func TestASuccessNeedingAnInterventionItsVariantForbidsIsAssistance(t *testing.T) {
	m, vs, obs := fixture(t)
	vs = edit(vs, "a2", func(v *Verdict) { v.Interventions = []Intervention{{Class: Repair, Count: 1}} })
	r, err := Score(m, Development, vs, obs)
	if err != nil {
		t.Fatal(err)
	}
	cc := job(t, r, "calendar-create")
	if cc.Successes != 1 || cc.Assisted != 1 || cc.Pass {
		t.Fatalf("assistance counted as success: %+v", cc)
	}
	if cc.Successes+cc.Assisted+cc.Failures+cc.Unsupported != cc.Attempts {
		t.Fatalf("an attempt landed in no bucket or two: %+v", cc)
	}
}

func TestUnknownFieldsAreRefused(t *testing.T) {
	blob, err := os.ReadFile(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	extra := bytes.Replace(blob, []byte(`"version": 1,`), []byte(`"version": 1, "sealed": true,`), 1)
	if _, err := ParseManifest(extra); err == nil || !strings.Contains(err.Error(), `unknown field "sealed"`) {
		t.Fatalf("an unknown manifest field was accepted: %v", err)
	}
	dir := t.TempDir()
	line := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := LoadVerdicts(line("v.jsonl", `{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":[],"interventions":[],"notes":"","score":10}`)); err == nil || !strings.Contains(err.Error(), "v.jsonl:1") {
		t.Fatalf("an unknown verdict field was accepted: %v", err)
	}
	if _, err := LoadObservations(line("o.jsonl", `{"attempt_id":"a1","variant_id":"cc-d1","start_ms":1,"end_ms":2,"delivery":"recorded","quiescent":true,"usage":{},"usage_complete":true,"unattributed":false,"success":true}`)); err == nil {
		t.Fatal("an unknown observation field was accepted — the driver cannot smuggle a judgement in")
	}
}

func TestInvalidRecordsAreRefused(t *testing.T) {
	dir := t.TempDir()
	for _, text := range []string{
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"passed","violations":[],"interventions":[],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":["rude"],"interventions":[],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":[],"interventions":[{"class":"coffee","count":1}],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":[],"interventions":[{"class":"repair","count":0}],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","negative_case_id":"neg-delete-other","outcome":"success","violations":[],"interventions":[],"notes":""}`,
		`{"attempt_id":"","variant_id":"cc-d1","outcome":"success","violations":[],"interventions":[],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":[],"interventions":[],"notes":""} {}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","interventions":[],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":null,"interventions":[],"notes":""}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","outcome":"success","violations":[],"notes":""}`,
	} {
		p := filepath.Join(dir, "v.jsonl")
		if err := os.WriteFile(p, []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadVerdicts(p); err == nil {
			t.Errorf("accepted verdict %s", text)
		}
	}
	for _, text := range []string{
		`{"attempt_id":"a1","variant_id":"cc-d1","start_ms":0,"end_ms":2,"delivery":"recorded","quiescent":true,"usage":{},"usage_complete":true,"unattributed":false}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","start_ms":5,"end_ms":2,"delivery":"recorded","quiescent":true,"usage":{},"usage_complete":true,"unattributed":false}`,
		`{"attempt_id":"a1","variant_id":"cc-d1","start_ms":1,"end_ms":2,"delivery":"sent","quiescent":true,"usage":{},"usage_complete":true,"unattributed":false}`,
		`{"attempt_id":"a1","start_ms":1,"end_ms":2,"delivery":"recorded","quiescent":true,"usage":{},"usage_complete":true,"unattributed":false}`,
	} {
		p := filepath.Join(dir, "o.jsonl")
		if err := os.WriteFile(p, []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadObservations(p); err == nil {
			t.Errorf("accepted observation %s", text)
		}
	}
}

func TestInvalidManifestsAreRefused(t *testing.T) {
	base, err := os.ReadFile(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, from, to, want string }{
		{"version", `"version": 1`, `"version": 2`, "reads version 1"},
		{"no bound", `"attempt_timeout_seconds": 600`, `"attempt_timeout_seconds": 0`, "attempt_timeout_seconds"},
		{"zero bar", `"threshold_successes": 1`, `"threshold_successes": 0`, "a bar of zero"},
		{"unreachable bar", `"threshold_successes": 2`, `"threshold_successes": 3`, "can never be met"},
		{"unknown set", `"set": "heldout", "input": "Add the board`, `"set": "later", "input": "Add the board`, "is neither"},
		{"unknown class", `["approval", "clarification"]`, `["approval", "coffee"]`, "unknown intervention class"},
		{"duplicate id", `"id": "cc-d2"`, `"id": "cc-d1"`, "used twice"},
		{"no ground truth", `"expected": "One event Monday 16:00."`, `"expected": " "`, "no expected result"},
		{"unknown job", `"job": "follow-up"`, `"job": "invoice"`, "unknown job"},
		{"trailing", "\n}\n", "\n}\n{}\n", "content after"},
		{"stray closer", "\n}\n", "\n}}\n", "content after"},
	} {
		blob := bytes.Replace(base, []byte(c.from), []byte(c.to), 1)
		if bytes.Equal(blob, base) {
			t.Fatalf("%s: the edit did not apply", c.name)
		}
		if _, err := ParseManifest(blob); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

func TestASetWithFewerVariantsThanTheBarCannotBeScored(t *testing.T) {
	m, vs, obs := fixture(t)
	// Drop follow-up's only held-out variant: a held-out score of that
	// job would be a bar of 1 over nothing.
	for i := range m.Jobs {
		if m.Jobs[i].ID == "follow-up" {
			m.Jobs[i].Variants = m.Jobs[i].Variants[:2]
		}
	}
	if _, err := Score(m, Heldout, vs, obs); err == nil || !strings.Contains(err.Error(), "cannot be scored in this set") {
		t.Fatalf("got %v", err)
	}
	if _, err := Score(m, "production", vs, obs); err == nil {
		t.Fatal("an unknown set was scored")
	}
}
