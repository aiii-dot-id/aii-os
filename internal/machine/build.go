package machine

import (
	"debug/buildinfo"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

type Build struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

func Self() Build { return Build{Version: version.Authored(), Commit: version.Commit()} }

func (b Build) String() string {
	switch {
	case b.Version != "" && b.Commit != "":
		return b.Version + ", " + b.Commit
	case b.Version != "":
		return b.Version
	case b.Commit != "":
		return "build " + b.Commit
	}
	return "an unknown build"
}

func ProgramBuild(exe string) (Build, error) {
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		return Build{}, fmt.Errorf("read the build record of %s: %w", exe, err)
	}
	return Build{Version: install.ReleaseVersion(exe), Commit: version.CommitOf(bi)}, nil
}
