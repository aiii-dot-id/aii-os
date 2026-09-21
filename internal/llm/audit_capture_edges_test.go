package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const auditEdgeCaptureLimit = 8 << 20
const auditEdgeWholeAnswer = `{"choices":[{"message":{"role":"assistant","content":"AUDIT-TAIL"},"finish_reason":"stop"}]}`
const auditEdgeStreamAnswer = "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"AUDIT-TAIL\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestAuditPromptCaptureDeclaresTruncation(t *testing.T) {
	// .
	// .
	question := strings.Repeat("<", 2<<20)
	var wireBytes atomic.Int64
	var fullQuestion atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		wireBytes.Store(int64(len(b)))
		var request struct {
			Messages []Message `json:"messages"`
		}
		if err := json.Unmarshal(b, &request); err != nil {
			t.Error(err)
		}
		fullQuestion.Store(len(request.Messages) == 1 && request.Messages[0].Content == question)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, auditEdgeWholeAnswer)
	}))
	defer srv.Close()
	dir := tapDir(t)
	c := New(&ClientConfig{Endpoint: srv.URL, Model: "synthetic", NoStream: true, MaxInputTokens: 10000000})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: question}}, ChatOptions{})
	if err != nil || resp == nil || resp.Choices[0].Message.Content != "AUDIT-TAIL" {
		t.Fatalf("request failed: %v", err)
	}
	if !fullQuestion.Load() || wireBytes.Load() <= auditEdgeCaptureLimit {
		t.Fatalf("fixture: complete large request not received: %d", wireBytes.Load())
	}
	var prompts int
	for _, row := range capturedLines(t, dir) {
		if row["direction"] != "prompt" {
			continue
		}
		prompts++
		payload := row["payload"].(string)
		t.Logf("request transmitted=%d bytes, capture=%d bytes", wireBytes.Load(), len(payload))
		if len(payload) > auditEdgeCaptureLimit+256 {
			t.Errorf("unbounded prompt capture: %d", len(payload))
		}
		if !strings.Contains(payload, "capture truncated") {
			t.Errorf("shortened request capture omitted its truncation notice: wire=%d capture=%d", wireBytes.Load(), len(payload))
		}
		if row["id"] != resp.CallID {
			t.Error("prompt correlation lost")
		}
	}
	if prompts != 1 {
		t.Fatalf("prompt captures=%d", prompts)
	}
}

type auditCaptureCredential struct{ advances atomic.Int32 }

func (*auditCaptureCredential) Credential(context.Context) (Credential, error) {
	return Credential{Token: "synthetic-token", Gen: 1}, nil
}
func (c *auditCaptureCredential) Stale(context.Context, uint64) error { c.advances.Add(1); return nil }

func TestAuditCredentialReplayCapturesSuccessfulReply(t *testing.T) {
	for _, mode := range []string{"whole", "stream", "anthropic"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if requests.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					io.WriteString(w, `{"error":{"message":"SYNTHETIC-REJECT"}}`)
					return
				}
				if mode == "stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, auditEdgeStreamAnswer)
				} else {
					w.Header().Set("Content-Type", "application/json")
					if mode == "anthropic" {
						io.WriteString(w, `{"content":[{"type":"text","text":"AUDIT-TAIL"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
					} else {
						io.WriteString(w, auditEdgeWholeAnswer)
					}
				}
			}))
			defer srv.Close()
			dir := tapDir(t)
			credential := &auditCaptureCredential{}
			cfg := &ClientConfig{Endpoint: srv.URL, Model: "synthetic", NoStream: mode == "whole", Credential: credential, MaxOutputTokens: 256}
			if mode == "anthropic" {
				cfg.Provider = "anthropic"
			}
			resp, err := New(cfg).Chat(context.Background(), []Message{{Role: "user", Content: "synthetic question"}}, ChatOptions{})
			if err != nil || resp == nil || resp.Choices[0].Message.Content != "AUDIT-TAIL" {
				t.Fatalf("replay did not succeed: %v", err)
			}
			if requests.Load() != 2 || credential.advances.Load() != 1 {
				t.Fatalf("requests=%d credential advances=%d", requests.Load(), credential.advances.Load())
			}
			var prompts, returns, answers int
			for _, row := range capturedLines(t, dir) {
				if row["id"] != resp.CallID {
					t.Error("capture correlation lost")
				}
				if row["direction"] == "prompt" {
					prompts++
				}
				if row["direction"] == "return" {
					returns++
					if strings.Contains(row["payload"].(string), "AUDIT-TAIL") {
						answers++
					}
				}
			}
			if prompts != 2 || returns != 2 || answers != 1 {
				t.Fatalf("successful credential replay was not fully captured: prompts=%d returns=%d final answers=%d", prompts, returns, answers)
			}
		})
	}
}
