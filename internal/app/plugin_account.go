package app

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

type AccountRefusal struct {
	Requirement string
	Text        string
}

func (e *AccountRefusal) Error() string { return e.Text }

const (
	accountNeedsHosts    = "hosts"
	accountNeedsProfile  = "profile"
	accountNeedsProvider = "provider"
	accountNeedsServices = "services"
	accountNeedsRide     = "rides"
)

func refuseAccount(requirement, format string, args ...interface{}) error {
	return &AccountRefusal{Requirement: requirement, Text: fmt.Sprintf(format, args...)}
}

func settingDecl(ap *pluginhost.ActivePlugin, key string) *pluginhost.SettingDecl {
	for i := range ap.Settings {
		if ap.Settings[i].Key == key {
			return &ap.Settings[i]
		}
	}
	return nil
}

func asksForAccount(d *pluginhost.SettingDecl) bool {
	return d != nil && d.Type == pluginhost.SettingSecret && d.OAuth != nil
}

func accountHosts(ap *pluginhost.ActivePlugin) ([]string, error) {
	declared := outboundHosts(ap.Capabilities)
	if len(declared) == 0 {
		return nil, refuseAccount(accountNeedsHosts, "plugin %s declares no outbound host in its signed package, so where an account's token would ride is not the package's own word — grant its handle and host in the config file (plugins.grants.%s)", ap.ID, ap.ID)
	}
	out := make([]string, 0, len(declared))
	for _, d := range declared {
		host, port, err := signedHostPort(d)
		if err != nil {
			return nil, refuseAccount(accountNeedsHosts, "plugin %s: %v", ap.ID, err)
		}
		out = append(out, host+":"+strconv.Itoa(port))
	}
	return out, nil
}

func accountFit(name string, p broker.AuthProfile, hint pluginhost.SettingOAuthHint, hosts []string, catalog map[string]oauth.Provider) error {
	if p.Scheme != broker.SchemeOAuth2 {
		scheme := p.Scheme
		if scheme == "" {
			scheme = "bearer"
		}
		return refuseAccount(accountNeedsProfile, "%s is a %s key, not an account of an authority", name, scheme)
	}
	if provider := providerName(p); provider != hint.Provider {
		return refuseAccount(accountNeedsProvider, "%s is a %s account, and this setting asks for a %s one", name, provider, hint.Provider)
	}
	_, tpl, err := p.Contract(catalog)
	if err != nil {
		return refuseAccount(accountNeedsProfile, "%s cannot be used: %v", name, err)
	}
	for _, svc := range hint.Services {
		if !servesService(tpl.Scopes[svc], p.Scopes) {
			return refuseAccount(accountNeedsServices, "%s was not set up for %s — add it to the account under Connected accounts and connect again", name, svc)
		}
	}
	var missing []string
	for _, want := range hosts {
		rides := false
		for _, h := range tpl.Hosts {
			if strings.EqualFold(strings.TrimSpace(h), want) {
				rides = true
			}
		}
		if !rides {
			missing = append(missing, want)
		}
	}
	switch {
	case len(missing) == 0:
		return nil
	case len(hosts) == 1:
		return refuseAccount(accountNeedsRide, "%s's token rides only to %s, not to %s, where this plugin's signed package sends its requests — every call would be refused", name, strings.Join(tpl.Hosts, ", "), missing[0])
	}
	return refuseAccount(accountNeedsRide, "%s's token rides only to %s, not to %s, which this plugin's signed package also sends its requests to — every call to %s would be refused", name, strings.Join(tpl.Hosts, ", "), strings.Join(missing, ", "), strings.Join(missing, ", "))
}

func providerName(p broker.AuthProfile) string {
	if p.Provider == "" {
		return "custom"
	}
	return p.Provider
}

func servesService(set oauth.ScopeSet, scopes []string) bool {
	need := set.Read
	if len(need) == 0 {
		need = set.Modify
	}
	if len(need) == 0 {
		return false
	}
	for _, s := range need {
		if !slices.Contains(scopes, s) {
			return false
		}
	}
	return true
}

func (a *App) accountFor(cfg *Config, ap *pluginhost.ActivePlugin, d pluginhost.SettingDecl, handle string) ([]string, error) {
	hosts, err := accountHosts(ap)
	if err != nil {
		return nil, err
	}
	prof, ok := cfg.Plugins.AuthProfiles[handle]
	if !ok {
		return nil, refuseAccount(accountNeedsProfile, "%s is not an account on this identity — connect one under Connected accounts on the Plugins page first", handle)
	}
	catalog, err := a.oauthContracts()
	if err != nil {
		return nil, fmt.Errorf("the OAuth contracts cannot be read, so %s cannot be held to its authority: %w", handle, err)
	}
	if err := accountFit(handle, prof, *d.OAuth, hosts, catalog); err != nil {
		return nil, err
	}
	return hosts, nil
}

func grantAccount(cfg *Config, id, setting, handle string, hosts []string, previous string) {
	if g := cfg.Plugins.Grants[id]; previous == handle && slices.Contains(g.CredentialHandles, handle) && containsAll(g.Hosts, hosts) {
		logsink.Info("config.decision", "plugin %s: %s is already its account for %s, granted with %s — nothing changed", id, handle, setting, strings.Join(hosts, ", "))
		return
	}
	grants := copyGrants(cfg.Plugins.Grants)
	g := grants[id]
	g.CredentialHandles = addOnce(g.CredentialHandles, handle)
	for _, h := range hosts {
		g.Hosts = addOnce(g.Hosts, h)
	}
	grants[id] = g
	cfg.Plugins.Grants = grants
	if previous != "" && previous != handle {
		releaseAccount(cfg, id, previous)
	}
}

func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func releaseAccount(cfg *Config, id, handle string) {
	for _, v := range cfg.Plugins.Settings[id] {
		if v == handle {
			return
		}
	}
	if g, ok := cfg.Plugins.Grants[id]; !ok || !slices.Contains(g.CredentialHandles, handle) {
		return
	}
	grants := copyGrants(cfg.Plugins.Grants)
	g := grants[id]
	if g.CredentialHandles = removeAll(g.CredentialHandles, handle); len(g.CredentialHandles) == 0 {
		g.CredentialHandles = nil
	}
	if grantEmpty(g) {
		delete(grants, id)
	} else {
		grants[id] = g
	}
	if len(grants) == 0 {
		grants = nil
	}
	cfg.Plugins.Grants = grants
}

func accountView(c *Config, ap *pluginhost.ActivePlugin, d pluginhost.SettingDecl, contracts func() (map[string]oauth.Provider, error)) *dashboard.SettingAccountView {
	v := &dashboard.SettingAccountView{}
	hosts, err := accountHosts(ap)
	if err != nil {
		v.Not = err.Error()
		return v
	}
	v.Hosts = hosts
	current, _ := c.Plugins.Settings[ap.ID][d.Key].(string)
	if g, ok := c.Plugins.Grants[ap.ID]; ok && current != "" {
		v.Granted = slices.Contains(g.CredentialHandles, current) && containsAll(g.Hosts, hosts)
	}
	names := make([]string, 0, len(c.Plugins.AuthProfiles))
	for name := range c.Plugins.AuthProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	catalog, catErr := contracts()
	for _, name := range names {
		p := c.Plugins.AuthProfiles[name]
		if name != current && (p.Scheme != broker.SchemeOAuth2 || providerName(p) != d.OAuth.Provider) {
			continue
		}
		offer := dashboard.AccountOffer{Name: name, State: "static"}
		if p.Scheme == broker.SchemeOAuth2 {
			offer.State, _ = profileState(p)
		}
		if catErr != nil {
			offer.Not = "the OAuth contracts cannot be read: " + catErr.Error()
		} else if err := accountFit(name, p, *d.OAuth, hosts, catalog); err != nil {
			offer.Not = err.Error()
		}
		v.Offers = append(v.Offers, offer)
	}
	return v
}
