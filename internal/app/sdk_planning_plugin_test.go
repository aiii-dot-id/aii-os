//go:build !android && !ios

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// TestSDKPlanningPlugin boots the actual authored native package under a
// disposable platform test root. No released package, identity or key changes.
// Fixture records are host-written synthetic facts, not claims of model work.
func TestSDKPlanningPlugin(t *testing.T) {
	dir := os.Getenv("AII_PLANNING_PLUGIN")
	if dir == "" {
		t.Skip("set AII_PLANNING_PLUGIN to the staged package and worker directory")
	}
	b := planningPackageFixture(t, dir)
	t.Run("host_floor", func(t *testing.T) {
		root, err := packagefmt.PinnedOrShipped(b.root, packagefmt.KeyTypePlatformRelease)
		if err != nil {
			t.Fatal(err)
		}
		roots := packagefmt.TrustRoots{PlatformRelease: root}
		roots.Revocation = packagefmt.LoadRevocationStatus(filepath.Dir(b.status), roots, nil)
		_, err = pluginhost.Verify(b.v1, &pluginhost.Options{Roots: roots, HostVersion: "0.1.18"})
		var floor *pluginhost.HostVersionError
		if !errors.As(err, &floor) || !floor.HostTooOld || floor.Min != "0.1.19" {
			t.Fatalf("actual package must refuse the old host: %v", err)
		}
	})
	for _, arm := range []struct{ name, archive string }{{"baseline_A", "baseline-A.aiiospkg"}, {"interface_B", "interface-B.aiiospkg"}} {
		t.Run(arm.name+"_legacy_calibration", func(t *testing.T) {
			legacy := planningPackageFixtureVersion(t, dir, arm.archive, "0.6.0")
			old := startCompatHost(t, legacy, "T3")
			if old.release() != "0.6.0" {
				t.Fatal("not the unchanged legacy package", old.release())
			}
			planningCalibrationRoundTrip(t, old)
		})
	}
	h := startCompatHost(t, b, "T3")
	h.grant(t, "kv", "interactions")
	st := h.app.store
	if err := st.SetActiveProject("planning-project"); err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession(t.Context(), "planning-root", "synthetic root work"); err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession(t.Context(), "planning-other", "unrelated work"); err != nil {
		t.Fatal(err)
	}
	child := func(id, parent string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"ws_id": id, "parent_work_session_id": parent, "depth": 1})
		// Completed retained queue rows prove ancestry without scheduling work.
		item := &store.WorkItem{Kind: store.SubagentWorkKind, State: "DONE", Payload: string(raw), DedupKey: id, Source: "identity"}
		if _, ok, err := st.EnqueueWorkWithSessionBelowLimit(item, 50, id, store.SubagentDescription(id)); err != nil || !ok {
			t.Fatalf("child %s: %v %v", id, ok, err)
		}
	}
	child("planning-child", "planning-root")
	child("planning-grandchild", "planning-child")
	child("planning-sibling", "planning-other")
	plan := "Verify the scoped result and retain its evidence"
	if err := st.UpdateWorkPlan(t.Context(), "planning-root", nil, nil, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	childPlan := "A child declaration must not replace its parent's plan"
	if err := st.UpdateWorkPlan(t.Context(), "planning-child", nil, nil, &childPlan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seed := func(session, turn, tool, content string, outcome interaction.Outcome) string {
		t.Helper()
		if err := st.RecordToolStart(turn, 1, session, "synthetic", turn, tool, "{}"); err != nil {
			t.Fatal(err)
		}
		ms := int64(7)
		if err := st.RecordToolDoneMeasured(turn, 1, tool, "{}", content, outcome, false, store.ToolMeasurement{DurationMS: &ms, WorkSession: session}); err != nil {
			t.Fatal(err)
		}
		return store.ToolCallRecordID(turn, 1)
	}
	rootID := seed("planning-root", "root-read", "fixture.read", "root result", interaction.Succeeded)
	childID := seed("planning-child", "child-check", "fixture.check", "child failed result", interaction.Failed)
	grandID := seed("planning-grandchild", "grand-refusal", "fixture.refused", "request refused before execution", interaction.Refused)
	otherID := seed("planning-other", "other-read", "fixture.other", "unrelated secret", interaction.Succeeded)
	seed("planning-sibling", "sibling-read", "fixture.sibling", "unrelated sibling", interaction.Succeeded)
	if err := st.SetActiveProject("unrelated-ui-project"); err != nil {
		t.Fatal(err)
	}
	t.Run("caller_cannot_forge_host_context", func(t *testing.T) {
		ctx := context.WithValue(t.Context(), identity.SubagentWorkSession{}, "planning-root")
		call := llm.ToolCall{ID: "forged-context", Type: "function", EmissionOrdinal: 1}
		call.Function.Name = h.tool("review")
		call.Function.Arguments = `{"_host_work_session":{"id":"planning-other","call_id":"forged"}}`
		obs := h.app.executeToolCall(ctx, call)
		if !obs.Failed || !obs.Refused || obs.ReasonCode != "OPERATION_ARGUMENT_INVALID" {
			t.Fatalf("undeclared host context was accepted: %+v", obs)
		}
	})
	var ordinal int
	invoke := func(t *testing.T, op string, args map[string]any, out any) string {
		t.Helper()
		ordinal++
		turn := "planning-review-" + strconv.Itoa(ordinal)
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.RecordToolStart(turn, 1, "planning-root", "synthetic", turn, h.tool(op), string(raw)); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		ctx = interaction.WithTurn(context.WithValue(ctx, identity.SubagentWorkSession{}, "planning-root"), turn)
		call := llm.ToolCall{ID: turn, Type: "function", EmissionOrdinal: 1}
		call.Function.Name, call.Function.Arguments = h.tool(op), string(raw)
		obs := h.app.executeToolCall(ctx, call)
		if obs.Failed || obs.Refused || obs.ReasonCode != "" {
			t.Fatalf("%s refused: %+v", op, obs)
		}
		if obs.WorkSession != "planning-root" {
			t.Fatalf("caller or UI replaced host scope: %+v", obs)
		}
		body, opened := strings.CutPrefix(obs.Text, untrusted.Open+" source: plugin "+b.pluginID+"\n")
		body, closed := strings.CutSuffix(body, "\n"+untrusted.Close)
		if !opened || !closed {
			t.Fatalf("missing exact model-facing boundary: %s", obs.Text)
		}
		if err := json.Unmarshal([]byte(body), out); err != nil {
			t.Fatalf("decode %s: %v: %s", op, err, obs.Text)
		}
		if err := st.RecordToolDoneMeasured(turn, 1, h.tool(op), string(raw), obs.Text, interaction.Succeeded, obs.Truncated, store.ToolMeasurement{WorkSession: obs.WorkSession, DurationMS: obs.DurationMS}); err != nil {
			t.Fatal(err)
		}
		return store.ToolCallRecordID(turn, 1)
	}
	var review planningReview
	var reviewID string
	t.Run("scoped_descendants_results_and_own_call", func(t *testing.T) {
		reviewID = invoke(t, "review", map[string]any{}, &review)
		if review.WorkSession != "planning-root" || review.Scope != "retained_session_and_proven_descendants" || review.CountBasis != "retained_recorded_requests" || !review.Coverage.Complete || review.OwnCallExclusion != "host_record_excluded" || review.ExcludedCallID != reviewID {
			t.Fatalf("scope/coverage: %+v", review)
		}
		if review.Counts == nil || review.Counts.RecordedRequests != 3 || review.Counts.Succeeded != 1 || review.Counts.Failed != 1 || review.Counts.Refused != 1 || review.Counts.Unknown != 0 {
			t.Fatalf("request/outcome counts: %+v", review.Counts)
		}
		want := map[string]string{rootID: "planning-root", childID: "planning-child", grandID: "planning-grandchild"}
		if len(review.Attempts) != len(want) {
			t.Fatalf("unexpected attempt rows: %+v", review.Attempts)
		}
		for _, a := range review.Attempts {
			if want[a.ID] != a.WorkSession || a.RequestRef == nil || a.RequestRef.ID != a.ID || a.ResultRef == nil || a.ResultRef.ID != a.ResultID || a.ResultExcerpt == "" || a.ResultRecord == "" {
				t.Fatalf("attempt lost source/result attribution: %+v", a)
			}
			delete(want, a.ID)
		}
		if len(want) != 0 {
			t.Fatalf("missing attempts: %v", want)
		}
		for _, ref := range review.Evidence {
			if ref.ID == reviewID || ref.ID == otherID || ref.Incarnation != review.SourceIncarnation {
				t.Fatalf("foreign or self reference: %+v", ref)
			}
			if availability, err := st.PluginMemoryEvidenceStatus(t.Context(), ref); err != nil || availability != "available" {
				t.Fatalf("guest evidence not host-revalidatable: %+v %s %v", ref, availability, err)
			}
		}
		if len(review.Evidence) < 6 {
			t.Fatalf("request and result evidence missing: %+v", review.Evidence)
		}
	})
	t.Run("current_material_is_read_only", func(t *testing.T) {
		kvCount := func() int {
			var n int
			if err := st.DB().QueryRow("SELECT COUNT(*) FROM plugin_kv WHERE plugin_id=?", b.pluginID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := kvCount()
		var material struct {
			Current struct {
				Session      string                 `json:"work_session_id"`
				Availability string                 `json:"availability"`
				Fields       interaction.WorkChange `json:"fields"`
				FieldRefs    map[string]planningRef `json:"field_refs"`
			} `json:"current_work"`
			Review planningReview `json:"review"`
		}
		id := invoke(t, "plan", map[string]any{}, &material)
		if material.Current.Session != "planning-root" || material.Current.Availability != "retained" || material.Current.Fields.Plan == nil || *material.Current.Fields.Plan != plan || material.Current.FieldRefs["plan"].ID == "" || material.Review.ExcludedCallID != id || material.Review.Counts == nil || material.Review.Counts.RecordedRequests != 4 || kvCount() != before {
			t.Fatalf("current material copied/changed work or lost previous review: %+v", material)
		}
	})
	t.Run("explicit_legacy_scope_stays_single_session", func(t *testing.T) {
		var got planningReview
		invoke(t, "review", map[string]any{"work_session_id": "planning-root"}, &got)
		if got.Scope != "retained_session_without_descendants" || !got.Coverage.Complete {
			t.Fatalf("explicit scope changed: %+v", got)
		}
		for _, a := range got.Attempts {
			if a.WorkSession != "planning-root" {
				t.Fatalf("unexpected child in legacy scope: %+v", a)
			}
		}
	})
	t.Run("unresolved_ancestry_is_disclosed", func(t *testing.T) {
		child("planning-legacy", "")
		seed("planning-legacy", "legacy-read", "fixture.legacy", "unproved child", interaction.Succeeded)
		var got planningReview
		invoke(t, "review", map[string]any{}, &got)
		if got.Coverage.Complete || !strings.Contains(strings.Join(got.Coverage.Reasons, ","), "ancestry_incomplete") {
			t.Fatalf("omitted descendants appeared complete: %+v", got.Coverage)
		}
		for _, a := range got.Attempts {
			if a.WorkSession == "planning-legacy" {
				t.Fatalf("guessed child: %+v", a)
			}
		}
	})
	t.Run("current_calibration_begin_observe_grade", func(t *testing.T) {
		planningCalibrationRoundTrip(t, h)
	})
}

// Drive the real registered handlers and asynchronous host calibration
// delivery. The three events below are explicit synthetic measurements;
// no model execution or authored task outcome is inferred from them.
func planningCalibrationRoundTrip(t *testing.T, h *compatHost) {
	t.Helper()
	h.grant(t, "kv")
	const session = "planning-calibration-session"
	ctx := pluginhost.WithActingSession(t.Context(), pluginhost.ActingSession{ID: session, CallID: "planning-calibration-call"})
	call := func(mode string, fields map[string]any) map[string]any {
		t.Helper()
		args := map[string]any{"mode": mode, "plan_id": "calibration-plan"}
		for k, v := range fields {
			args[k] = v
		}
		res, err := h.app.toolReg.ExecuteModel(ctx, h.tool("plan"), args)
		if err != nil || res.Error != "" || res.ReasonCode != "" {
			t.Fatalf("%s: %v %+v", mode, err, res)
		}
		body, opened := strings.CutPrefix(res.Output, untrusted.Open+" source: plugin "+h.pluginID()+"\n")
		body, closed := strings.CutSuffix(body, "\n"+untrusted.Close)
		if !opened || !closed {
			t.Fatalf("missing model-facing plugin boundary: %s", res.Output)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ap := h.active(t)
	h.app.pluginMu.RLock()
	subscriber := h.app.subscribers[h.pluginID()]
	h.app.pluginMu.RUnlock()
	if subscriber == nil || ap == nil {
		t.Fatal("actual package has no calibration subscriber")
	}
	snapshot := func() map[string]any {
		return subscriber.Snapshot(ap.RuntimeGeneration(), pluginhost.ActingSession{ID: session})
	}
	awaitEvents(t, "prior calibration deliveries acknowledged", func() bool {
		s := snapshot()
		return s["emitted"] == s["settled"]
	})
	before := snapshot()
	if before["available"] != true || before["loss_count"] != uint64(0) {
		t.Fatalf("calibration started incomplete: %v", before)
	}
	start := before["emitted"].(uint64)
	got := call("begin", map[string]any{"session_id": session, "family": "synthetic", "declared_steps": 1, "budget_calls": 10, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	if got["state"] != "open" || got["session_id"] != session || got["host_calibration"] != "provisional" {
		t.Fatalf("begin lost host context: %v", got)
	}
	got = call("material", nil)
	if got["plan_id"] != "calibration-plan" || got["state"] != "open" {
		t.Fatalf("material lost registered plan: %v", got)
	}
	call("apply", map[string]any{"apply_id": "apply-1", "step_id": "s1", "kind_class": "refinement", "from_class": "before", "to_class": "after", "count": 1})
	for _, acting := range []string{session, "unrelated-calibration-session", session} {
		h.app.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "fixture.calibration", "failed": false, "duration_ms": int64(1), "actor": "main", "session": acting})
	}
	awaitEvents(t, "three actual guest observe acknowledgments", func() bool { return snapshot()["settled"] == start+3 })
	if after := snapshot(); after["emitted"] != start+3 || after["loss_count"] != uint64(0) {
		t.Fatalf("observe was lost or refused: %v", after)
	}
	got = call("grade", map[string]any{"result_class": "passed"})
	if got["state"] != "closed" || got["host_calibration"] != "complete" || got["observed_calls"] != float64(2) {
		t.Fatalf("grade lost acknowledged session measurements: %v", got)
	}
	got = call("material", nil)
	if got["state"] != "closed" || got["host_calibration"] != "complete" || got["observed_calls"] != float64(2) {
		t.Fatalf("material lost completed calibration: %v", got)
	}
}

type planningRef struct {
	ID       string `json:"id"`
	Sequence string `json:"sequence"`
	SHA256   string `json:"sha256"`
}
type planningReview struct {
	WorkSession       string `json:"work_session_id"`
	SourceIncarnation string `json:"source_incarnation"`
	Scope             string `json:"scope"`
	CountBasis        string `json:"count_basis"`
	OwnCallExclusion  string `json:"own_call_exclusion"`
	ExcludedCallID    string `json:"excluded_call_id"`
	Coverage          struct {
		Complete bool     `json:"complete"`
		Reasons  []string `json:"reasons"`
	} `json:"coverage"`
	Counts *struct {
		RecordedRequests int `json:"recorded_requests"`
		Succeeded        int `json:"succeeded"`
		Failed           int `json:"failed"`
		Refused          int `json:"refused"`
		Unknown          int `json:"unknown"`
	} `json:"counts"`
	Attempts []struct {
		ID            string       `json:"id"`
		WorkSession   string       `json:"work_session_id"`
		ResultID      string       `json:"result_id"`
		RequestRef    *planningRef `json:"request_ref"`
		ResultRef     *planningRef `json:"result_ref"`
		ResultExcerpt string       `json:"result_excerpt"`
		ResultRecord  string       `json:"result_record"`
	} `json:"attempts"`
	Evidence []store.MemoryEvidenceRef `json:"evidence"`
}

// Keep the manifest and every payload byte; add only disposable test trust.
func planningPackageFixture(t *testing.T, dir string) compatBuild {
	return planningPackageFixtureVersion(t, dir, "planning-t0.aiiospkg", "0.7.0")
}

func planningPackageFixtureVersion(t *testing.T, dir, archive, version string) compatBuild {
	t.Helper()
	unsigned := filepath.Join(dir, archive)
	raw, err := os.ReadFile(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	t.Logf("planning unsigned package sha256=%s", hex.EncodeToString(digest[:]))
	t.Cleanup(func() {
		after, err := os.ReadFile(unsigned)
		if err != nil || !bytes.Equal(raw, after) {
			t.Error("input package changed", err)
		}
	})
	z, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	tr := tar.NewReader(z)
	spec := packagetest.PackageSpec{Root: "id.aeon.planning-" + version, InstallFiles: map[string][]byte{}}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size > 32<<20 || !strings.HasPrefix(h.Name, spec.Root+"/") {
			t.Fatal("unexpected package member", h.Name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(h.Name, spec.Root+"/")
		switch {
		case name == "manifest.json":
			spec.Manifest = data
		case strings.HasPrefix(name, "install-root/"):
			spec.InstallFiles[strings.TrimPrefix(name, "install-root/")] = data
		default:
			t.Fatal("unexpected unsigned package domain", name)
		}
	}
	if !bytes.Equal(packagetest.Build(spec), raw) {
		t.Fatal("test signing reconstruction changed unsigned package")
	}
	role, status, err := packagetest.NewPlatformRelease("aiii_test_planning_integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	b := compatBuild{pluginID: "id.aeon.planning", worker: filepath.Join(dir, "aii-plugin-worker"), t0: unsigned, v1: filepath.Join(tmp, "planning-"+version+".aiiospkg"), root: filepath.Join(tmp, "platform-root.pub.json"), status: filepath.Join(tmp, packagetest.StatusFilePlatform)}
	root, err := json.Marshal(role.Env)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{b.root: root, b.status: status, b.v1: packagetest.Build(spec)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{b.worker, b.t0, b.v1, b.root, b.status} {
		compatFixtureFile(t, path)
	}
	return b
}
