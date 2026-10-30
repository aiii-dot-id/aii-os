//go:build darwin

package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func TestARealEngineReachesReadinessOnMetalBehindTheWall(t *testing.T) {
	carrier, models := os.Getenv("AII_WALL_ENGINE"), os.Getenv("AII_WALL_MODELS")
	if carrier == "" || models == "" {
		t.Skip("set AII_WALL_ENGINE and AII_WALL_MODELS (a carrier at its runtime root and its models) to start a real engine")
	}
	requireWall(t)
	timeout := 3 * time.Minute
	if v := os.Getenv("AII_WALL_READY_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			t.Fatalf("AII_WALL_READY_TIMEOUT %q is not a positive duration", v)
		}
		timeout = d
	}
	root := filepath.Dir(carrier)
	private := filepath.Join(shortDir(t), "private")
	start := func(contained bool) {
		t.Helper()
		argv := []string{carrier}
		var wall supervisor.Containment
		if contained {
			var err error
			argv, wall, err = containArgv(t.Context(), argv, &AcceleratorProfile{Backend: "metal"},
				nativeFiles{own: []string{root}, read: []string{root, models}, private: private})
			if err != nil {
				t.Fatalf("containment refused on a host that can build it: %v", err)
			}
		}
		capture := logsink.CaptureForTest(t)
		const id = "id.aiii.voice"
		spec := supervisor.Spec{
			PluginID: id, Argv: argv, ArgvContainment: wall,
			Env:         []string{"SEV_PLUGIN_SOCKET=stdio:", "SEV_PLUGIN_ID=" + id, "AII_MODELS_DIR=" + models, "AII_RUNTIME_ROOT=" + root},
			SessionMode: true, AudioPair: true,
			ReadyMark: ReadyMark, ReadyTimeout: timeout,
			Backoff: supervisor.Backoff{Initial: time.Second, Max: time.Second},
		}
		began := time.Now()
		s, err := supervisor.Start(spec, nil)
		if err != nil {
			t.Fatalf("contained=%v: the engine did not reach readiness: %v\n--- host log ---\n%s", contained, err, capture.String())
		}
		took := time.Since(began)
		line := s.ReadyLine()
		if err := s.Close(); err != nil {
			t.Errorf("contained=%v: close: %v", contained, err)
		}
		if contained && !strings.Contains(wall.Description, "metal compute") {
			t.Errorf("the wall does not say what it lets a Metal engine reach: %s", wall.Description)
		}
		t.Logf("contained=%v: ready in %s; ready line %.400s", contained, took.Round(time.Millisecond), line)
		if contained {
			t.Logf("wall: %s", wall.Description)
		}
	}
	start(false)
	start(true)
}
