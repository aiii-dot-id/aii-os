package packagefmt

import "runtime"

var goosPlatforms = [...]struct{ goos, platform string }{
	{"linux", "linux"},
	{"darwin", "macos"},
	{"windows", "windows"},
	{"android", "android"},
	{"ios", "ios"},
}

func HostPlatform() string { return platformOf(runtime.GOOS) }

func platformOf(goos string) string {
	for _, p := range goosPlatforms {
		if p.goos == goos {
			return p.platform
		}
	}
	return ""
}

func GOOSFor(platform string) string {
	for _, p := range goosPlatforms {
		if p.platform == platform {
			return p.goos
		}
	}
	return ""
}

func HostArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	}
	return ""
}

func HostTopology() string {
	switch runtime.GOOS {
	case "android", "ios":
		return "mobile_app_host"
	}
	return "full_identity_host"
}
