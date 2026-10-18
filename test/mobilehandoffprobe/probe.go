// Package mobilehandoffprobe is a disposable device-acceptance fixture. Bind
// it with gomobile into a separately named test app, never the installed shell.
// It drives the real mobile.Start/DashboardURL boundary in empty private homes;
// founding endpoints deliberately refuse locally, so no identity is created and
// no external service is contacted. See TESTING.md for qualification scope.
package mobilehandoffprobe

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"github.com/aiii-dot-id/aii-os/mobile"
)

// Probe is owned serially by the test app's background runner.
type Probe struct {
	// CompanionHost is supplied by the native test app's interface API.
	// Android forbids the routing-table query used by Go's InterfaceAddrs.
	CompanionHost string
	rt            *mobile.Runtime
	detail        string
}

func Create() *Probe { return &Probe{} }

// StartCase starts one real FIRSTBOOT runtime in a new disposable container.
// The caller must supply its test app's private files directory, not AII OS's.
func (p *Probe) StartCase(home, kind string) (string, error) {
	if err := p.Stop(); err != nil {
		return "", fmt.Errorf("the previous case did not end cleanly: %w", err)
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", fmt.Errorf("test app private absolute directory required")
	}
	host, secure := "127.0.0.1", false
	switch kind {
	case "plain4":
	case "plain6":
		host = "::1"
	case "wildcard":
		host = "0.0.0.0"
	case "companion":
		secure = true
		host = p.CompanionHost
		if ip := net.ParseIP(host); ip == nil || ip.To4() == nil || !ip.IsPrivate() {
			return "", fmt.Errorf("native test app must supply its observed private IPv4 interface")
		}
	default:
		return "", fmt.Errorf("unknown test case %q", kind)
	}
	dir, err := os.MkdirTemp(home, "f016-")
	if err != nil {
		return "", err
	}
	config := map[string]any{
		"dashboard":   map[string]any{"host": host, "port": 0, "tls": secure},
		"genesis":     map[string]any{"server_url": "http://127.0.0.1:1", "firewall_url": "http://127.0.0.1:1", "bootstrap_url": "http://127.0.0.1:1"},
		"certificate": map[string]any{"server_url": "none"},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return "", err
	}
	p.rt, err = mobile.Start(path, dir)
	if err != nil {
		return "", err
	}
	url := p.rt.DashboardURL()
	detail, _ := json.Marshal(map[string]any{"case": kind, "url": url, "configured_host": host, "configured_port": 0, "configured_tls": secure, "commit": mobile.BuildID(), "go": runtime.Version()})
	p.detail = string(detail)
	return url, nil
}

func (p *Probe) Detail() string { return p.detail }

// Stop ends the current case's runtime and says whether it ended cleanly.
func (p *Probe) Stop() error {
	if p.rt == nil {
		return nil
	}
	err := p.rt.Shutdown()
	p.rt = nil
	return err
}
