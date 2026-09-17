package pluginhost

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestAcceleratorProfilesReadinessAndTheWindowsGate(t *testing.T) {
	good := `{"macos-arm64-native":{"os":"macos","arch":"arm64","backend":"mlx","operators":["conv1d","attention"],"runtime_libraries":["mlx-0.20","onnxruntime-1.29.0"],"precision":"int8","models":["nemotron-3.5-stt-0.6b-int8","qwen3-tts-0.6b-int8","silero-vad","smart-turn-v3.2"],"memory_bytes":6442450944,"session_limit":1,"fallback":"reported"}}`
	profiles, err := ParseAccelerators([]byte(good), []string{"macos-arm64-native", "linux-x86_64-native"})
	if err != nil || profiles["macos-arm64-native"].Backend != "mlx" || len(profiles["macos-arm64-native"].Models) != 4 {
		t.Fatalf("a good declaration parses: %v %+v", err, profiles)
	}
	for name, tc := range map[string]struct{ raw, want string }{
		"unknown variant":   {`{"windows-x86_64-native":{"os":"windows","arch":"x86_64","backend":"directml","precision":"fp16","models":["m"],"memory_bytes":1,"session_limit":1,"fallback":"none"}}`, "not a variant"},
		"no models":         {`{"macos-arm64-native":{"os":"macos","arch":"arm64","backend":"mlx","precision":"int8","models":[],"memory_bytes":1,"session_limit":1,"fallback":"none"}}`, "at least one model"},
		"silent fallback":   {`{"macos-arm64-native":{"os":"macos","arch":"arm64","backend":"mlx","precision":"int8","models":["m"],"memory_bytes":1,"session_limit":1,"fallback":"silent"}}`, "never taken silently"},
		"unmeasured memory": {`{"macos-arm64-native":{"os":"macos","arch":"arm64","backend":"mlx","precision":"int8","models":["m"],"memory_bytes":0,"session_limit":1,"fallback":"none"}}`, "measured"},
		"unknown member":    {`{"macos-arm64-native":{"os":"macos","arch":"arm64","backend":"mlx","precision":"int8","models":["m"],"memory_bytes":1,"session_limit":1,"fallback":"none","npu_only":true}}`, "unknown field"},
		"not an object":     {`[{"os":"macos"}]`, "not an object"},
	} {
		if _, err := ParseAccelerators([]byte(tc.raw), []string{"macos-arm64-native"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}

	r := ParseReadiness("child-ready event=ready models_loaded=4 accelerator=mlx probe_ms=38 extra=ignored")
	if !r.Real() || r.ModelsLoaded != 4 || r.Accelerator != "mlx" || r.ProbeMS != 38 {
		t.Fatalf("readiness: %+v", r)
	}
	for _, line := range []string{"child-ready", "event=ready", "event=ready models_loaded=0 accelerator=mlx probe_ms=1", "event=ready models_loaded=2 probe_ms=1", "event=ready models_loaded=2 accelerator=mlx"} {
		if ParseReadiness(line).Real() {
			t.Fatalf("%q is not real readiness", line)
		}
	}

	win := hostContext{platform: "windows", arch: "x86_64", topology: "full_identity_host", supervised: true}
	if ok, why := win.runtimeLane("native_t3_component"); !ok || why != "" {
		t.Fatalf("qualified Windows native T3 must reach per-activation containment and readiness: %v %q", ok, why)
	}
	if ok, _ := win.runtimeLane("wasm_component"); !ok {
		t.Fatal("wasm still runs on Windows")
	}
	lin := hostContext{platform: "linux", arch: "x86_64", topology: "full_identity_host", supervised: true}
	if ok, _ := lin.runtimeLane("native_t3_component"); !ok {
		t.Fatal("Linux keeps its native lane")
	}
}
