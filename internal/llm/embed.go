package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func (c *Client) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if c.dialect == DialectAnthropic {
		return nil, fmt.Errorf("embeddings: the anthropic dialect serves none; name an OpenAI-compatible provider's embeddings_model")
	}
	if model == "" {
		return nil, fmt.Errorf("embeddings: no model named")
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("embeddings: no inputs")
	}
	if c.dialect == DialectGemini {
		return c.embedGemini(ctx, model, inputs)
	}
	body, err := json.Marshal(map[string]any{"model": model, "input": inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpResp, err := c.post(ctx, strings.TrimSuffix(c.endpoint, "/")+"/embeddings", body, bearer)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		refusal, err := refusalError(httpResp)
		if err != nil {
			return nil, err
		}
		return nil, refusal
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
