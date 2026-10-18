package pluginhost

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestTheVoiceEventNamesAreTheKits(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "sdk-compat.rev"))
	if err != nil {
		t.Fatal(err)
	}
	rev := strings.TrimSpace(string(raw))
	sdk := os.Getenv("AII_SDK_DIR")
	if sdk == "" {
		sdk, _ = filepath.Abs(filepath.Join("..", "..", "..", "aii-plugin-sdk"))
	}
	if err := exec.Command("git", "-C", sdk, "cat-file", "-e", rev+"^{commit}").Run(); err != nil {
		t.Skipf("no SDK checkout holding the pinned commit %s at %s (set AII_SDK_DIR): %v", rev, sdk, err)
	}
	src, err := exec.Command("git", "-C", sdk, "show", rev+":examples/voice-skel/main.go").Output()
	if err != nil {
		t.Fatalf("the pinned kit has no voice skeleton at examples/voice-skel/main.go: %v", err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "voice-skel/main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Every name the skeleton publishes goes through its one emit(typ, fields).
	emitted := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "emit" {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if name, err := strconv.Unquote(lit.Value); err == nil {
				emitted[name] = true
			}
		}
		return true
	})

	declared := map[string]bool{}
	for _, name := range []string{EventSessionReady, EventSessionEnd, EventFailure, EventCancellation,
		EventVADProbability, EventSpeechStart, EventTranscriptPartial, EventTranscriptFinal,
		EventSpeakerObservation, EventInputFinished, EventSynthesisStart, EventSynthesisEnd,
		EventSynthesisCancelled, EventInterruptionRequested} {
		declared[name] = true
	}
	// Playback evidence the page shows and no host code branches on.
	unread := map[string]bool{"playback_end": true, "playback_stop": true, "playback_observation": true}
	for name := range emitted {
		if !declared[name] && !unread[name] {
			t.Errorf("the kit's voice skeleton emits %q, which the host neither declares nor passes on knowingly", name)
		}
	}
	// The names both sides use: each host constant is the kit's string.
	for _, name := range []string{EventSessionReady, EventSessionEnd, EventCancellation, EventVADProbability,
		EventTranscriptFinal, EventInputFinished, EventSynthesisStart, EventSynthesisEnd, EventSynthesisCancelled} {
		if !emitted[name] {
			t.Errorf("the host reads %q, and the kit's voice skeleton at %s emits no event by that name", name, rev[:12])
		}
	}
	names := make([]string, 0, len(emitted))
	for name := range emitted {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("the kit's voice skeleton at %s emits %v", rev[:12], names)
}
