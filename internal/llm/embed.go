package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
		Data []embeddingRow `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	return vectorsInInputOrder(parsed.Data, len(inputs))
}

type embeddingRow struct {
	Index     *int      `json:"index"`
	Embedding []float32 `json:"embedding"`
}

func vectorsInInputOrder(rows []embeddingRow, n int) ([][]float32, error) {
	if len(rows) != n {
		return nil, fmt.Errorf("embeddings: %d vectors for %d inputs", len(rows), n)
	}
	indexed := 0
	for _, r := range rows {
		if r.Index != nil {
			indexed++
		}
	}
	if indexed != 0 && indexed != n {
		return nil, fmt.Errorf("embeddings: %d of %d vectors say which input they are for and the rest do not; the reply does not say which vector is whose", indexed, n)
	}
	out := make([][]float32, n)
	for i, r := range rows {
		at := i
		if r.Index != nil {
			at = *r.Index
			if at < 0 || at >= n {
				return nil, fmt.Errorf("embeddings: a vector names input %d and there are %d inputs (0 to %d)", at, n, n-1)
			}
			if out[at] != nil {
				return nil, fmt.Errorf("embeddings: two vectors name input %d; the reply does not say which vector is whose", at)
			}
		}
		if len(r.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings: vector %d is empty", at)
		}
		out[at] = r.Embedding
	}
	return out, nil
}
