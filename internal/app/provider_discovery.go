// .
// .
// .
// .
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

var (
	// .
	// .
	errAuthRequired = errors.New("this provider requires an API key to list models")
	// .
	// .
	errCredentialUnavailable = errors.New("provider credential unavailable")
)

const maxModelListBytes = 1 << 20

// .
// .
// .
// .
// .
// .
// .
// .
// .
func discoverModels(ctx context.Context, url, apiKey string) ([]string, error) {
	models, _, err := discoverModelsWith(ctx, "", url, apiKey, false, nil, nil)
	return models, err
}

func discoverModelsWith(ctx context.Context, dialect, base, token string, bearer bool, extra, query map[string]string) ([]string, map[string]modelMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	base = strings.TrimSuffix(base, "/")
	// .
	// .
	// .
	// .
	// .
	path := base + "/v1/models"
	if apiVersionInPath(base) {
		path = base + "/models"
	}
	if dialect == "chatgpt" {
		path = base + "/models"
	}
	anthropic := dialect == "anthropic"

	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		path += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		if anthropic && !bearer {
			req.Header.Set("x-api-key", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if anthropic {
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", llm.DefaultAnthropicVersion)
		}
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}

	resp, err := httpDiscoveryClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// .
		// .
		if token == "" {
			return nil, nil, errAuthRequired
		}
		return nil, nil, fmt.Errorf("key rejected (%d) — check the credential for this provider", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("provider returned %d listing models at %s", resp.StatusCode, path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelListBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxModelListBytes {
		return nil, nil, fmt.Errorf("provider model list exceeds %d bytes", maxModelListBytes)
	}
	return parseModelList(body)
}

// .
// .
// .
// .
// .
// .
func parseModelList(body []byte) ([]string, map[string]modelMeta, error) {
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			MaxInput      int    `json:"max_input_tokens"`
			MaxTokens     int    `json:"max_tokens"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
		Models []struct {
			Slug          string `json:"slug"`
			Name          string `json:"name"`
			ID            string `json:"id"`
			ContextWindow int    `json:"context_window"`
			MaxTokens     int    `json:"max_tokens"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("provider model list is not JSON: %w", err)
	}
	models := make([]string, 0, len(out.Data)+len(out.Models))
	meta := map[string]modelMeta{}
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, m.ID)
		ctx := m.MaxInput
		if ctx == 0 {
			ctx = m.ContextLength
		}
		if ctx > 0 || m.MaxTokens > 0 {
			meta[m.ID] = modelMeta{Context: ctx, MaxOut: m.MaxTokens}
		}
	}
	for _, m := range out.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" {
			id = m.Name
		}
		if id == "" {
			continue
		}
		models = append(models, id)
		if m.ContextWindow > 0 || m.MaxTokens > 0 {
			meta[id] = modelMeta{Context: m.ContextWindow, MaxOut: m.MaxTokens}
		}
	}
	return models, meta, nil
}

// .
// .
// .
// .
type modelMeta struct {
	Context int
	MaxOut  int
}

// .
// .
var httpDiscoveryClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// .
// .
func (a *App) discoverForEntry(ctx context.Context, e providerEntry, apiKey string) ([]string, error) {
	models, _, err := a.discoverMetaForEntry(ctx, e, apiKey)
	return models, err
}

func (a *App) discoverMetaForEntry(ctx context.Context, e providerEntry, apiKey string) ([]string, map[string]modelMeta, error) {
	fallbackEnv := ""
	if a.cfg != nil {
		fallbackEnv = a.configSnapshot().LLM.APIKeyEnv
	}
	apiKey = providerAPIKey(e, apiKey, fallbackEnv)
	dialect, base, bearer := e.APIType, e.URL, false
	var extra, query map[string]string
	if e.Credential != "" {
		src, err := a.credentialSource(e.Credential, e.CredentialOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, err)
		}
		cr, err := src.Credential(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, err)
		}
		apiKey, bearer, extra, query = cr.Token, true, cr.Headers, src.DiscoveryQuery()
		if b := src.BaseURL(); b != "" {
			base = b
		}
		if d := src.Dialect(); d != "" {
			dialect = d
		}
	}
	return discoverModelsWith(ctx, dialect, base, apiKey, bearer, extra, query)
}

// .
// .
// .
func (a *App) discoverForProvider(ctx context.Context, reg *providerRegistry, name, apiKey string) ([]string, error) {
	for _, p := range reg.Providers {
		if p.Name == name {
			models, meta, err := a.discoverMetaForEntry(ctx, p, apiKey)
			if err == nil {
				// .
				// .
				a.provMu.Lock()
				if a.provStatus == nil {
					a.provStatus = make(map[string]providerProbe)
				}
				a.provStatus[p.Name] = providerProbe{state: "ok", models: models, meta: meta, checkedAt: time.Now(), key: probeKey(p)}
				a.provMu.Unlock()
				return mergeModels(p.Models, models), nil
			}
			if len(p.Models) > 0 {
				return mergeModels(p.Models, nil), nil
			}
			return nil, err
		}
	}
	return nil, fmt.Errorf("unknown provider %q", name)
}

func mergeModels(lists ...[]string) []string {
	seen := make(map[string]bool)
	var models []string
	for _, list := range lists {
		for _, model := range list {
			if model != "" && !seen[model] {
				models = append(models, model)
				seen[model] = true
			}
		}
	}
	return models
}
