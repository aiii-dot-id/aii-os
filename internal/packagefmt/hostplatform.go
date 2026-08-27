package packagefmt

// The host's own coordinates in the MANIFEST vocabulary — the adopted
// platform/arch/topology names variant selection matches against.
//
// The vocabulary is C-owned and closed (manifest.schema.json:
// platform enum [linux, macos, windows, android, ios]; arch enum
// [x86_64, arm64]; topology enum [full_identity_host,
// mobile_app_host]) and the C stack derives its own coordinates at
// compile time exactly like this (manifest.c:1447-1478
// sev_manifest_current_platform/arch/topology). Go's mapping is
// explicit, never a passthrough of GOOS/GOARCH spellings: darwin is
// "macos" and amd64 is "x86_64" in the shared contract.
//
// A GOOS/GOARCH outside the five-platform law maps to "" — no variant
// can match an empty coordinate, so selection refuses with the host
// named rather than guessing a nearest neighbor.

import "runtime"

// HostPlatform is the running build's platform in manifest vocabulary.
func HostPlatform() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	case "android":
		return "android"
	case "ios":
		return "ios"
	}
	return ""
}

// HostArch is the running build's arch in manifest vocabulary.
func HostArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	}
	return ""
}

// HostTopology is the running build's topology: the mobile shells host
// a mobile_app_host, every desktop build a full_identity_host —
// the same compile-time split the C stack makes (manifest.c:1471-1478).
func HostTopology() string {
	switch runtime.GOOS {
	case "android", "ios":
		return "mobile_app_host"
	}
	return "full_identity_host"
}
