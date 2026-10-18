//go:build !windows

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAShellCallsExitStatusReachesItsRecord(t *testing.T) {
	for _, c := range []struct {
		name, command string
		ceiling       int
		want          sql.NullInt64
		endsWith      string
	}{
		{"a clean exit", "true", 0, sql.NullInt64{Int64: 0, Valid: true}, ""},
		{"a non-zero exit", "echo begun; exit 3", 0, sql.NullInt64{Int64: 3, Valid: true}, "[exit status 3]"},
		{"a non-zero exit after more output than the record keeps", "seq 1 2000; exit 3", 0, sql.NullInt64{Int64: 3, Valid: true}, "[exit status 3]"},
		{"ended by the ceiling: no status", "echo begun; sleep 30", 1, sql.NullInt64{}, "partial]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{ShellSeconds: c.ceiling})
			args, err := json.Marshal(map[string]string{"command": c.command})
			if err != nil {
				t.Fatal(err)
			}
			loop := conversation.New(&argsScript{tool: "shell", args: string(args)}, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, conversation.Config{})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
				t.Fatal(err)
			}
			var status sql.NullInt64
			var content string
			if err := a.store.DB().QueryRow(`SELECT json_extract(details, '$.exit_status'), content FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')='shell'`).Scan(&status, &content); err != nil {
				t.Fatalf("the shell's result: %v", err)
			}
			if status != c.want {
				t.Fatalf("recorded exit status %+v, want %+v", status, c.want)
			}
			if !strings.HasSuffix(content, c.endsWith) {
				t.Fatalf("the record's text ends …%q, want it to end %q", content[max(0, len(content)-90):], c.endsWith)
			}
		})
	}
}
