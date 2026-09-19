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

	// .
	// .
	// .
	// .
	// .
	staleFloor = 24 * time.Hour
)

// .
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

// .
// .
func Fetch() (*Artifacts, error) {
	once.Do(func() { cached, err = load() })
	return cached, err
}

// .
// .
// .
// .
// .
func CachePath() string {
	if p := os.Getenv("AII_GENESIS_CACHE"); p != "" {
		return p
	}
	if dir, e := os.UserCacheDir(); e == nil {
		d := filepath.Join(dir, "aii-os")
		if os.MkdirAll(d, 0o755) == nil {
			return filepath.Join(d, "genesis-live-v1.json")
		}
	}
	return filepath.Join(os.TempDir(), "aii-genesis-live-v1.json")
}

func load() (*Artifacts, error) {
	path := CachePath()
	if a := readCache(path); a != nil {
		return a, nil
	}
	// .
	// .
	// .
	lock := path + ".lock"
	if !takeLock(lock) {
		if a := waitForCache(path, 60*time.Second); a != nil {
			return a, nil
		}
	} else {
		defer func() { _ = os.Remove(lock) }()
	}
	if a := readCache(path); a != nil {
		return a, nil
	}
	a, e := fetchAll()
	if e != nil {
		// .
		// .
		if b := readCache(path); b != nil {
			return b, nil
		}
		if b := readCacheOlderThanTTL(path); b != nil {
			fmt.Fprintf(os.Stderr, "genesislive: %v — falling back to the cached artifacts in %s\n", e, path)
			return b, nil
		}
		return nil, e
	}
	writeCache(path, a)
	return a, nil
}

// .
// .
func takeLock(lock string) bool {
	if st, e := os.Stat(lock); e == nil && time.Since(st.ModTime()) > cacheTTL {
		_ = os.Remove(lock)
	}
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if e != nil {
		return false
	}
	_ = f.Close()
	return true
}

func waitForCache(path string, limit time.Duration) *Artifacts {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if a := readCache(path); a != nil {
			return a
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil
}

// .
// .
func readCacheOlderThanTTL(path string) *Artifacts {
	st, e := os.Stat(path)
	if e != nil || time.Since(st.ModTime()) > staleFloor {
		return nil
	}
	return decodeCache(path)
}

func readCache(path string) *Artifacts {
	st, e := os.Stat(path)
	if e != nil || time.Since(st.ModTime()) > cacheTTL {
		return nil
	}
	return decodeCache(path)
}

func decodeCache(path string) *Artifacts {
	raw, e := os.ReadFile(path)
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
	if _, e = tmp.Write(raw); e != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return
	}
	if e = tmp.Close(); e != nil {
		_ = os.Remove(name)
		return
	}
	_ = os.Chmod(name, 0o644)
	if os.Rename(name, path) != nil {
		_ = os.Remove(name)
	}
}

func fetchAll() (*Artifacts, error) {
	a := &Artifacts{}
	ring0, token, e := get(GenesisURL+"/genesis/bundle", "")
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
		b, _, e := get(step.url, step.token)
		if e != nil {
			return nil, e
		}
		*step.dst = b
	}
	return a, nil
}

// .
// .
func Get(url, token string) ([]byte, error) {
	b, _, e := get(url, token)
	return b, e
}

// .
// .
func get(url, token string) ([]byte, string, error) {
	// .
	// .
	// .
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*attempt*2) * time.Second)
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

func attemptGet(url, token string) (body []byte, tok string, status int, e error) {
	req, e := http.NewRequest(http.MethodGet, url, nil)
	if e != nil {
		return nil, "", 0, e
	}
	if token != "" {
		req.Header.Set("X-Genesis-Token", token)
	}
	resp, e := (&http.Client{Timeout: 20 * time.Second}).Do(req)
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
