package app

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
var grantFields = []string{"kv", "memory", "voice", "embeddings", "tools", "read_only"}

// .
// .
func setGrantField(g *broker.Grant, field string, on bool) bool {
	switch field {
	case "kv":
		g.KV = on
	case "memory":
		g.Memory = on
	case "voice":
		g.Voice = on
	case "embeddings":
		g.Embeddings = on
	case "tools":
		g.Tools = on
	case "read_only":
		g.ReadOnly = on
	default:
		return false
	}
	return true
}

// .
func grantEmpty(g broker.Grant) bool {
	return !g.KV && !g.Memory && !g.Voice && !g.Embeddings && !g.Tools && !g.ReadOnly && !g.PlaintextCredentials &&
		len(g.Hosts) == 0 && len(g.Local) == 0 && len(g.CredentialHandles) == 0 && len(g.Roots) == 0 && len(g.AutoConfirm) == 0
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func applyPluginGrant(cfg *Config, key string, v interface{}) error {
	rest := strings.TrimPrefix(key, "plugins.grants.")
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return fmt.Errorf("%s: want plugins.grants.<plugin id>.<%s>", key, strings.Join(grantFields, "|"))
	}
	id, field := rest[:dot], rest[dot+1:]
	next := make(map[string]broker.Grant, len(cfg.Plugins.Grants)+1)
	for pid, g := range cfg.Plugins.Grants {
		next[pid] = g
	}
	g := next[id]
	if field == "auto_confirm" {
		// .
		// .
		list, err := operationList(v)
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		g.AutoConfirm = list
	} else {
		on, ok := v.(bool)
		if !ok {
			return fmt.Errorf("%s: want a boolean", key)
		}
		if !setGrantField(&g, field, on) {
			return fmt.Errorf("%s: %q is not a grant the page sets (%s); hosts, local, roots and credential_handles are lists, edited in the config file", key, field, strings.Join(grantFields, ", "))
		}
	}
	if grantEmpty(g) {
		delete(next, id)
	} else {
		next[id] = g
	}
	if len(next) == 0 {
		next = nil
	}
	cfg.Plugins.Grants = next
	return nil
}

// .
// .
// .
func operationList(v interface{}) ([]string, error) {
	var raw []string
	switch list := v.(type) {
	case []string:
		raw = list
	case []interface{}:
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("want a list of operation names")
			}
			raw = append(raw, s)
		}
	case nil:
	default:
		return nil, fmt.Errorf("want a list of operation names")
	}
	var out []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" || hasOperation(out, s) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// .
// .
func (a *App) publishGrants(cfg Config) {
	a.replacePolicy(cfg)
	logsink.Info("config.decision", "plugin grants applied live -> %d grant(s)", len(cfg.Plugins.Grants))
}

// .
func pluginGrantView(g broker.Grant) dashboard.PluginGrantsView {
	v := dashboard.PluginGrantsView{Listed: true, KV: g.KV, Memory: g.Memory, Voice: g.Voice, Embeddings: g.Embeddings, Tools: g.Tools,
		Hosts: append([]string(nil), g.Hosts...), Handles: append([]string(nil), g.CredentialHandles...),
		Local: append([]string(nil), g.Local...), PlaintextCredentials: g.PlaintextCredentials,
		AutoConfirm: append([]string(nil), g.AutoConfirm...), ReadOnly: g.ReadOnly}
	for _, r := range g.Roots {
		name := r.Name
		if r.Write {
			name += " (writable)"
		}
		v.Roots = append(v.Roots, name)
	}
	return v
}
