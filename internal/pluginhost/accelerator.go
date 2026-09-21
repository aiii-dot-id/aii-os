package pluginhost

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
// .

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
const AcceleratorFile = "accelerator.json"

// .
// .
// .
const MaxStartupMS = 3600000

// .
// .
// .
// .
const WindowsContainedNativeQualified = true

// .
const WindowsNativeRefusal = "runtime:native_t3_component (Windows contained native is not yet qualified — the AppContainer wall is implemented; real-backend qualification is still required before native T3 admission)"

var reToken = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,63}$`)

// .
type AcceleratorProfile struct {
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	Backend          string   `json:"backend"`
	Operators        []string `json:"operators,omitempty"`
	RuntimeLibraries []string `json:"runtime_libraries,omitempty"`
	Precision        string   `json:"precision"`
	Models           []string `json:"models"`
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
	MemoryBytes int64 `json:"memory_bytes"`
	// .
	// .
	// .
	// .
	// .
	// .
	DeviceMemoryBytes *int64 `json:"device_memory_bytes,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	StartupMS    *int64 `json:"startup_ms,omitempty"`
	SessionLimit int    `json:"session_limit"`
	Fallback     string `json:"fallback"`
}

// .
type AcceleratorError struct {
	PluginID string
	Detail   string
}

func (e *AcceleratorError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not an accelerator declaration the host honors: %s", e.PluginID, AcceleratorFile, e.Detail)
}

// .
// .
func ParseAccelerators(raw []byte, variantIDs []string) (map[string]AcceleratorProfile, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var profiles map[string]AcceleratorProfile
	if err := dec.Decode(&profiles); err != nil {
		return nil, fmt.Errorf("not an object of profiles by variant id: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content")
	}
	known := map[string]bool{}
	for _, id := range variantIDs {
		known[id] = true
	}
	for id, p := range profiles {
		if !known[id] {
			return nil, fmt.Errorf("profile for %q: not a variant this package declares", id)
		}
		if !reToken.MatchString(p.OS) || !reToken.MatchString(p.Arch) || !reToken.MatchString(p.Backend) {
			return nil, fmt.Errorf("profile for %s: os, arch and backend are required tokens", id)
		}
		if !reToken.MatchString(p.Precision) {
			return nil, fmt.Errorf("profile for %s: precision is a required token (e.g. int8, fp16)", id)
		}
		if len(p.Operators) > 64 || len(p.RuntimeLibraries) > 32 || len(p.Models) > MaxProfileModels || len(p.Models) == 0 {
			return nil, fmt.Errorf("profile for %s: at most 64 operators, 32 runtime libraries and %d models, and at least one model", id, MaxProfileModels)
		}
		for _, list := range [][]string{p.Operators, p.RuntimeLibraries, p.Models} {
			for _, s := range list {
				if s == "" || len(s) > 128 || strings.ContainsAny(s, "\x00\r\n") {
					return nil, fmt.Errorf("profile for %s: list entries are short names", id)
				}
			}
		}
		if p.MemoryBytes <= 0 || p.SessionLimit <= 0 {
			return nil, fmt.Errorf("profile for %s: memory_bytes and session_limit are declared, positive numbers", id)
		}
		// .
		// .
		// .
		if p.DeviceMemoryBytes != nil && *p.DeviceMemoryBytes < 0 {
			return nil, fmt.Errorf("profile for %s: device_memory_bytes is a reservation in bytes (omit it where there is nothing to declare; 0 declares no device allocation)", id)
		}
		if p.StartupMS != nil && (*p.StartupMS <= 0 || *p.StartupMS > MaxStartupMS) {
			return nil, fmt.Errorf("profile for %s: startup_ms is an allowance of 1..%d milliseconds (omit it to take the host's default)", id, MaxStartupMS)
		}
		if p.Fallback != "none" && p.Fallback != "reported" {
			return nil, fmt.Errorf("profile for %s: fallback is \"none\" or \"reported\" — never taken silently", id)
		}
	}
	return profiles, nil
}

// .
// .
func loadAccelerator(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest, variantID string) (*AcceleratorProfile, error) {
	if _, present := res.FileDigests[AcceleratorFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, AcceleratorFile)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(m.Variants))
	for _, v := range m.Variants {
		ids = append(ids, v.VariantID)
	}
	profiles, err := ParseAccelerators(raw, ids)
	if err != nil {
		return nil, &AcceleratorError{PluginID: m.ID, Detail: err.Error()}
	}
	p, ok := profiles[variantID]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// .
type Readiness struct {
	Line         string
	ModelsLoaded int
	Accelerator  string
	ProbeMS      int
}

// .
// .
func (r Readiness) Real() bool {
	return r.ModelsLoaded > 0 && r.Accelerator != "" && r.ProbeMS >= 0 && strings.Contains(r.Line, "probe_ms=")
}

// .
const ReadyMark = "event=ready"

// .
// .
func ParseReadiness(line string) Readiness {
	r := Readiness{Line: line, ProbeMS: -1}
	for _, f := range strings.Fields(line) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "models_loaded":
			r.ModelsLoaded, _ = strconv.Atoi(v)
		case "accelerator":
			r.Accelerator = v
		case "probe_ms":
			if n, err := strconv.Atoi(v); err == nil {
				r.ProbeMS = n
			}
		}
	}
	return r
}

// .
// .
type ReadinessError struct {
	PluginID string
	Line     string
}

func (e *ReadinessError) Error() string {
	return fmt.Sprintf("pluginhost: %s: the native child spawned but did not report real readiness (models_loaded, accelerator, probe_ms on its event=ready line); got %q", e.PluginID, e.Line)
}
