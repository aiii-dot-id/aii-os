package dashboard

import (
	"encoding/json"
	"net/http"
)

type SettingInForce struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Usual  string `json:"usual,omitempty"`
	Stated bool   `json:"stated,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

type SettingsInForce struct {
	Settings   []SettingInForce `json:"settings"`
	FileUnread string           `json:"file_unread,omitempty"`
}

func (s *Server) serveSettingsInForce(w http.ResponseWriter, r *http.Request) {
	h, done, ok := s.operatorRead(w, r)
	if !ok {
		return
	}
	defer done()
	if h == nil || h.SettingsInForce == nil {
		http.Error(w, "the settings in force are not readable now", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.SettingsInForce())
}
