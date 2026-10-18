//go:build linux

package pluginhost

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestLinuxCUDADeviceAdmission(t *testing.T) {
	char := fs.ModeDevice | fs.ModeCharDevice | 0666
	fixture := func() fstest.MapFS {
		return fstest.MapFS{"nvidia0": {Mode: char}, "nvidiactl": {Mode: char}, "nvidia-uvm": {Mode: char},
			"nvidia-uvm-tools": {Mode: char}, "nvidia-modeset": {Mode: char}, "dri/renderD128": {Mode: char},
			"dri/card0": {Mode: char}, "input/event0": {Mode: char}, "snd/controlC0": {Mode: char}}
	}
	for _, backend := range []string{"cuda", "cuda+vulkan", "vulkan+cuda"} {
		t.Run(backend, func(t *testing.T) {
			profile := &AcceleratorProfile{Backend: backend}
			want := []string{"/dev/nvidia-uvm", "/dev/nvidia0", "/dev/nvidiactl"}
			if backend != "cuda" {
				want = append([]string{"/dev/dri/renderD128"}, want...)
			}
			got, err := linuxAcceleratorDevices(profile, fixture())
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("got %v %v; want %v", got, err, want)
			}
			for _, name := range []string{"nvidia0", "nvidiactl", "nvidia-uvm"} {
				files := fixture()
				delete(files, name)
				if _, err := linuxAcceleratorDevices(profile, files); err == nil {
					t.Fatalf("missing %s admitted", name)
				}
			}
			for _, mode := range []fs.FileMode{0644, fs.ModeSymlink, fs.ModeDir, fs.ModeDevice, fs.ModeNamedPipe} {
				files := fixture()
				files["nvidia-uvm"].Mode = mode
				if _, err := linuxAcceleratorDevices(profile, files); err == nil {
					t.Fatalf("non-character UVM admitted: %v", mode)
				}
			}
		})
	}
	for _, backend := range []string{"cuda+unknown", "vulkan+vulkan", "+cuda", "cuda+"} {
		if _, err := linuxAcceleratorDevices(&AcceleratorProfile{Backend: backend}, fixture()); err == nil {
			t.Fatalf("unsupported composition %q admitted", backend)
		}
	}
}

func TestLinuxCUDAProfileKeepsTheWall(t *testing.T) {
	if os.Getenv("AII_LINUX_CUDA_REQUIRED") != "1" {
		t.Skip("explicit live CUDA qualification not requested")
	}
	sentinel := t.TempDir() + "/sentinel"
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	network, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	script := `import ctypes,os,sys
assert os.path.exists('/dev/nvidia-uvm')
for path in ['/dev/nvidia-uvm-tools','/dev/nvidia-modeset','/dev/dri/card0','/dev/input','/dev/snd']:
 assert not os.path.exists(path),path
assert os.readlink('/proc/self/ns/net')!=sys.argv[2]
assert os.path.exists('/proc/self/task/'+str(os.getpid())+'/comm')
try:
 open(sys.argv[1],'w').write('changed')
except OSError:
 pass
else:
 raise RuntimeError('host filesystem writable')
cuda=ctypes.CDLL('libcuda.so.1')
assert cuda.cuInit(0)==0,'CUDA initialization failed'
count=ctypes.c_int()
assert cuda.cuDeviceGetCount(ctypes.byref(count))==0 and count.value>0
print('contained_cuda_devices='+str(count.value))`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	argv, wall, err := containArgv(ctx, []string{"/usr/bin/python3", "-c", script, sentinel, network}, &AcceleratorProfile{Backend: "cuda+vulkan"}, nativeFiles{})
	if err != nil {
		t.Fatal(err)
	}
	if !wall.NetworkDenied || !wall.FilesystemRestricted {
		t.Fatalf("containment lost: %+v", wall)
	}
	out, err := walled(t, exec.CommandContext(ctx, argv[0], argv[1:]...), wall).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "contained_cuda_devices=") {
		t.Fatalf("contained CUDA: %v %s", err, out)
	}
	raw, err := os.ReadFile(sentinel)
	if err != nil || string(raw) != "unchanged" {
		t.Fatalf("sentinel changed: %v", err)
	}
}
