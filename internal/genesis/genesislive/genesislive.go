package genesislive

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	GenesisURL   = "https://genesis.aiii.id"
	FirewallURL  = "https://firewall.aiii.id"
	BootstrapURL = "https://bootstrap.aiii.id"

	cacheTTL = time.Hour

	staleFloor = 24 * time.Hour
)

var (
	holderWait         = 60 * time.Second
	holderWaitWithCopy = 15 * time.Second

	pause = time.Sleep
)

type Artifacts struct {
	Ring0             []byte `json:"ring0"`
	RootKey           []byte `json:"root_key"`
	Ring5Bundle       []byte `json:"ring5_bundle"`
	Ring5PubkeyBundle []byte `json:"ring5_pubkey_bundle"`
	Ring5Manifest     []byte `json:"ring5_manifest"`
	BootstrapKey      []byte `json:"bootstrap_key"`
	BootstrapPacket   []byte `json:"bootstrap_packet"`
	Token             string `json:"token"`
}

var (
	once   sync.Once
	cached *Artifacts
	err    error
)

func Fetch() (*Artifacts, error) {
	once.Do(func() { cached, err = load() })
	return cached, err
}

func CachePath() string {
	if p := os.Getenv("AII_GENESIS_CACHE"); p != "" {
		return p
	}
	var places []string
	if dir, e := os.UserCacheDir(); e == nil {
		places = append(places, filepath.Join(dir, "aii-os"))
	}
	places = append(places, filepath.Join(os.TempDir(), "aii-os-"+accountTag()))
	for _, d := range places {
		if privateDir(d) {
			return filepath.Join(d, "genesis-live-v1.json")
		}
	}
	return ""
}

func load() (*Artifacts, error) {
	path := CachePath()
	if path == "" {
		return fetchAll(true)
	}
	if a := readCache(path); a != nil {
		return a, nil
	}

	older := readCacheOlderThanTTL(path)

	lock := path + ".lock"
	if !takeLock(lock) {
		limit := holderWait
		if older != nil {
			limit = holderWaitWithCopy
		}
		a, ended := waitForHolder(path, lock, limit)
		if a != nil {
			return a, nil
		}

		if ended && older != nil {
			fmt.Fprintf(os.Stderr, "genesislive: the platform did not answer the binary that asked; using the cached artifacts in %s\n", path)
			return older, nil
		}
	} else {
		defer func() { _ = os.Remove(lock) }()
	}
	if a := readCache(path); a != nil {
		return a, nil
	}

	a, e := fetchAll(older == nil)
	if e != nil {

		if b := readCache(path); b != nil {
			return b, nil
		}
		if older != nil {
			fmt.Fprintf(os.Stderr, "genesislive: %v — falling back to the cached artifacts in %s\n", e, path)
			return older, nil
		}
		return nil, e
	}
	writeCache(path, a)
	return a, nil
}

func takeLock(lock string) bool {
	if st, e := os.Stat(lock); e == nil && time.Since(st.ModTime()) > cacheTTL {
		_ = os.Remove(lock)
	}
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return false
	}
	_ = f.Close()
	return true
}

func waitForHolder(path, lock string, limit time.Duration) (a *Artifacts, ended bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if a := readCache(path); a != nil {
			return a, true
		}
		if _, e := os.Lstat(lock); os.IsNotExist(e) {

			return readCache(path), true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, false
}

func readCacheOlderThanTTL(path string) *Artifacts {
	return decodeCache(path, staleFloor)
}

func readCache(path string) *Artifacts {
	return decodeCache(path, cacheTTL)
}

func decodeCache(path string, maxAge time.Duration) *Artifacts {
	f, st, ok := openOwn(path)
	if !ok {
		return nil
	}
	defer func() { _ = f.Close() }()
	if time.Since(st.ModTime()) > maxAge {
		return nil
	}
	raw, e := io.ReadAll(io.LimitReader(f, 16<<20))
	if e != nil {
		return nil
	}
	var a Artifacts
	if json.Unmarshal(raw, &a) != nil || len(a.Ring0) == 0 || len(a.BootstrapPacket) == 0 {
		return nil
	}
	return &a
}

func writeCache(path string, a *Artifacts) {
	raw, e := json.Marshal(a)
	if e != nil {
		return
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), ".aii-genesis-*")
	if e != nil {
		return
	}
	name := tmp.Name()
	if _, e = tmp.Write(raw); e == nil {
		e = tmp.Sync()
	}
	if e != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return
	}
	if e = tmp.Close(); e != nil {
		_ = os.Remove(name)
		return
	}

	if os.Rename(name, path) != nil {
		_ = os.Remove(name)
	}
}

func fetchAll(patient bool) (*Artifacts, error) {
	a := &Artifacts{}
	ring0, token, e := get(GenesisURL+"/genesis/bundle", "", patient)
	if e != nil {
		return nil, e
	}
	a.Ring0, a.Token = ring0, token
	for _, step := range []struct {
		dst   *[]byte
		url   string
		token string
	}{
		{&a.RootKey, GenesisURL + "/genesis/pubkey", ""},
		{&a.Ring5Bundle, FirewallURL + "/v1/ring5/bundle", ""},
		{&a.Ring5PubkeyBundle, FirewallURL + "/v1/ring5/pubkey.bundle", ""},
		{&a.Ring5Manifest, FirewallURL + "/v1/ring5/manifest", ""},
		{&a.BootstrapKey, BootstrapURL + "/bootstrap/pubkey.bundle", ""},
		{&a.BootstrapPacket, BootstrapURL + "/bootstrap", a.Token},
	} {
		b, _, e := get(step.url, step.token, patient)
		if e != nil {
			return nil, e
		}
		*step.dst = b
	}
	return a, nil
}

func Get(url, token string) ([]byte, error) {
	b, _, e := get(url, token, true)
	return b, e
}

func get(url, token string, patient bool) ([]byte, string, error) {

	attempts := 6
	if !patient {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			pause(time.Duration(attempt*attempt*2) * time.Second)
		}
		body, tok, status, e := attemptGet(url, token)
		switch {
		case e != nil:
			lastErr = e
		case status == http.StatusOK:
			return body, tok, nil
		case status == http.StatusTooManyRequests || status >= 500:
			lastErr = fmt.Errorf("%s: HTTP %d", url, status)
		default:
			return nil, "", fmt.Errorf("%s: HTTP %d", url, status)
		}
	}
	return nil, "", lastErr
}

var seam = &http.Client{Timeout: 20 * time.Second, Transport: ownTransport()}

func ownTransport() http.RoundTripper {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return http.DefaultTransport
}

func attemptGet(url, token string) (body []byte, tok string, status int, e error) {
	req, e := http.NewRequest(http.MethodGet, url, nil)
	if e != nil {
		return nil, "", 0, e
	}
	if token != "" {
		req.Header.Set("X-Genesis-Token", token)
	}
	resp, e := seam.Do(req)
	if e != nil {
		return nil, "", 0, fmt.Errorf("%s: %w", url, e)
	}
	defer func() { _ = resp.Body.Close() }()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return nil, "", resp.StatusCode, e
	}
	return b, resp.Header.Get("X-Genesis-Token"), resp.StatusCode, nil
}
