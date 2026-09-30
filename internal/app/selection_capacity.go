package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

func pluginAdmissionPolicy(cfg Config) pluginfacility.AdmissionPolicy {
	return pluginfacility.AdmissionPolicy{ReserveBytes: cfg.Plugins.Runtime.AdmissionMemoryReserveBytes,
		BudgetBytes: cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes, MaxConcurrentStarts: cfg.Plugins.Runtime.MaxConcurrentStarts}
}

func measureSelectionResources(ctx context.Context) (pluginfacility.Availability, error) {
	if err := ctx.Err(); err != nil {
		return pluginfacility.Availability{}, err
	}
	a := (hostCapacity{}).Measure()
	a.Devices = make(map[string]pluginfacility.DeviceAvailability)
	switch packagefmt.HostPlatform() {
	case "macos":

		if runtime.GOARCH == "arm64" && a.HostKnown {
			raw, err := selectionQuery(ctx, "/usr/sbin/system_profiler", "SPDisplaysDataType", "-json")
			if err == nil && hasAppleMetal(raw) {
				a.Devices["metal"] = pluginfacility.DeviceAvailability{Known: true, Total: a.HostTotal, Available: a.HostAvailable, Unified: true}
			}
		}
	case "linux", "windows":
		path := "/usr/bin/nvidia-smi"
		if packagefmt.HostPlatform() == "windows" {
			root := os.Getenv("SystemRoot")
			if !filepath.IsAbs(root) {
				break
			}
			path = filepath.Join(root, "System32", "nvidia-smi.exe")
		}

		raw, err := selectionQuery(ctx, path, "--id=0", "--query-gpu=memory.total,memory.free,compute_mode", "--format=csv,noheader,nounits")
		if err == nil {
			if d, ok := parseNvidiaCapacity(raw); ok {
				a.Devices["cuda"] = d
			}
		}
	}
	return a, ctx.Err()
}

func selectionQuery(ctx context.Context, path string, args ...string) ([]byte, error) {
	if !hostcap.Can(hostcap.Subprocess).Available {
		return nil, errors.New("hardware query unavailable on this topology")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var out limitedSelectionOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	cmd.WaitDelay = 250 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type limitedSelectionOutput struct{ bytes.Buffer }

func (b *limitedSelectionOutput) Write(p []byte) (int, error) {
	if len(p) > (64<<10)-b.Len() {
		return 0, errors.New("hardware query exceeded 64 KiB")
	}
	return b.Buffer.Write(p)
}

func parseNvidiaCapacity(raw []byte) (pluginfacility.DeviceAvailability, bool) {
	fields := strings.Split(strings.TrimSpace(string(raw)), ",")
	if len(fields) != 3 || strings.TrimSpace(fields[2]) != "Default" {
		return pluginfacility.DeviceAvailability{}, false
	}
	total, e1 := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
	free, e2 := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
	if e1 != nil || e2 != nil || total <= 0 || free < 0 || free > total || total > math.MaxInt64/(1<<20) {
		return pluginfacility.DeviceAvailability{}, false
	}
	return pluginfacility.DeviceAvailability{Known: true, Total: total << 20, Available: free << 20}, true
}

func hasAppleMetal(raw []byte) bool {
	var facts struct {
		Displays []struct {
			Vendor string `json:"spdisplays_vendor"`
			Family string `json:"spdisplays_mtlgpufamilysupport"`
			Metal  string `json:"spdisplays_metal"`
		} `json:"SPDisplaysDataType"`
	}
	if json.Unmarshal(raw, &facts) != nil {
		return false
	}
	for _, d := range facts.Displays {
		if d.Vendor != "sppci_vendor_Apple" {
			continue
		}
		version := strings.TrimPrefix(d.Family, "spdisplays_metal")
		v, err := strconv.Atoi(version)
		if (err == nil && v > 0) || d.Metal == "spdisplays_supported" {
			return true
		}
	}
	return false
}
