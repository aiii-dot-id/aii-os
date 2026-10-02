package tools

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"io"
	"net"
	"net/http"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
	"strings"
	"time"
)

type WebFetchTool struct {
	maxBytes int
	timeout  time.Duration

	local  []firewall.LocalScope
	refuse func(net.IP, int) bool

	onFetch func(url string)
}

const ReasonHTTPStatus = "HTTP_STATUS"

func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return text content. Args: url (required)"
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "URL to fetch"},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return Refusal(ReasonArgumentsRequired, "url is required"), nil
	}
	lg := firewall.GuardAdmitting(nil, t.local, t.refuse)
	if err := lg.Guard(ctx, strings.TrimSpace(url)); err != nil {
		return Result{Error: fmt.Sprintf("web_fetch blocked: %v", err)}, nil
	}

	client := firewall.GuardedClient(t.timeout, lg.Guard, nil)
	ctx = firewall.WithPins(ctx, lg)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	req.Header.Set("User-Agent", "AII-OS/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.maxBytes)+1))
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	truncated := len(body) > t.maxBytes
	if truncated {
		body = body[:t.maxBytes]
	}
	content := string(body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		source := strings.TrimSpace(fmt.Sprintf("%s — HTTP %d %s", url, resp.StatusCode, http.StatusText(resp.StatusCode)))
		return Result{Error: untrusted.Wrap(source, content), Truncated: truncated, ReasonCode: ReasonHTTPStatus}, nil
	}

	if t.onFetch != nil {
		t.onFetch(url)
	}

	labeled := untrusted.Wrap(url, content)

	return Result{Output: labeled, Truncated: truncated}, nil
}
