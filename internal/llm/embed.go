package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// .
// .
// .
// .
// .
func (c *Client) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if c.provider == "anthropic" {
		return nil, fmt.Errorf("embeddings: the anthropic dialect serves none; name an OpenAI-compatible provider's embeddings_model")
	}
	if model == "" {
		return nil, fmt.Errorf("embeddings: no model named")
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("embeddings: no inputs")
	}
	body, err := json.Marshal(map[string]interface{}{"model": model, "input": inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpResp, err := c.sendAuthed(ctx, func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(c.endpoint, "/")+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		cred, err := c.credential(ctx)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		applyAuth(httpReq, cred)
		return httpReq, nil
	})
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		respBody, _, _ := readBounded(httpResp.Body, 64<<10)
		return nil, fmt.Errorf("API returned %d: %s", httpResp.StatusCode, string(respBody))
	}
	respBody, truncated, err := readBounded(httpResp.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(parsed.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings: %d vectors for %d inputs", len(parsed.Data), len(inputs))
	}
	sort.SliceStable(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings: vector %d is empty", i)
		}
		out[i] = d.Embedding
	}
	return out, nil
}
