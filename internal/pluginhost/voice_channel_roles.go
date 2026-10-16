package pluginhost

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

func requestedChannelRoles(args map[string]any) ([]string, int, error) {
	var a struct {
		Input *struct {
			Channels     int
			ChannelRoles []string `json:"channel_roles"`
		}
	}
	raw, err := json.Marshal(args["audio"])
	if err != nil {
		return nil, 0, err
	}
	if err = json.Unmarshal(raw, &a); err != nil {
		return nil, 0, err
	}
	if a.Input == nil {
		return nil, 0, nil
	}
	return a.Input.ChannelRoles, a.Input.Channels, nil
}
func validateChannelRoles(args map[string]any) error {
	roles, n, err := requestedChannelRoles(args)
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}
	if len(roles) != n {
		return fmt.Errorf("audio channel roles differ from channel count")
	}
	for i, role := range roles {
		if len(role) == 0 || len(role) > 64 || slices.Contains(roles[:i], role) {
			return fmt.Errorf("invalid or repeated audio channel role")
		}
	}
	return nil
}
func confirmChannelRoles(args map[string]any, result json.RawMessage, in audio.Format) error {
	roles, _, err := requestedChannelRoles(args)
	if err != nil {
		return err
	}
	var r struct {
		Audio struct {
			Input *struct {
				ChannelRoles []string `json:"channel_roles"`
			}
		}
	}
	if err = json.Unmarshal(result, &r); err != nil {
		return err
	}
	var confirmed []string
	if r.Audio.Input != nil {
		confirmed = r.Audio.Input.ChannelRoles
	}
	if !slices.Equal(roles, confirmed) || (len(roles) != 0 && in.Channels != len(roles)) {
		return fmt.Errorf("engine did not confirm the input channel roles; audio not started")
	}
	return nil
}
