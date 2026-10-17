package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A looked-up setting's choices are answered off the read loop and always
// wear the setting's name: the choices, or the reason there are none.
func TestSettingChoicesAreAnsweredWearingTheirSetting(t *testing.T) {
	h := &WSHandler{
		SettingChoices: func(plugin, key string) ([]SettingChoice, error) {
			if key == "broken" {
				return nil, errors.New("the service did not answer")
			}
			return []SettingChoice{{Value: "jev-latest", Label: "The latest Jev"}}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	ask := func(id, key string) ServerMessage {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"type": "plugin_setting_choices", "request_id": id, "plugin_key": map[string]string{"plugin": "id.aiii.codequality", "key": key}})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var m ServerMessage
			if json.Unmarshal(raw, &m) == nil && m.RequestID == id {
				return m
			}
		}
	}
	m := ask("c1", "model")
	if m.Type != "setting_choices" || m.SettingChoices == nil || m.SettingChoices.Key != "model" || m.SettingChoices.Plugin != "id.aiii.codequality" ||
		len(m.SettingChoices.Choices) != 1 || m.SettingChoices.Choices[0].Label != "The latest Jev" || m.SettingChoices.Error != "" {
		t.Fatalf("the choices come back wearing their setting: %+v", m.SettingChoices)
	}
	m = ask("c2", "broken")
	if m.Type != "setting_choices" || m.SettingChoices.Key != "broken" || m.SettingChoices.Error != "the service did not answer" || len(m.SettingChoices.Choices) != 0 {
		t.Fatalf("a failed lookup is an answer with its reason: %+v", m.SettingChoices)
	}
}
