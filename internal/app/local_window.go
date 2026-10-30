package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

var localWindowRoutes = []struct {
	path string
	read func(body []byte, models []string) map[string]int
}{
	{"/props", readLlamaCppProps},
	{"/api/v0/models", readLMStudioModels},
	{"/api/ps", readOllamaRunning},
}

func askLocalWindows(ctx context.Context, e providerEntry, token string, models []string, meta map[string]modelMeta) map[string]modelMeta {
	if !e.Local || len(models) == 0 {
		return meta
	}
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" {
		return meta
	}
	origin := u.Scheme + "://" + u.Host
	missing := func() bool {
		for _, m := range models {
			if meta[m].Context == 0 {
				return true
			}
		}
		return false
	}
	for _, route := range localWindowRoutes {
		if !missing() {
			break
		}
		body := localRoute(ctx, origin+route.path, token)
		if body == nil {
			continue
		}
		for model, n := range route.read(body, models) {
			if n <= 0 || meta[model].Context != 0 {
				continue
			}
			if meta == nil {
				meta = map[string]modelMeta{}
			}
			row := meta[model]
			row.Context = n
			meta[model] = row
		}
	}
	return meta
}

func localRoute(ctx context.Context, address, token string) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpDiscoveryClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelListBytes+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(body) > maxModelListBytes {
		return nil
	}
	return body
}

func readLlamaCppProps(body []byte, models []string) map[string]int {
	var out struct {
		Settings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if json.Unmarshal(body, &out) != nil || out.Settings.NCtx <= 0 || len(models) != 1 {
		return nil
	}
	return map[string]int{models[0]: out.Settings.NCtx}
}

func readLMStudioModels(body []byte, _ []string) map[string]int {
	var out struct {
		Data []struct {
			ID     string `json:"id"`
			Loaded int    `json:"loaded_context_length"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	got := map[string]int{}
	for _, m := range out.Data {
		if m.ID != "" && m.Loaded > 0 {
			got[m.ID] = m.Loaded
		}
	}
	return got
}

func readOllamaRunning(body []byte, _ []string) map[string]int {
	var out struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Context int    `json:"context_length"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	got := map[string]int{}
	for _, m := range out.Models {
		for _, name := range []string{m.Name, m.Model} {
			if name != "" && m.Context > 0 {
				got[name] = m.Context
			}
		}
	}
	return got
}
