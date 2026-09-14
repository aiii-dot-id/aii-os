package packagefmt

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

import "runtime"

// .
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

// .
func HostArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	}
	return ""
}

// .
// .
// .
func HostTopology() string {
	switch runtime.GOOS {
	case "android", "ios":
		return "mobile_app_host"
	}
	return "full_identity_host"
}
