package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func keyProfileName(id, key string) string {
	name := strings.ToLower(id + "." + key)
	if profileNameRe.MatchString(name) {
		return name
	}
	sum := sha256.Sum256([]byte(id + "\x00" + key))
	return "plugin-" + hex.EncodeToString(sum[:8])
}

func outboundHosts(envelope []string) []string {
	var found []string
	for _, c := range envelope {
		if rest, ok := strings.CutPrefix(c, "net.outbound:"); ok {
			found = append(found, rest)
		}
	}
	return found
}

func signedOutboundHost(envelope []string) (string, int, error) {
	found := outboundHosts(envelope)
	if len(found) != 1 {
		return "", 0, fmt.Errorf("its package declares %d outbound hosts, not one, so the key's destination is not the package's own word — configure a profile in the config file", len(found))
	}
	return signedHostPort(found[0])
}

func signedHostPort(declared string) (string, int, error) {
	host, portText, err := splitHostPort(declared)
	if err != nil {
		return "", 0, fmt.Errorf("its declared outbound host %q is not host:port", declared)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("its declared outbound host %q has no single port", declared)
	}
	return host, port, nil
}

func (a *App) secretDecl(id, key string) (*pluginhost.ActivePlugin, error) {
	ap := a.activePlugin(id)
	if ap == nil {
		return nil, fmt.Errorf("no active plugin %q", id)
	}
	d := ap.Setting(key)
	if d == nil {
		return nil, fmt.Errorf("plugin %s declares no setting %q", id, key)
	}
	if d.Type != pluginhost.SettingSecret {
		return nil, fmt.Errorf("plugin %s's setting %q is a %s, not a secret", id, key, d.Type)
	}
	return ap, nil
}

func (a *App) keyView(c Config, ap *pluginhost.ActivePlugin, key string) (kept bool, host, why string) {
	h, port, err := signedOutboundHost(ap.Capabilities)
	if err != nil {
		return false, "", err.Error()
	}
	host = h + ":" + strconv.Itoa(port)
	dir, err := a.credentialsDir()
	if err != nil {
		return false, host, err.Error()
	}
	name := keyProfileName(ap.ID, key)
	p, ok := c.Plugins.AuthProfiles[name]
	return ok && samePath(p.SecretFile, filepath.Join(dir, name+".key")), host, ""
}

func (a *App) SetPluginSecret(id, key, secret string) error {
	secret = strings.TrimSpace(secret)
	switch {
	case secret == "":
		return errors.New("paste the key; an empty key is not saved (Remove forgets one)")
	case len(secret) > broker.MaxCredentialBytes:
		return fmt.Errorf("a key of %d bytes is longer than a request header can carry (%d)", len(secret), broker.MaxCredentialBytes)
	case strings.ContainsAny(secret, "\r\n\x00"):
		return errors.New("the key spans more than one line; paste the key alone")
	}
	ap, err := a.secretDecl(id, key)
	if err != nil {
		return err
	}
	host, port, err := signedOutboundHost(ap.Capabilities)
	if err != nil {
		return fmt.Errorf("plugin %s cannot take a key here: %w", id, err)
	}
	dir, err := a.credentialsDir()
	if err != nil {
		return err
	}
	name := keyProfileName(id, key)
	path := filepath.Join(dir, name+".key")
	orig := a.configSnapshot()
	if existing, ok := orig.Plugins.AuthProfiles[name]; ok && !samePath(existing.SecretFile, path) {
		return fmt.Errorf("a profile named %q already exists and is not the one this card keeps — it is edited in the config file", name)
	}
	_, statErr := os.Stat(path)
	fresh := errors.Is(statErr, os.ErrNotExist)
	if err := oauth.WritePrivateFile(path, []byte(secret+"\n")); err != nil {
		return fmt.Errorf("store the key: %w", err)
	}
	hostPort := host + ":" + strconv.Itoa(port)
	err = a.commitConfig(orig, func(c *Config) {
		profiles := make(map[string]broker.AuthProfile, len(c.Plugins.AuthProfiles)+1)
		for k, v := range c.Plugins.AuthProfiles {
			profiles[k] = v
		}
		profiles[name] = broker.AuthProfile{Scheme: "bearer", SecretFile: path, Host: host, Port: port}
		c.Plugins.AuthProfiles = profiles
		grants := copyGrants(c.Plugins.Grants)
		g := grants[id]
		g.CredentialHandles = addOnce(g.CredentialHandles, name)
		g.Hosts = addOnce(g.Hosts, hostPort)
		grants[id] = g
		c.Plugins.Grants = grants
		c.Plugins.Settings = withSetting(c.Plugins.Settings, id, key, name)
	}, nil, nil)
	if err != nil {
		if fresh {
			os.Remove(path)
		}
		return err
	}
	logsink.Info("config.decision", "plugin %s: key for %s kept as profile %s, riding only to %s", id, key, name, hostPort)
	return nil
}

func (a *App) ClearPluginSecret(id, key string) error {
	ap, err := a.secretDecl(id, key)
	if err != nil {
		return err
	}
	account := asksForAccount(ap.Setting(key))
	dir, err := a.credentialsDir()
	if err != nil {
		return err
	}
	name := keyProfileName(id, key)
	path := filepath.Join(dir, name+".key")
	orig := a.configSnapshot()
	prof, ours := orig.Plugins.AuthProfiles[name]
	ours = ours && samePath(prof.SecretFile, path)
	previous, _ := orig.Plugins.Settings[id][key].(string)
	err = a.commitConfig(orig, func(c *Config) {
		c.Plugins.Settings = withSetting(c.Plugins.Settings, id, key, "")
		if ours {
			profiles := make(map[string]broker.AuthProfile, len(c.Plugins.AuthProfiles))
			for k, v := range c.Plugins.AuthProfiles {
				if k != name {
					profiles[k] = v
				}
			}
			if len(profiles) == 0 {
				profiles = nil
			}
			c.Plugins.AuthProfiles = profiles
			grants := copyGrants(c.Plugins.Grants)
			g := grants[id]
			g.CredentialHandles = removeAll(g.CredentialHandles, name)
			grants[id] = g
			c.Plugins.Grants = grants
		}
		if account && previous != "" {
			releaseAccount(c, id, previous)
		}
	}, nil, nil)
	if err != nil {
		return err
	}
	if ours {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("the key is no longer used, but its file %s could not be removed: %w", path, err)
		}
	}
	logsink.Info("config.decision", "plugin %s: key for %s forgotten", id, key)
	return nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

func copyGrants(in map[string]broker.Grant) map[string]broker.Grant {
	out := make(map[string]broker.Grant, len(in)+1)
	for k, v := range in {
		v.CredentialHandles = append([]string(nil), v.CredentialHandles...)
		v.Hosts = append([]string(nil), v.Hosts...)
		out[k] = v
	}
	return out
}

func addOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func removeAll(list []string, v string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func withSetting(in map[string]map[string]interface{}, id, key, v string) map[string]map[string]interface{} {
	out := make(map[string]map[string]interface{}, len(in)+1)
	for pid, vals := range in {
		copied := make(map[string]interface{}, len(vals)+1)
		for k, val := range vals {
			copied[k] = val
		}
		out[pid] = copied
	}
	if v == "" {
		if vals := out[id]; vals != nil {
			delete(vals, key)
			if len(vals) == 0 {
				delete(out, id)
			}
		}
	} else {
		if out[id] == nil {
			out[id] = map[string]interface{}{}
		}
		out[id][key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
