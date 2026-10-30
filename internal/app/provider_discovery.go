package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

var (
	errAuthRequired = errors.New("this provider requires an API key to list models")

	errCredentialUnavailable = errors.New("provider credential unavailable")

	errCredentialRejected = errors.New("the provider refused the credential it was shown")
)

const maxModelListBytes = 1 << 20

const geminiModelPageSize = "1000"

func discoverModelsWith(ctx context.Context, dialect, base, token string, bearer bool, extra, query map[string]string) ([]string, map[string]modelMeta, error) {
	base = strings.TrimSuffix(base, "/")

	path := base + "/v1/models"
	if apiVersionInPath(base) {
		path = base + "/models"
	}
	if dialect == "chatgpt" {
		path = base + "/models"
	}
	if dialect == "gemini" {

		query = maps.Clone(query)
		if query == nil {
			query = map[string]string{}
		}
		if _, set := query["pageSize"]; !set {
			query["pageSize"] = geminiModelPageSize
		}
	}

	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		path += "?" + q.Encode()
	}
	return listModelsAt(ctx, dialect, path, "", token, bearer, extra)
}

func listModelsAt(ctx context.Context, dialect, path, show, token string, bearer bool, extra map[string]string) ([]string, map[string]modelMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	anthropic, gemini := dialect == "anthropic", dialect == "gemini"
	method, ask := http.MethodGet, io.Reader(nil)
	if show != "" {
		asked, err := json.Marshal(map[string]string{"model": show})
		if err != nil {
			return nil, nil, err
		}
		method, ask = http.MethodPost, bytes.NewReader(asked)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, ask)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		switch {
		case anthropic && !bearer:
			req.Header.Set("x-api-key", token)
		case gemini && !bearer:
			req.Header.Set("x-goog-api-key", token)
		default:
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelListBytes+1))
	if err != nil {
		return nil, nil, err
	}

	refused := &llm.HTTPError{StatusCode: resp.StatusCode, Body: string(body[:min(len(body), maxModelListBytes)])}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || (resp.StatusCode == http.StatusBadRequest && refused.AuthFailure()) {

		if token == "" {
			return nil, nil, errAuthRequired
		}
		return nil, nil, fmt.Errorf("%w: key rejected (%d) — check the credential for this provider", errCredentialRejected, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("provider returned %d listing models at %s", resp.StatusCode, path)
	}
	if len(body) > maxModelListBytes {
		return nil, nil, fmt.Errorf("provider model list exceeds %d bytes", maxModelListBytes)
	}
	if show != "" {
		return parseShow(show, body)
	}
	return parseModelList(body)
}

func parseShow(model string, body []byte) ([]string, map[string]modelMeta, error) {
	var out struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("provider model description is not JSON: %w", err)
	}
	if len(out.Capabilities) == 0 {
		return []string{model}, nil, nil
	}
	has := func(c string) bool { return slices.Contains(out.Capabilities, c) }
	return []string{model}, map[string]modelMeta{model: {
		Input:  map[string]bool{"image": has("vision"), "audio": has("audio")},
		Output: map[string]bool{"text": has("completion"), "image": has("image")},
		Tools:  new(has("tools")),
	}}, nil
}

func capabilityListAt(list, model string) string {
	return strings.ReplaceAll(list, "{model}", url.QueryEscape(model))
}

func showsOneModel(list string) bool {
	u, err := url.Parse(list)
	return err == nil && strings.HasSuffix(u.Path, "/api/show")
}

func parseModelList(body []byte) ([]string, map[string]modelMeta, error) {
	type supported struct {
		Supported *bool `json:"supported"`
	}
	var out struct {
		Data []struct {
			ID              string `json:"id"`
			CanonicalID     string `json:"canonical_id"`
			CanonicalSlug   string `json:"canonical_slug"`
			MaxInput        int    `json:"max_input_tokens"`
			MaxTokens       int    `json:"max_tokens"`
			MaxOutputTokens int    `json:"max_output_tokens"`
			ContextLength   int    `json:"context_length"`
			MaxModelLen     int    `json:"max_model_len"`
			ContextWindow   int    `json:"context_window"`
			Architecture    struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			SupportedParameters []string `json:"supported_parameters"`
			InputModalities     []string `json:"input_modalities"`
			OutputModalities    []string `json:"output_modalities"`
			SupportsImageIn     *bool    `json:"supports_image_in"`
			SupportsVideoIn     *bool    `json:"supports_video_in"`
			Capabilities        struct {
				ImageInput      supported `json:"image_input"`
				PDFInput        supported `json:"pdf_input"`
				Vision          *bool     `json:"vision"`
				FunctionCalling *bool     `json:"function_calling"`
			} `json:"capabilities"`
		} `json:"data"`
		Models []struct {
			Slug          string `json:"slug"`
			Name          string `json:"name"`
			ID            string `json:"id"`
			Key           string `json:"key"`
			ContextWindow int    `json:"context_window"`
			MaxTokens     int    `json:"max_tokens"`

			InputModalities []string `json:"input_modalities"`

			InputTokenLimit  int      `json:"inputTokenLimit"`
			OutputTokenLimit int      `json:"outputTokenLimit"`
			Methods          []string `json:"supportedGenerationMethods"`

			MaxContextLength int `json:"max_context_length"`
			Capabilities     struct {
				Vision            *bool `json:"vision"`
				TrainedForToolUse *bool `json:"trained_for_tool_use"`
			} `json:"capabilities"`
		} `json:"models"`

		Output struct {
			Models []struct {
				Model             string   `json:"model"`
				Features          []string `json:"features"`
				InferenceMetadata struct {
					RequestModality  []string `json:"request_modality"`
					ResponseModality []string `json:"response_modality"`
				} `json:"inference_metadata"`
				ModelInfo struct {
					ContextWindow   int `json:"context_window"`
					MaxOutputTokens int `json:"max_output_tokens"`
				} `json:"model_info"`
			} `json:"models"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("provider model list is not JSON: %w", err)
	}
	models := make([]string, 0, len(out.Data)+len(out.Models)+len(out.Output.Models))
	meta := map[string]modelMeta{}
	own := make(map[string]bool, len(out.Data))
	keep := func(id string, row modelMeta) {
		if row.Context > 0 || row.MaxOut > 0 || row.answers() {
			meta[id] = row
		}
	}
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, m.ID)
		own[m.ID] = true
		ctx := m.MaxInput
		if ctx == 0 {
			ctx = m.ContextLength
		}
		if ctx == 0 {

			ctx = m.MaxModelLen
		}
		if ctx == 0 {

			ctx = m.ContextWindow
		}
		row := modelMeta{Context: ctx, MaxOut: cmp.Or(m.MaxTokens, m.MaxOutputTokens)}
		row.Input = kindsFrom(slices.Concat(m.Architecture.InputModalities, m.InputModalities), inputKinds)
		row.Output = kindsFrom(slices.Concat(m.Architecture.OutputModalities, m.OutputModalities), outputKinds)
		if row.Input == nil {
			c := m.Capabilities
			row.Input = saidKinds(map[string]*bool{
				"image": cmp.Or(c.ImageInput.Supported, c.Vision, m.SupportsImageIn),
				"pdf":   c.PDFInput.Supported,
				"video": m.SupportsVideoIn,
			})
		}
		row.Tools = m.Capabilities.FunctionCalling
		if len(m.SupportedParameters) > 0 {
			row.Tools = new(slices.Contains(m.SupportedParameters, "tools"))
		}
		keep(m.ID, row)
	}

	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		known := meta[m.ID]
		for _, name := range []string{m.CanonicalID, m.CanonicalSlug} {
			if name == "" || name == m.ID || own[name] {
				continue
			}
			if _, taken := meta[name]; !taken {
				meta[name] = known
			}
		}
	}
	for _, m := range out.Models {
		id := cmp.Or(m.Slug, m.ID, m.Key, strings.TrimPrefix(m.Name, "models/"))
		if id == "" || (len(m.Methods) > 0 && !slices.Contains(m.Methods, "generateContent")) {
			continue
		}
		models = append(models, id)
		row := modelMeta{
			Context: max(m.ContextWindow, m.InputTokenLimit, m.MaxContextLength),
			MaxOut:  max(m.MaxTokens, m.OutputTokenLimit),
			Input:   kindsFrom(m.InputModalities, inputKinds),
			Tools:   m.Capabilities.TrainedForToolUse,
		}
		if row.Input == nil {
			row.Input = saidKinds(map[string]*bool{"image": m.Capabilities.Vision})
		}
		keep(id, row)
	}
	for _, m := range out.Output.Models {
		if m.Model == "" {
			continue
		}
		models = append(models, m.Model)
		row := modelMeta{
			Context: m.ModelInfo.ContextWindow, MaxOut: m.ModelInfo.MaxOutputTokens,
			Input:  kindsFrom(m.InferenceMetadata.RequestModality, inputKinds),
			Output: kindsFrom(m.InferenceMetadata.ResponseModality, outputKinds),
		}
		if m.Features != nil {
			row.Tools = new(slices.Contains(m.Features, "function-calling"))
		}
		keep(m.Model, row)
	}
	return models, meta, nil
}

func kindsFrom(list, vocabulary []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	kinds := make(map[string]bool, len(vocabulary))
	for _, k := range vocabulary {
		kinds[k] = false
	}
	for _, said := range list {
		said = strings.ToLower(said)
		if said == "file" {
			said = "pdf"
		}
		if _, asked := kinds[said]; asked {
			kinds[said] = true
		}
	}
	return kinds
}

func saidKinds(fields map[string]*bool) map[string]bool {
	var kinds map[string]bool
	for kind, v := range fields {
		if v == nil {
			continue
		}
		if kinds == nil {
			kinds = map[string]bool{}
		}
		kinds[kind] = *v
	}
	return kinds
}

type modelMeta struct {
	Context       int
	MaxOut        int
	Input, Output map[string]bool
	Tools         *bool
}

func (m modelMeta) answers() bool {
	return len(m.Input) > 0 || len(m.Output) > 0 || m.Tools != nil
}

var httpDiscoveryClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func (a *App) discoverForEntry(ctx context.Context, e providerEntry, apiKey string) ([]string, error) {
	models, _, err := a.discoverMetaForEntry(ctx, e, apiKey)
	return models, err
}

func (a *App) llmBlockEnvFor(e providerEntry) string {
	if a.cfg == nil {
		return ""
	}
	block := a.configSnapshot().LLM
	return keyEnvFor(block.Provider, block.APIKeyEnv, e)
}

func (a *App) discoverMetaForEntry(ctx context.Context, e providerEntry, apiKey string) ([]string, map[string]modelMeta, error) {
	models, meta, err := a.discoverMetaAt(ctx, e, apiKey, "", "")
	if err == nil && e.Credential == "" {
		meta = askLocalWindows(ctx, e, providerAPIKey(e, apiKey, a.llmBlockEnvFor(e)), models, meta)
	}
	return models, meta, err
}

func (a *App) discoverMetaAt(ctx context.Context, e providerEntry, apiKey, list, model string) ([]string, map[string]modelMeta, error) {
	at, show := capabilityListAt(list, model), ""
	if list != "" && showsOneModel(list) {
		show = model
	}
	if at != "" && !sameOrigin(at, e.URL) {
		return nil, nil, fmt.Errorf("the capability list %s is not at the address of provider %q, %s", at, e.Name, e.URL)
	}
	apiKey = providerAPIKey(e, apiKey, a.llmBlockEnvFor(e))
	dialect, base, bearer := e.APIType, e.URL, false
	var extra, query map[string]string
	if e.Credential != "" {

		if err := credentialAddressRefusal(e); err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, err)
		}
		src, err := a.credentialSource(e.Credential, e.CredentialOptions, e.signIn)
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
		if at != "" && !sameOrigin(at, base) {
			return nil, nil, fmt.Errorf("%w: the capability list %s is not at %s, where this credential is sent", errCredentialUnavailable, at, base)
		}
		list := func(token string, headers map[string]string) ([]string, map[string]modelMeta, error) {
			if at != "" {
				return listModelsAt(ctx, dialect, at, show, token, bearer, headers)
			}
			return discoverModelsWith(ctx, dialect, base, token, bearer, headers, query)
		}
		gen := cr.Gen
		models, meta, err := list(apiKey, extra)
		if errors.Is(err, errCredentialRejected) {

			if serr := src.Stale(ctx, gen); serr != nil {
				return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, serr)
			}
			fresh, ferr := src.Credential(ctx)
			if ferr != nil {
				return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, ferr)
			}
			return list(fresh.Token, fresh.Headers)
		}
		return models, meta, err
	}
	if at != "" {
		return listModelsAt(ctx, dialect, at, show, apiKey, bearer, extra)
	}
	return discoverModelsWith(ctx, dialect, base, apiKey, bearer, extra, query)
}

func (a *App) discoverForProvider(ctx context.Context, reg *providerRegistry, name, baseURL, apiKey string) ([]string, error) {
	for _, p := range reg.Providers {
		if p.Name != name {
			continue
		}
		if baseURL != "" {
			if !p.Local {
				return nil, fmt.Errorf("provider %q is not a local entry: only a local entry is asked at an address typed on the birth form", name)
			}
			if !validProviderURL(baseURL) {
				return nil, fmt.Errorf("%q is not a valid provider URL (http(s)://host[:port][/path])", baseURL)
			}
			if !sameOrigin(p.URL, baseURL) {
				models, _, err := discoverModelsWith(ctx, p.APIType, baseURL, apiKey, false, nil, nil)
				return models, err
			}
			p.URL = baseURL
			return a.discoverForEntry(ctx, p, apiKey)
		}
		models, meta, err := a.discoverMetaForEntry(ctx, p, apiKey)
		if err == nil {

			a.providers.runtime.mu.Lock()
			if a.providers.runtime.provStatus == nil {
				a.providers.runtime.provStatus = make(map[string]providerProbe)
			}
			a.providers.runtime.provStatus[p.Name] = retainNativeCapabilities(
				providerProbe{state: probeOK, models: models, meta: meta, checkedAt: time.Now(), key: probeKey(p)},
				a.providers.runtime.provStatus[p.Name])
			a.providers.runtime.mu.Unlock()
			return mergeDistinct(p.Models, models), nil
		}
		if len(p.Models) > 0 {
			return mergeDistinct(p.Models, nil), nil
		}
		return nil, err
	}
	return nil, fmt.Errorf("unknown provider %q", name)
}

func modelOffered(models []string, meta map[string]modelMeta, model string) bool {
	for _, m := range models {
		if m == model {
			return true
		}
	}
	_, ok := meta[model]
	return ok
}

func mergeDistinct(lists ...[]string) []string {
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
