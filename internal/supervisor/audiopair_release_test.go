//go:build linux || windows

package supervisor

import (
	"testing"
	"time"
)

// A STARTED SPAWN GIVES THE AUDIO PAIR'S CHILD ENDS BACK (T37 follow-up).
// Once the child holds its two ends, the parent's copies are closed: the
// child's death then ends the channel, and nothing accumulates per spawn.
// Twelve started and closed spawns leave no descriptor or handle behind
// (the failed-start half is TestAudioPairFailedStartLeaksNothing). The
// count is one Linux and Windows can read (openResourceCount); this
// cannot run on macOS.
func TestAStartedSpawnGivesItsAudioPairsChildEndsBack(t *testing.T) {
	before := openResourceCount(t)
	for i := 0; i < 12; i++ {
		s, err := Start(Spec{
			PluginID: "audio.released", Argv: []string{fakechildBin, "session-audio"}, SessionMode: true, AudioPair: true,
			Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 0},
		}, nil)
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		if _, _, ok := s.AudioPair(); !ok {
			_ = s.Close()
			t.Fatalf("spawn %d has no audio pair", i)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
	after := openResourceCount(t)
	if after-before > 8 {
		t.Fatalf("twelve started spawns leaked %d open resources (%d -> %d); each keeping the child's two ends would leak 24", after-before, before, after)
	}
}
