package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestShardedRaceInstrumentationIsExplicit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell runner")
	}
	runner, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "0", "1", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "test"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "test", "run_race_scope.sh"), runner, 0700); err != nil {
				t.Fatal(err)
			}
			arguments := filepath.Join(root, "arguments")
			driver := filepath.Join(root, "go")
			if err := os.WriteFile(driver, []byte(`#!/bin/sh
printf '%s\n' "$@" >> "$AII_DRIVER_ARGS"
case "$1" in
  list) echo fixture/internal/app ;;
  test)
    case " $* " in
      *' -list '*) echo TestOne ;;
      *) printf '=== RUN   TestOne\n--- PASS: TestOne (0.00s)\nPASS\nok fixture/internal/app 0.001s\n' ;;
    esac ;;
  *) exit 2 ;;
esac
`), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "test/run_race_scope.sh", "app")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "AII_GO="+driver, "AII_TEST_SHARDS=1", "AII_TEST_FULL_RACE="+mode, "AII_DRIVER_ARGS="+arguments)
			out, err := cmd.CombinedOutput()
			if mode == "invalid" {
				if err == nil || !strings.Contains(string(out), "AII_TEST_FULL_RACE must be 0 or 1") {
					t.Fatalf("invalid mode was not refused: %v\n%s", err, out)
				}
				if _, err := os.Stat(arguments); !os.IsNotExist(err) {
					t.Fatalf("invalid mode dispatched a compiler: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("scope failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(arguments)
			if err != nil {
				t.Fatal(err)
			}
			args := string(raw)
			if strings.Count(args, "\n-race\n") != 2 {
				t.Fatalf("discovery and execution were not both instrumented:\n%s", args)
			}
			if mode == "1" {
				if strings.Contains(args, "-gcflags") || !strings.Contains(string(out), "race instrumentation: full (no dependency exclusions)") {
					t.Fatalf("full mode weakened instrumentation:\n%s\n%s", args, out)
				}
			} else {
				for _, exclusion := range []string{"modernc.org/sqlite/lib", "modernc.org/libc", "github.com/trailofbits/go-slh-dsa/..."} {
					if strings.Count(args, "-gcflags="+exclusion+"=-race=false\n") != 2 {
						t.Fatalf("normal mode changed its dependency instrumentation for %s:\n%s", exclusion, args)
					}
				}
			}
			if !strings.Contains(string(out), "race scope app: PASS (1 exact-once shards)") {
				t.Fatalf("instrumentation mode bypassed exact-once validation:\n%s", out)
			}
		})
	}
}
