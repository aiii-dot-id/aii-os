package app

import (
	"context"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const choicesTimeout = 20 * time.Second

func (a *App) PluginSettingChoices(id, key string) ([]dashboard.SettingChoice, error) {
	ap := a.activePlugin(id)
	if ap == nil {
		return nil, fmt.Errorf("no active plugin %q", id)
	}
	var decl *pluginhost.SettingDecl
	for i := range ap.Settings {
		if ap.Settings[i].Key == key {
			decl = &ap.Settings[i]
		}
	}
	if decl == nil || decl.ChoicesFrom == "" {
		return nil, fmt.Errorf("plugin %s's setting %q looks up no choices", id, key)
	}
	name := pluginhost.ToolNameFor(id, decl.ChoicesFrom)
	tool, ok := a.toolReg.Get(name)
	if !ok {
		return nil, fmt.Errorf("plugin %s offers no operation %q", id, decl.ChoicesFrom)
	}
	if rs, ok := tool.(interface{ ReplaySafe() bool }); !ok || !rs.ReplaySafe() {
		return nil, fmt.Errorf("plugin %s's %q is not a read, so the page does not call it", id, decl.ChoicesFrom)
	}
	ctx, cancel := context.WithTimeout(context.Background(), choicesTimeout)
	defer cancel()
	res, err := a.toolReg.Execute(ctx, name, map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("the lookup failed: %w", err)
	}
	if res.Error != "" {
		return nil, fmt.Errorf("the plugin could not look them up: %s", res.Error)
	}
	choices, err := pluginhost.ParseSettingChoices([]byte(res.Output))
	if err != nil {
		return nil, fmt.Errorf("the plugin's answer is not choices the card can offer: %w", err)
	}
	out := make([]dashboard.SettingChoice, 0, len(choices))
	for _, c := range choices {
		out = append(out, dashboard.SettingChoice{Value: c.Value, Label: c.Label})
	}
	return out, nil
}
