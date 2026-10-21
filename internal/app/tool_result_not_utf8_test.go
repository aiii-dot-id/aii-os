package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// notUTF8Command is one shell command whose output is not UTF-8, in the
// shell of the system the test runs on (notUTF8Commands, a file a system).
// encodingIsTheShells marks a command that writes text and leaves the
// bytes to the shell: Windows PowerShell 5.1 writes it in the console's
// code page, PowerShell 7 as UTF-8, so the result holds either the
// replacement character or the letter, and the turn finishes either way.
type notUTF8Command struct {
	name, command       string
	encodingIsTheShells bool
}

func TestAToolResultThatIsNotUTF8IsRecordedAndSaysSo(t *testing.T) {
	for _, c := range notUTF8Commands {
		t.Run(c.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			a := newVoiceApp(t)
			a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
			args, err := json.Marshal(map[string]string{"command": c.command})
			if err != nil {
				t.Fatal(err)
			}
			model := &argsScript{tool: "shell", args: string(args)}
			loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if _, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
				t.Fatalf("THE TURN ENDED ON ITS OWN RECORD: %v", err)
			}
			var content string
			if err := a.store.DB().QueryRow(`SELECT content FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')='shell'`).Scan(&content); err != nil {
				t.Fatalf("the shell's result: %v", err)
			}
			shown := content[:min(len(content), 160)]
			if !utf8.ValidString(content) {
				t.Fatalf("the record holds a result that is not UTF-8: %q", shown)
			}
			switch replaced := strings.Contains(content, "�"); {
			case c.encodingIsTheShells && replaced:
				t.Logf("this shell wrote the letter in a code page that is not UTF-8, the case the laptop met: %q", shown)
			case c.encodingIsTheShells && strings.Contains(content, "é"):
				t.Logf("this shell wrote the letter as UTF-8, so nothing was replaced: %q", shown)
			case c.encodingIsTheShells:
				t.Fatalf("the result holds neither the letter nor the mark of its bytes: %q", shown)
			case !replaced:
				t.Fatalf("the record does not show where the bytes were: %q", shown)
			}
			if !logs.Contains("Tool result: ") {
				t.Fatalf("the host logged no tool result, so its line was not read: %q", logs.String())
			}
			for _, line := range logs.Lines() {
				if i := strings.IndexFunc(line, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }); i >= 0 {
					t.Fatalf("the host's log line carries the control character %U from the command's output: %q", []rune(line[i:])[0], line)
				}
			}
		})
	}
	if got := observedText("caf\xe9"); !strings.HasPrefix(got, "caf�\n") || !strings.HasSuffix(got, notUTF8Note) {
		t.Fatalf("a result whose bytes were replaced does not say so: %q", got)
	}
	if got := observedText("café"); got != "café" {
		t.Fatalf("a UTF-8 result was changed: %q", got)
	}
}
