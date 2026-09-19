package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .

// .
func (a *App) activePlugin(id string) *pluginhost.ActivePlugin {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	for _, p := range a.plugins {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// .
// .
// .
// .
// .
func (a *App) applyPluginSetting(cfg *Config, key string, v interface{}) error {
	rest := strings.TrimPrefix(key, "plugins.settings.")
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return fmt.Errorf("%s: want plugins.settings.<plugin id>.<setting key>", key)
	}
	id, setting := rest[:dot], rest[dot+1:]
	ap := a.activePlugin(id)
	if ap == nil {
		return fmt.Errorf("%s: no active plugin %q", key, id)
	}
	var decl *pluginhost.SettingDecl
	for i := range ap.Settings {
		if ap.Settings[i].Key == setting {
			decl = &ap.Settings[i]
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if v == nil || v == "" {
		if cfg.Plugins.Settings == nil || cfg.Plugins.Settings[id] == nil {
			return nil
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		next := make(map[string]map[string]interface{}, len(cfg.Plugins.Settings))
		for pid, vals := range cfg.Plugins.Settings {
			if pid != id {
				next[pid] = vals
				continue
			}
			kept := make(map[string]interface{}, len(vals))
			for k, val := range vals {
				if k != setting {
					kept[k] = val
				}
			}
			if len(kept) > 0 {
				next[pid] = kept
			}
		}
		cfg.Plugins.Settings = next
		return nil
	}
	if decl == nil {
		return fmt.Errorf("%s: plugin %s declares no setting %q (declared: %s) — clear it by saving it empty", key, id, setting, strings.Join(pluginhost.SettingKeys(ap.Settings), ", "))
	}
	if err := pluginhost.CheckSettingValue(*decl, v); err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	if decl.Type == pluginhost.SettingSecret {
		handle, _ := v.(string)
		if _, ok := cfg.Plugins.AuthProfiles[handle]; !ok {
			return fmt.Errorf("%s: %q names no credential handle in plugins.auth_profiles — a secret setting is a handle the operator configured, never a value typed here", key, handle)
		}
	}
	// .
	// .
	next := make(map[string]map[string]interface{}, len(cfg.Plugins.Settings)+1)
	for pid, vals := range cfg.Plugins.Settings {
		copied := make(map[string]interface{}, len(vals)+1)
		for k, val := range vals {
			copied[k] = val
		}
		next[pid] = copied
	}
	if next[id] == nil {
		next[id] = map[string]interface{}{}
	}
	next[id][setting] = v
	cfg.Plugins.Settings = next
	return nil
}

// .
// .
func (a *App) pluginSettingsSource(id string) map[string]interface{} {
	c := a.configSnapshot()
	vals := c.Plugins.Settings[id]
	if len(vals) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(vals))
	for k, v := range vals {
		out[k] = v
	}
	return out
}

// .
// .
// .
// .
func (a *App) pluginViews(c *Config) []dashboard.PluginView {
	a.pluginMu.Lock()
	active := append([]*pluginhost.ActivePlugin(nil), a.plugins...)
	a.pluginMu.Unlock()
	// .
	// .
	life := map[string]pluginfacility.InstanceView{}
	for _, v := range a.pluginFacility().Snapshot().Instances {
		life[v.ID] = v
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	views := make([]dashboard.PluginView, 0, len(active))
	for _, ap := range active {
		view := dashboard.PluginView{ID: ap.ID, Version: ap.Version, Tier: ap.Tier.String(), Mode: ap.Mode, Variant: ap.VariantID, Tools: ap.Tools()}
		view.Publisher, view.PublisherID, view.Family, view.Runtime, view.PackageHash = ap.Publisher, ap.PublisherID, ap.Family, ap.Runtime, ap.PackageHash
		view.Title, view.Description = ap.Title, ap.Description
		view.Interfaces, view.Capabilities = append([]string(nil), ap.Interfaces...), append([]string(nil), ap.Capabilities...)
		if a := ap.Accelerator; a != nil {
			view.Accelerator = &dashboard.AcceleratorView{OS: a.OS, Arch: a.Arch, Backend: a.Backend, Operators: a.Operators, RuntimeLibraries: a.RuntimeLibraries, Precision: a.Precision, Models: a.Models, MemoryBytes: a.MemoryBytes, SessionLimit: a.SessionLimit, Fallback: a.Fallback}
		}
		if r := ap.Readiness; r != nil {
			view.Readiness = &dashboard.ReadinessView{ModelsLoaded: r.ModelsLoaded, Accelerator: r.Accelerator, ProbeMS: r.ProbeMS}
		}
		if s := ap.Startup; s != nil {
			ms := func(d time.Duration) int64 { return int64(d / time.Millisecond) }
			view.Startup = &dashboard.StartupView{EffectiveMS: ms(s.Effective), RequestedMS: ms(s.Requested),
				CeilingMS: ms(s.Ceiling), Source: s.Source, Capped: s.Capped}
		}
		if v, ok := life[ap.ID]; ok {
			view.Lifecycle = lifecycleView(v)
		}
		if len(ap.Models) > 0 && ap.ModelsDir != "" {
			for _, st := range pluginhost.ModelStatuses(ap.Models, ap.ModelsDir) {
				view.Models = append(view.Models, dashboard.ModelView{Name: st.Name, Size: st.Size, Present: st.Present, Partial: st.Partial})
			}
		}
		values := c.Plugins.Settings[ap.ID]
		var handles []string
		if g, ok := c.Plugins.Grants[ap.ID]; ok {
			handles = append(handles, g.CredentialHandles...)
			view.Grants = pluginGrantView(g)
		}
		// .
		// .
		// .
		// .
		view.Applies = "next_call"
		if ap.Family == "voice_interface" {
			view.Applies = "next_session"
			view.SessionSettings = a.appliedVoiceSettings()
		}
		view.Acts = a.pendingActViews(ap.ID)
		effective := pluginhost.EffectiveSettings(ap.Settings, values)
		for _, d := range ap.Settings {
			sv := dashboard.PluginSettingView{Key: d.Key, Type: d.Type, Title: d.Title, Description: d.Description, Default: d.Default, Values: d.Values, Labels: d.Labels, Required: d.Required, Minimum: d.Minimum, Maximum: d.Maximum, Scope: d.Scope}
			if d.OAuth != nil {
				sv.OAuth = &dashboard.SettingOAuthHintView{Provider: d.OAuth.Provider, Services: append([]string(nil), d.OAuth.Services...)}
			}
			if v, ok := values[d.Key]; ok {
				sv.Value = v
			}
			if e, ok := effective[d.Key]; ok {
				sv.Effective = e
			}
			sv.Invalid = pluginhost.StoredInvalid(d, values)
			if d.Type == pluginhost.SettingSecret {
				sv.Handles = append([]string(nil), handles...)
			}
			view.Settings = append(view.Settings, sv)
		}
		// .
		// .
		// .
		// .
		// .
		// .
		var orphans []string
		for k := range values {
			if !pluginhost.DeclaresSetting(ap.Settings, k) {
				orphans = append(orphans, k)
			}
		}
		sort.Strings(orphans)
		for _, k := range orphans {
			view.Settings = append(view.Settings, dashboard.PluginSettingView{
				Key: k, Value: values[k], Undeclared: true,
				Title:       k,
				Description: "saved for an earlier release; this one does not offer it and nothing reads it",
			})
		}
		views = append(views, view)
	}
	return views
}
