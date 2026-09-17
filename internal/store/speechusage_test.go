package store

import "testing"

// .
// .
// .
// .
func TestSpeechUseAccumulatesPerServiceDirectionAndMonth(t *testing.T) {
	s := testStore(t)
	add := func(provider, dir, period string, reqs, chars int, ms int64) {
		t.Helper()
		if err := s.AddSpeechUse(SpeechUse{Provider: provider, Direction: dir, Period: period, Requests: reqs, Characters: chars, Ms: ms}); err != nil {
			t.Fatal(err)
		}
	}
	add("ElevenLabs", "tts", "2026-09", 1, 120, 0)
	add("ElevenLabs", "tts", "2026-09", 1, 80, 0)
	add("Cartesia", "tts", "2026-09", 1, 40, 0)
	add("Deepgram", "stt", "2026-09", 3, 0, 9_000)
	add("ElevenLabs", "tts", "2026-10", 1, 5, 0)

	used, err := s.SpeechUsed("2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 3 {
		t.Fatalf("one row per service and direction: %+v", used)
	}
	by := map[string]SpeechUse{}
	for _, u := range used {
		by[u.Provider+"/"+u.Direction] = u
	}
	if got := by["ElevenLabs/tts"]; got.Requests != 2 || got.Characters != 200 {
		t.Errorf("two replies did not add up: %+v", got)
	}
	if got := by["Deepgram/stt"]; got.Requests != 3 || got.Ms != 9_000 {
		t.Errorf("what was heard did not add up: %+v", got)
	}
	if got := by["Cartesia/tts"]; got.Characters != 40 {
		t.Errorf("one service's spend landed on another: %+v", got)
	}
	next, err := s.SpeechUsed("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Characters != 5 {
		t.Fatalf("a new month did not start at nothing: %+v", next)
	}
}
