package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAReadPageFitsTheWindowItIsShownIn(t *testing.T) {
	var many strings.Builder
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&many, "line %d of a long file\n", i)
	}
	files := map[string]string{
		"many.txt":   many.String(),
		"long.txt":   strings.Repeat("y", 200_000) + "\nnext\n",
		"bytes.txt":  strings.Repeat("caf\xe9 au lait\n", 20000),
		"bytes1.txt": strings.Repeat("\xff", 120_000) + "\n",
	}
	for _, window := range []int{limits.DefaultToolResultChars, 4000} {
		for name, body := range files {
			t.Run(fmt.Sprintf("%s in %d", name, window), func(t *testing.T) {
				a := newVoiceApp(t)
				home := t.TempDir()
				if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				a.toolReg = tools.NewRegistry(home, nil, tools.Timeouts{})
				args, err := json.Marshal(map[string]string{"file_path": name})
				if err != nil {
					t.Fatal(err)
				}
				model := &seenScript{argsScript: argsScript{tool: "read", args: string(args)}}
				loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil,
					testLoop(conversation.Config{MaxToolResultChars: window}))
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if _, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
					t.Fatal(err)
				}
				var result string
				for _, m := range model.sent {
					if m.Role == "tool" {
						result = m.Content
					}
				}
				result = strings.TrimPrefix(result, notUTF8Note+"\n")
				if strings.HasPrefix(result, "[output truncated") || !strings.HasPrefix(result, "     1\t") {
					t.Fatalf("THE LOOP CUT THE PAGE'S HEAD: the model was not shown line 1 first: %.160q", result)
				}
				if !strings.HasSuffix(result, "]") {
					t.Fatalf("the page does not end on its closing line: …%q", result[max(0, len(result)-120):])
				}
			})
		}
	}
}
