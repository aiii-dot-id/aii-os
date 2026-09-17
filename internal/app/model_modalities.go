package app

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
var modalityGuard = tools.FetchGuard

const (
	// .
	modalityMaxBytes = 256 << 10
	modalityTimeout  = 8 * time.Second
)

// .
// .
// .
var reModalitySlug = regexp.MustCompile(`^[A-Za-z0-9._~-]+/[A-Za-z0-9._~:-]+$`)

// .
// .
// .
// .
// .
type modelModalities struct {
	in  []string
	out []string
}

func (m modelModalities) known() bool { return len(m.in) > 0 || len(m.out) > 0 }

// .
// .
// .
// .
// .
// .
// .
func modalitySlug(entry providerEntry, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	slug := model
	if !strings.Contains(model, "/") {
		if entry.CatalogueAuthor == "" {
			return ""
		}
		slug = entry.CatalogueAuthor + "/" + model
	}
	if !reModalitySlug.MatchString(slug) {
		return ""
	}
	return slug
}

// .
// .
// .
// .
// .
// .
// .
// .
type modalityMemo struct {
	mu sync.Mutex
	by map[string]modalityMemoEntry
}

type modalityMemoEntry struct {
	mods modelModalities
	at   time.Time
}

const (
	modalityMemoTTL = 6 * time.Hour
	// .
	// .
	// .
	// .
	modalityMemoMax = 128
)

func (m *modalityMemo) get(slug string) (modelModalities, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.by[slug]
	if !ok || time.Since(e.at) > modalityMemoTTL {
		return modelModalities{}, false
	}
	return e.mods, true
}

func (m *modalityMemo) put(slug string, mods modelModalities) {
	if !mods.known() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.by == nil || len(m.by) >= modalityMemoMax {
		m.by = make(map[string]modalityMemoEntry, 8)
	}
	m.by[slug] = modalityMemoEntry{mods: mods, at: time.Now()}
}

// .
func modelModalitiesURL(entry providerEntry, model, baseURL string) string {
	slug := modalitySlug(entry, model)
	if slug == "" || baseURL == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + slug + "/endpoints"
}

// .
// .
func (a *App) fetchModelModalities(ctx context.Context, url string, guard func(context.Context, string) error) modelModalities {
	if url == "" {
		return modelModalities{}
	}
	if mods, ok := a.modalities.get(url); ok {
		return mods
	}
	mods := lookupModalities(ctx, url, guard)
	a.modalities.put(url, mods)
	return mods
}

func lookupModalities(ctx context.Context, url string, guard func(context.Context, string) error) modelModalities {
	if err := guard(ctx, url); err != nil {
		return modelModalities{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return modelModalities{}
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (model capability)")
	resp, err := tools.GuardedClient(modalityTimeout, guard, nil).Do(req)
	if err != nil {
		return modelModalities{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelModalities{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modalityMaxBytes+1))
	if err != nil || len(body) > modalityMaxBytes {
		return modelModalities{}
	}
	var out struct {
		Data struct {
			Architecture struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return modelModalities{}
	}
	return modelModalities{
		in:  out.Data.Architecture.InputModalities,
		out: out.Data.Architecture.OutputModalities,
	}
}
