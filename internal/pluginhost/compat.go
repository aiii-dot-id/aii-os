package pluginhost

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/version"
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
// .
// .
// .
// .

// .
// .
type HostVersionError struct {
	PluginID       string
	PackageVersion string
	HostVersion    string
	Min            string
	MaxExclusive   string
	HostTooOld     bool
	// .
	// .
	Unknown bool
}

// .
func (e *HostVersionError) window() string {
	switch {
	case e.Min != "" && e.MaxExclusive != "":
		return e.Min + " up to but not including " + e.MaxExclusive
	case e.Min != "":
		return e.Min + " or newer"
	default:
		return "below " + e.MaxExclusive
	}
}

func (e *HostVersionError) Error() string {
	if e.Unknown {
		return fmt.Sprintf("pluginhost: %s %s declares the AII OS versions it runs on (%s), and this host's own version %q cannot be read — whether it is one of them cannot be decided, so it is refused rather than guessed at",
			e.PluginID, e.PackageVersion, e.window(), e.HostVersion)
	}
	if e.HostTooOld {
		return fmt.Sprintf("pluginhost: %s %s is authored for AII OS %s or newer, and this host is %s — update the host, or install a release authored for it",
			e.PluginID, e.PackageVersion, e.Min, e.HostVersion)
	}
	return fmt.Sprintf("pluginhost: %s %s is authored for AII OS below %s, and this host is %s — install a release authored for this host",
		e.PluginID, e.PackageVersion, e.MaxExclusive, e.HostVersion)
}

// .
// .
func hostVersionFor(opts *Options) string {
	if opts != nil && opts.HostVersion != "" {
		return opts.HostVersion
	}
	return version.Authored()
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
// .
// .
// .
// .
func checkHostWindow(m *packagefmt.Manifest, hostVersion string) error {
	if m == nil || (m.AiiosMinVersion == "" && m.AiiosMaxExclusiveVersion == "") {
		return nil
	}
	out := &HostVersionError{PluginID: m.ID, PackageVersion: m.Version, HostVersion: hostVersion,
		Min: m.AiiosMinVersion, MaxExclusive: m.AiiosMaxExclusiveVersion}
	// .
	// .
	// .
	if !packagefmt.ValidHostBound(hostVersion) {
		out.Unknown = true
		return out
	}
	if m.AiiosMinVersion != "" && packagefmt.CompareHostBounds(hostVersion, m.AiiosMinVersion) < 0 {
		out.HostTooOld = true
		return out
	}
	if m.AiiosMaxExclusiveVersion != "" && packagefmt.CompareHostBounds(hostVersion, m.AiiosMaxExclusiveVersion) >= 0 {
		return out
	}
	return nil
}
