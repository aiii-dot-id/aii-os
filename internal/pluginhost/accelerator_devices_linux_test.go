//go:build linux

package pluginhost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestLinuxVulkanDeviceAdmission(t *testing.T) {
	char := fs.ModeDevice | fs.ModeCharDevice | 0666
	fixture := func() fstest.MapFS {
		return fstest.MapFS{
			"dri/renderD128": {Mode: char}, "dri/card0": {Mode: char},
			"nvidia0": {Mode: char}, "nvidiactl": {Mode: char},
			"nvidia-modeset": {Mode: char}, "nvidia-uvm": {Mode: char},
			"sda": {Mode: fs.ModeDevice}, "input/event0": {Mode: char},
			"dri/renderDnot-a-number": {Mode: char},
			"nvidia0-backup":          {Mode: char},
		}
	}
	vulkan := &AcceleratorProfile{Backend: "vulkan"}
	t.Run("compute nodes only", func(t *testing.T) {
		got, err := linuxAcceleratorDevices(vulkan, fixture())
		want := []string{"/dev/dri/renderD128", "/dev/nvidia0", "/dev/nvidiactl"}
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("compute-only admission: got %v, %v; want %v", got, err, want)
		}
	})
	for _, backend := range []string{"", "cpu", "cuda", "metal"} {
		t.Run("no Vulkan request "+backend, func(t *testing.T) {
			var profile *AcceleratorProfile
			if backend != "" {
				profile = &AcceleratorProfile{Backend: backend}
			}
			// .
			got, err := linuxAcceleratorDevices(profile, failingDeviceFS{})
			if err != nil || len(got) != 0 {
				t.Fatalf("non-Vulkan plugin gained device access: %v, %v", got, err)
			}
		})
	}
	for _, mode := range []fs.FileMode{0644, fs.ModeSymlink, fs.ModeDir, fs.ModeDevice, fs.ModeNamedPipe} {
		t.Run(mode.String(), func(t *testing.T) {
			files := fixture()
			files["dri/renderD128"].Mode = mode
			if got, err := linuxAcceleratorDevices(vulkan, files); err == nil || got != nil {
				t.Fatalf("non-character render node admitted: %v, %v", got, err)
			}
		})
	}
	for _, absent := range []string{"nvidiactl", "nvidia0"} {
		t.Run("incomplete "+absent, func(t *testing.T) {
			files := fixture()
			delete(files, absent)
			if got, err := linuxAcceleratorDevices(vulkan, files); err == nil || got != nil {
				t.Fatalf("incomplete NVIDIA pair admitted: %v, %v", got, err)
			}
		})
	}
	t.Run("no hardware", func(t *testing.T) {
		if got, err := linuxAcceleratorDevices(vulkan, fstest.MapFS{}); err == nil || got != nil {
			t.Fatalf("absence was admitted as hardware: %v, %v", got, err)
		}
	})
	t.Run("directory symlink", func(t *testing.T) {
		files := fstest.MapFS{"dri": {Mode: fs.ModeSymlink}}
		if got, err := linuxAcceleratorDevices(vulkan, files); err == nil || got != nil {
			t.Fatalf("directory alias admitted: %v, %v", got, err)
		}
	})
	t.Run("read error is not absence", func(t *testing.T) {
		if _, err := linuxAcceleratorDevices(vulkan, failingDeviceFS{}); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("device read failure lost: %v", err)
		}
	})
	t.Run("DRI without NVIDIA", func(t *testing.T) {
		got, err := linuxAcceleratorDevices(vulkan, fstest.MapFS{"dri/renderD128": {Mode: char}})
		if err != nil || !slices.Equal(got, []string{"/dev/dri/renderD128"}) {
			t.Fatalf("vendor-neutral render node refused: %v, %v", got, err)
		}
	})
}

type failingDeviceFS struct{}

func (failingDeviceFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

// .
// .
func TestLinuxVulkanProfileKeepsTheWall(t *testing.T) {
	if os.Getenv("AII_LINUX_VULKAN_REQUIRED") != "1" {
		t.Skip("explicit live Linux GPU qualification not requested")
	}
	profile := &AcceleratorProfile{Backend: "vulkan"}
	devices, err := linuxAcceleratorDevices(profile, os.DirFS("/dev"))
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	sentinel := t.TempDir() + "/sentinel"
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	script := `test ! -e /dev/dri/card0 && test ! -e /dev/input &&
test ! -e /dev/snd && test ! -e /dev/nvidia-modeset &&
test ! -e /dev/nvidia-uvm || exit 31
if (echo modified > "$1") 2>/dev/null; then exit 32; fi
if test -s /etc/shadow; then exit 33; fi
if test -d /root/.ssh && test -n "$(ls -A /root/.ssh 2>/dev/null)"; then exit 34; fi
if test "$(readlink /proc/self/ns/net)" = "$2"; then exit 35; fi
exec vulkaninfo --summary`
	network, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	argv, wall, err := containArgv(context.Background(), []string{"/bin/sh", "-c", script, "probe", sentinel, network}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !wall.NetworkDenied || !wall.FilesystemRestricted {
		t.Fatalf("accelerator lost containment: %+v", wall)
	}
	for _, device := range devices {
		if !strings.Contains(wall.Description, device) {
			t.Fatalf("admitted device not disclosed: %s", device)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("contained Vulkan/wall proof failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PHYSICAL_DEVICE_TYPE_DISCRETE_GPU") &&
		!strings.Contains(string(out), "PHYSICAL_DEVICE_TYPE_INTEGRATED_GPU") {
		t.Fatalf("no hardware Vulkan device under production containment: %s", out)
	}
	raw, err := os.ReadFile(sentinel)
	if err != nil || string(raw) != "unchanged" {
		t.Fatalf("root write protection lost: %v", err)
	}
	t.Logf("admitted %v; network namespace isolated; filesystem/credential masks retained\n%s", devices, out)
}
