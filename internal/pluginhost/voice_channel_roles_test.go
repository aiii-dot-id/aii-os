package pluginhost

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/audio"
	"testing"
)

func TestExplicitAudioRolesRequireExactConfirmation(t *testing.T) {
	args := map[string]any{"audio": map[string]any{"input": map[string]any{"channels": 2, "channel_roles": []string{"capture", "playback_reference"}}}}
	if err := validateChannelRoles(args); err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{
		`{"audio":{"input":{"channel_roles":["capture","playback_reference"]}}}`,
		`{"audio":{"input":{}}}`,
		`{"audio":{"input":{"channel_roles":["playback_reference","capture"]}}}`,
	} {
		err := confirmChannelRoles(args, json.RawMessage(response), audio.Format{Rate: 16000, Channels: 2})
		if (err == nil) != (response == `{"audio":{"input":{"channel_roles":["capture","playback_reference"]}}}`) {
			t.Fatalf("confirmation: %s: %v", response, err)
		}
	}
	if err := confirmChannelRoles(args, json.RawMessage(`{"audio":{"input":{"channel_roles":["capture","playback_reference"]}}}`), audio.Format{Rate: 16000, Channels: 1}); err == nil {
		t.Fatal("downmix of reference admitted")
	}
}
