package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WHO IS HEARD IS HELD TO ONE RULE AT BOTH DOORS. The settings door
// refused a speaker policy outside its values; the loader did not look.
// A file that said "mode": "Only" with a list of ids loaded without a
// word, Settings showed a restrictive-looking policy, and every voice in
// the room was delivered to the identity and recorded.
func TestASpeakerPolicyTheDoorRefusesIsRefusedAtLoad(t *testing.T) {
	load := func(policy map[string]any) (*Config, error) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"speech": map[string]any{"speakers": policy}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return ReadConfig(path)
	}
	for _, policy := range []map[string]any{
		{},
		{"mode": "all"},
		{"mode": "only", "uids": []string{"quinn"}},
		{"mode": "ignore", "uids": []string{"quinn"}, "unidentified": "withhold"},
	} {
		if _, err := load(policy); err != nil {
			t.Errorf("%v is a policy the door takes, yet the file was refused: %v", policy, err)
		}
	}
	for _, policy := range []map[string]any{
		{"mode": "Only", "uids": []string{"quinn"}},
		{"mode": "whitelist", "uids": []string{"quinn"}},
		{"mode": "ignore", "uids": []string{"quinn"}, "unidentified": "Withhold"},
		{"mode": "all", "uids": []string{"quinn"}},
	} {
		_, err := load(policy)
		if err == nil {
			t.Errorf("%v LOADED: the door refuses it, and a mode outside the set delivers everyone", policy)
			continue
		}
		if !strings.HasPrefix(err.Error(), "speech.speakers: ") {
			t.Errorf("%v was refused without naming its key: %v", policy, err)
		}
	}
}

// A mode the host does not know is no permission to hear anyone, however
// the policy came to hold it.
func TestAnUnknownSpeakerModeWithholds(t *testing.T) {
	for _, mode := range []string{"Only", "whitelist"} {
		p := policyFrom(SpeakerPolicyConfig{Mode: mode, UIDs: []string{"quinn"}})
		for _, ids := range [][]string{nil, {"quinn"}, {"someone-else"}} {
			if deliver, why := p.decideIdentity(ids); deliver || why == "" {
				t.Errorf("mode %q delivered %v (reason %q): an unknown mode must withhold and say why", mode, ids, why)
			}
		}
	}
	if deliver, _ := policyFrom(SpeakerPolicyConfig{}).decideIdentity(nil); !deliver {
		t.Error("the default policy, all, withheld an unidentified voice")
	}
}
