//go:build darwin && !ios

package tools

import (
	"strings"
	"testing"
	"time"
)

// A JOB LEFT BEHIND THE MAC'S SHELL WALL IS FOUND AND ENDED, WITH THE
// SANDBOX ON AND OFF. On macOS the shell runs inside
// a Seatbelt profile and whatever it starts runs inside it too; the host,
// outside the profile, reads the shell's process group and signals it.
// Each posture first proves the call ran behind the wall (the control: a
// kept file read by a built name is refused by the kernel, which only a
// walled shell meets), then leaves a job, reads that the result says so,
// and ends it through the Registry as SAFE and a stop do.
func TestALeftJobBehindTheMacsShellWallIsFoundAndEnded(t *testing.T) {
	for _, off := range []bool{false, true} {
		name := "sandbox on"
		if off {
			name = "sandbox off"
		}
		t.Run(name, func(t *testing.T) {
			f := newWallFixture(t)
			f.reg.SetSandboxOff(off)
			t.Cleanup(func() { f.reg.EndLeftJobs(0) })
			refusedByKernel(t, f, "control: a kept file behind the wall", `p=conf; q=ig.json; cat "$p$q"`)

			res := modelCall(t, f.reg, "shell", map[string]any{"command": `sleep 30 >/dev/null 2>&1 &`})
			if res.Refused || !strings.Contains(res.Output, shellLeftJob) {
				t.Fatalf("a job left behind the wall was not said (refused %v, %q):\n%s", res.Refused, res.Error, res.Output)
			}
			jobs := kept(f.reg.shell)
			if len(jobs) != 1 || !groupLive(jobs[0].pgid) {
				t.Fatalf("the job left behind the wall is not kept as running: %+v", jobs)
			}
			n, err := f.reg.EndLeftJobs(2 * time.Second)
			if err != nil || n != 1 {
				t.Fatalf("ending the left jobs ended %d (%v), want 1", n, err)
			}
			waitGone(t, jobs[0].pgid, 5*time.Second)
		})
	}
}
