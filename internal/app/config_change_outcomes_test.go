package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// THE FIVE WAYS A CHANGE THROUGH THE DOOR CAN END, each told apart by
// what the caller gets back, whether the save step ran, and what the
// live configuration says afterwards:
//
//	refused before the save       error, save never called, live as it was
//	the configuration moved       errConfigMoved, save never called, live is the newer writer's
//	the save replaced nothing     the save's error wrapped, live as it was
//	the save replaced and erred   the save's error wrapped, live is the change
//	the save replaced nothing     no error at all, the LIVE state returned, live as it was
//	  and said nothing
//
// Pinned ahead of breaking applyConfigChangeWith up: a change that folds
// two of these into one return fails here.

var errOutcomeSave = errors.New("the save step's own error")

// outcomePersist is a save step that answers (published, err) and counts
// how often the door reached it.
func outcomePersist(published bool, err error) (func(*Config) (bool, error), *int) {
	calls := 0
	return func(*Config) (bool, error) { calls++; return published, err }, &calls
}

func outcomeMeetingOff() map[string]any {
	return map[string]any{"listen": "meeting", "speak": "off"}
}

func outcomeIgnoreAda() map[string]any {
	return map[string]any{"mode": "ignore", "uids": []any{"ada.2"}, "unidentified": "withhold"}
}

// outcomeDuringMarshal is a change value that runs during when the door
// marshals it. The speaker policy's reader marshals its value inside the
// key loop, after the door has read the configuration it checks against
// and before it takes the lock to commit: so during is a second writer
// placed exactly in that gap, on the door's own goroutine, with no sleep
// and no production seam.
type outcomeDuringMarshal struct {
	during func()
	value  any
}

func (d outcomeDuringMarshal) MarshalJSON() ([]byte, error) {
	d.during()
	return json.Marshal(d.value)
}

func TestAChangeRefusedBeforeTheSaveNeverReachesIt(t *testing.T) {
	a := newVoiceApp(t)
	persist, calls := outcomePersist(true, nil)
	st, err := a.applyConfigChangeWith(map[string]any{"speech.mode": map[string]any{"listen": "meating"}}, persist)
	if err == nil {
		t.Fatal("a mode that is not one was accepted")
	}
	if st != nil {
		t.Errorf("a refusal returned a state: %+v", st)
	}
	if errors.Is(err, errConfigMoved) || errors.Is(err, errOutcomeSave) {
		t.Errorf("a refusal of the value reads as another outcome: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "speech.mode: ") {
		t.Errorf("the refusal does not open with the key it refuses: %q", err)
	}
	if *calls != 0 {
		t.Errorf("the save step ran %d time(s) for a refused change", *calls)
	}
	if got := a.configSnapshot().Speech.Mode; got != (VoiceModeConfig{}) {
		t.Errorf("a refusal changed the live mode: %+v", got)
	}
}

func TestAChangeCheckedAgainstAConfigurationThatMovedSavesNothing(t *testing.T) {
	a := newVoiceApp(t)
	newer, newerCalls := outcomePersist(true, nil)
	older, olderCalls := outcomePersist(true, nil)
	ran := 0
	policy := outcomeDuringMarshal{
		during: func() {
			ran++
			if _, err := a.applyConfigChangeWith(map[string]any{"speech.mode": outcomeMeetingOff()}, newer); err != nil {
				t.Errorf("the newer writer was refused: %v", err)
			}
		},
		value: outcomeIgnoreAda(),
	}
	st, err := a.applyConfigChangeWith(map[string]any{"speech.speakers": policy}, older)
	if ran != 1 || *newerCalls != 1 {
		t.Fatalf("the schedule did not happen: the newer writer ran %d time(s) and saved %d time(s)", ran, *newerCalls)
	}
	if !errors.Is(err, errConfigMoved) {
		t.Fatalf("the older change was not refused as moved: %v", err)
	}
	if st != nil {
		t.Errorf("a moved configuration returned a state: %+v", st)
	}
	if *olderCalls != 0 {
		t.Errorf("the older change reached the save step %d time(s)", *olderCalls)
	}
	// Live is the NEWER writer's configuration, said as such: not "what it
	// was at the start", which the newer writer changed.
	live := a.configSnapshot().Speech
	if live.Mode.Listen != "meeting" || live.Mode.Speak != "off" || int(live.Mode.Revision) != 1 {
		t.Errorf("live is not the newer writer's mode: %+v", live.Mode)
	}
	if int(live.Speakers.Revision) != 0 || live.Speakers.Mode != "" || len(live.Speakers.UIDs) != 0 {
		t.Errorf("the refused older policy reached the live configuration: %+v", live.Speakers)
	}
}

func TestASaveThatReplacedNothingAndFailedChangesNothing(t *testing.T) {
	a := newVoiceApp(t)
	persist, calls := outcomePersist(false, errOutcomeSave)
	st, err := a.applyConfigChangeWith(map[string]any{"speech.mode": outcomeMeetingOff()}, persist)
	if !errors.Is(err, errOutcomeSave) {
		t.Fatalf("the save's error did not come back as itself: %v", err)
	}
	if errors.Is(err, errConfigMoved) {
		t.Errorf("a failed save reads as a moved configuration: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "persist config: ") {
		t.Errorf("the error does not say the save failed: %q", err)
	}
	if strings.Contains(err.Error(), "published") {
		t.Errorf("a save that replaced nothing is reported as published: %q", err)
	}
	if st != nil {
		t.Errorf("a failed save returned a state: %+v", st)
	}
	if *calls != 1 {
		t.Errorf("the save step ran %d time(s), want 1", *calls)
	}
	if got := a.configSnapshot().Speech.Mode; got != (VoiceModeConfig{}) {
		t.Errorf("a save that replaced nothing changed the live mode: %+v", got)
	}
	if _, _, rev := a.VoiceMode(); int(rev) != 0 {
		t.Errorf("the voice mode was published at revision %d by a save that replaced nothing", rev)
	}
}

func TestASaveThatReplacedTheFileAndFailedIsLiveAndSaysSo(t *testing.T) {
	a := newVoiceApp(t)
	persist, calls := outcomePersist(true, errOutcomeSave)
	st, err := a.applyConfigChangeWith(map[string]any{"speech.mode": outcomeMeetingOff()}, persist)
	if !errors.Is(err, errOutcomeSave) {
		t.Fatalf("the save's error did not come back as itself: %v", err)
	}
	if errors.Is(err, errConfigMoved) {
		t.Errorf("a published save reads as a moved configuration: %v", err)
	}
	if !strings.Contains(err.Error(), "published and applied live") {
		t.Errorf("the error does not say the change is live: %q", err)
	}
	if strings.HasPrefix(err.Error(), "persist config: ") {
		t.Errorf("a published save is reported as one that replaced nothing: %q", err)
	}
	if st != nil {
		t.Errorf("an erring save returned a state beside its error: %+v", st)
	}
	if *calls != 1 {
		t.Errorf("the save step ran %d time(s), want 1", *calls)
	}
	// The change IS live, and what follows a published save ran: the mode
	// the status frames carry moved with it.
	if got := a.configSnapshot().Speech.Mode; got.Listen != "meeting" || got.Speak != "off" || int(got.Revision) != 1 {
		t.Errorf("the published change is not live: %+v", got)
	}
	if l, s, rev := a.VoiceMode(); l != "meeting" || s != "off" || int(rev) != 1 {
		t.Errorf("what follows a published save did not run: VoiceMode() = (%s, %s, %d)", l, s, rev)
	}
}

func TestASaveThatReplacedNothingAndSaidNothingReturnsNoError(t *testing.T) {
	a := newVoiceApp(t)
	persist, calls := outcomePersist(false, nil)
	st, err := a.applyConfigChangeWith(map[string]any{"speech.mode": outcomeMeetingOff()}, persist)
	if err != nil {
		t.Fatalf("a silent save that replaced nothing returned an error: %v", err)
	}
	if st == nil {
		t.Fatal("no state came back beside a nil error")
	}
	if *calls != 1 {
		t.Errorf("the save step ran %d time(s), want 1", *calls)
	}
	if got := a.configSnapshot().Speech.Mode; got != (VoiceModeConfig{}) {
		t.Errorf("a save that replaced nothing changed the live mode: %+v", got)
	}
	if _, _, rev := a.VoiceMode(); int(rev) != 0 {
		t.Errorf("the voice mode was published at revision %d by a save that replaced nothing", rev)
	}
	// The state that came back is the LIVE one. The page reads it as the
	// answer to its change: a state carrying the mode that was asked for,
	// beside a nil error, tells it the change took when nothing was saved.
	if st.Speech.Mode == nil {
		t.Fatal("the returned state carries no voice mode")
	}
	liveState := a.configState().Speech.Mode
	if liveState == nil {
		t.Fatal("rig: the live state carries no voice mode")
	}
	if got := *st.Speech.Mode; got.Listen == "meeting" || got != *liveState {
		t.Errorf("a save that replaced nothing told the page its change took: the returned mode is %+v, the live one is %+v", got, *liveState)
	}
}

// A VALID SUBMISSION EQUAL TO WHAT IS STORED STILL ADVANCES ITS REVISION.
// The existing bodies only ever submit a value that differs, so a door
// that advanced the revision "when the value changed" would pass them.
func TestAnEqualSubmissionStillAdvancesItsRevision(t *testing.T) {
	a := newVoiceApp(t)
	persist := func(*Config) (bool, error) { return true, nil }
	for want := 1; want <= 3; want++ {
		if _, err := a.applyConfigChangeWith(map[string]any{"speech.mode": outcomeMeetingOff()}, persist); err != nil {
			t.Fatal(err)
		}
		got := a.configSnapshot().Speech.Mode
		if got.Listen != "meeting" || got.Speak != "off" {
			t.Fatalf("submission %d changed the mode itself: %+v", want, got)
		}
		if int(got.Revision) != want {
			t.Fatalf("after %d equal submission(s) the mode's revision is %d", want, got.Revision)
		}
	}
	for want := 1; want <= 3; want++ {
		if _, err := a.applyConfigChangeWith(map[string]any{"speech.speakers": outcomeIgnoreAda()}, persist); err != nil {
			t.Fatal(err)
		}
		got := a.configSnapshot().Speech.Speakers
		if got.Mode != "ignore" || len(got.UIDs) != 1 || got.UIDs[0] != "ada.2" {
			t.Fatalf("submission %d changed the policy itself: %+v", want, got)
		}
		if int(got.Revision) != want {
			t.Fatalf("after %d equal submission(s) the policy's revision is %d", want, got.Revision)
		}
	}
	if got := a.configSnapshot().Speech.Mode; int(got.Revision) != 3 {
		t.Errorf("the mode's revision moved with the policy: %+v", got)
	}
}

// A CHANGE THAT WAS NOT PUBLISHED LEAVES THE PUBLISHED REVISIONS ALONE,
// and uses no number up: the next published change is the next number.
func TestAnUnpublishedSubmissionLeavesThePublishedRevision(t *testing.T) {
	a := newVoiceApp(t)
	both := func() map[string]any {
		return map[string]any{"speech.mode": outcomeMeetingOff(), "speech.speakers": outcomeIgnoreAda()}
	}
	check := func(when string, want int) {
		t.Helper()
		s := a.configSnapshot().Speech
		if int(s.Mode.Revision) != want || int(s.Speakers.Revision) != want {
			t.Fatalf("%s: revisions are mode %d, policy %d, want %d and %d", when, s.Mode.Revision, s.Speakers.Revision, want, want)
		}
		if _, _, rev := a.VoiceMode(); int(rev) != want {
			t.Fatalf("%s: the published voice mode is at revision %d, want %d", when, rev, want)
		}
	}
	publish := func(*Config) (bool, error) { return true, nil }
	if _, err := a.applyConfigChangeWith(both(), publish); err != nil {
		t.Fatal(err)
	}
	check("after the first published change", 1)

	silent, _ := outcomePersist(false, nil)
	if _, err := a.applyConfigChangeWith(both(), silent); err != nil {
		t.Fatal(err)
	}
	check("after a save that replaced nothing", 1)

	failing, _ := outcomePersist(false, errOutcomeSave)
	if _, err := a.applyConfigChangeWith(both(), failing); !errors.Is(err, errOutcomeSave) {
		t.Fatalf("the failed save did not come back as itself: %v", err)
	}
	check("after a save that failed", 1)

	// The mode lands on the candidate first (keys apply sorted), then the
	// policy is refused: the candidate's advanced revision must not leak.
	refused := both()
	refused["speech.speakers"] = map[string]any{"mode": "only", "loud": true}
	reached, calls := outcomePersist(true, nil)
	if _, err := a.applyConfigChangeWith(refused, reached); err == nil {
		t.Fatal("a policy with an unknown field was accepted")
	}
	if *calls != 0 {
		t.Fatalf("the save step ran %d time(s) for a refused change", *calls)
	}
	check("after a change refused at its second key", 1)

	if _, err := a.applyConfigChangeWith(both(), publish); err != nil {
		t.Fatal(err)
	}
	check("after the next published change", 2)
}
