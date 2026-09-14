package pluginhost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

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
const (
	ArtifactKindPluginCatalog = "plugin.catalog"
	CatalogFile               = "aiios-plugins.md"
	CatalogSigFile            = "aiios-plugins.md.sig"
	catalogFence              = "```json"
)

// .
// .
// .
// .
type CatalogPackage struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// .
// .
// .
type CatalogEntry struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Tier        string `json:"tier"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	// .
	// .
	// .
	Title     string           `json:"title,omitempty"`
	Category  string           `json:"category,omitempty"`
	Keywords  []string         `json:"keywords,omitempty"`
	Updated   string           `json:"updated,omitempty"`
	Publisher string           `json:"publisher,omitempty"`
	Homepage  string           `json:"homepage,omitempty"`
	License   string           `json:"license,omitempty"`
	Packages  []CatalogPackage `json:"packages"`
}

// .
// .
// .
const DefaultCatalogURL = "https://raw.githubusercontent.com/aiii-dot-id/plugin-catalog/main/aiios-plugins.md"

// .
type Catalog struct {
	Version   int            `json:"catalog_version"`
	Generated string         `json:"generated"`
	Plugins   []CatalogEntry `json:"plugins"`
}

// .
// .
// .
type catalogSig struct {
	CatalogVersion int    `json:"catalog_version"`
	Generated      string `json:"generated"`
	CatalogSHA256  string `json:"catalog_sha256"`
}

// .
// .
// .
// .
func LoadCatalog(dir string, platformRoot *sigenvelope.PublicKeyEnvelope) (*Catalog, error) {
	mdBytes, err := os.ReadFile(filepath.Join(dir, CatalogFile))
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	sigBytes, err := os.ReadFile(filepath.Join(dir, CatalogSigFile))
	if err != nil {
		return nil, fmt.Errorf("catalog: %s: %w", CatalogSigFile, err)
	}
	return ParseCatalog(mdBytes, sigBytes, platformRoot)
}

// .
// .
// .
func ParseCatalog(mdBytes, sigBytes []byte, platformRoot *sigenvelope.PublicKeyEnvelope) (*Catalog, error) {
	if platformRoot == nil {
		return nil, fmt.Errorf("catalog: no platform_release root is pinned — the catalog cannot be verified (unverifiable is not unsigned)")
	}
	payloadRaw, err := sigenvelope.VerifyPayload(sigBytes, platformRoot, ArtifactKindPluginCatalog, crypto.ProfileRoot)
	if err != nil {
		return nil, fmt.Errorf("catalog signature REFUSED: %w", err)
	}
	var sig catalogSig
	if err := json.Unmarshal(payloadRaw, &sig); err != nil {
		return nil, fmt.Errorf("catalog: signed payload malformed: %w", err)
	}
	if got := sigenvelope.SHA256Prefixed(mdBytes); got != sig.CatalogSHA256 {
		return nil, fmt.Errorf("catalog: the signature does not cover this file (%s declares %s, file hashes to %s) — refused", CatalogSigFile, sig.CatalogSHA256, got)
	}
	block, err := catalogBlock(mdBytes)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(block))
	dec.DisallowUnknownFields()
	var cat Catalog
	if err := dec.Decode(&cat); err != nil {
		return nil, fmt.Errorf("catalog: index block malformed: %w", err)
	}
	if err := cat.validate(); err != nil {
		return nil, err
	}
	return &cat, nil
}

// .
// .
// .
// .
func catalogBlock(md []byte) (string, error) {
	s := string(md)
	i := strings.Index(s, catalogFence)
	if i < 0 {
		return "", fmt.Errorf("catalog: no %s index block in %s", catalogFence, CatalogFile)
	}
	rest := s[i+len(catalogFence):]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return "", fmt.Errorf("catalog: malformed index fence")
	}
	rest = rest[nl+1:]
	j := strings.Index(rest, "```")
	if j < 0 {
		return "", fmt.Errorf("catalog: unterminated index block")
	}
	return rest[:j], nil
}

func (c *Catalog) validate() error {
	seen := map[string]bool{}
	for _, e := range c.Plugins {
		if e.ID == "" || e.Version == "" {
			return fmt.Errorf("catalog: an entry is missing id or version")
		}
		if seen[e.ID] {
			return fmt.Errorf("catalog: %s appears twice", e.ID)
		}
		seen[e.ID] = true
		if e.Homepage != "" && !strings.HasPrefix(e.Homepage, "https://") {
			return fmt.Errorf("catalog: %s names a homepage that is not https", e.ID)
		}
		if len(e.Description) > 4096 || len(e.Summary) > 512 || len(e.Publisher) > 256 || len(e.License) > 128 || len(e.Homepage) > 1024 {
			return fmt.Errorf("catalog: %s carries detail longer than the store shows", e.ID)
		}
		if len(e.Title) > 120 || len(e.Category) > 40 || len(e.Keywords) > 16 {
			return fmt.Errorf("catalog: %s carries a title, category or keyword list longer than the store shows", e.ID)
		}
		for _, k := range e.Keywords {
			if k == "" || len(k) > 40 {
				return fmt.Errorf("catalog: %s carries an empty or overlong keyword", e.ID)
			}
		}
		if e.Updated != "" {
			if _, err := time.Parse("2006-01-02", e.Updated); err != nil {
				if _, err := time.Parse(time.RFC3339, e.Updated); err != nil {
					return fmt.Errorf("catalog: %s names an updated date that is neither YYYY-MM-DD nor RFC 3339", e.ID)
				}
			}
		}
		for _, p := range e.Packages {
			if p.Platform == "" || p.Arch == "" || p.URL == "" || p.SHA256 == "" {
				return fmt.Errorf("catalog: %s has an incomplete package (platform, arch, url, sha256 are required)", e.ID)
			}
		}
	}
	return nil
}

// .
// .
func (c *Catalog) Search(query string) []CatalogEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []CatalogEntry
	for _, e := range c.Plugins {
		if q == "" || strings.Contains(strings.ToLower(e.ID), q) || strings.Contains(strings.ToLower(e.Summary), q) ||
			strings.Contains(strings.ToLower(e.Title), q) || strings.Contains(strings.ToLower(strings.Join(e.Keywords, " ")), q) {
			out = append(out, e)
		}
	}
	return out
}

// .
// .
// .
func (c *Catalog) SelectFor(id, platform, arch string) (*CatalogEntry, *CatalogPackage, error) {
	for i := range c.Plugins {
		if c.Plugins[i].ID != id {
			continue
		}
		e := &c.Plugins[i]
		var portable *CatalogPackage
		for j := range e.Packages {
			p := &e.Packages[j]
			if p.Platform == platform && p.Arch == arch {
				return e, p, nil
			}
			if p.Platform == "*" && p.Arch == "*" {
				portable = p
			}
		}
		if portable != nil {
			return e, portable, nil
		}
		return e, nil, fmt.Errorf("catalog: %s has no build for %s/%s", id, platform, arch)
	}
	return nil, nil, fmt.Errorf("catalog: %s is not in the catalog", id)
}

// .
func (c *Catalog) Select(id string) (*CatalogEntry, *CatalogPackage, error) {
	return c.SelectFor(id, packagefmt.HostPlatform(), packagefmt.HostArch())
}

// .
// .
// .
// .
// .
func NewerVersion(candidate, installed string) bool {
	a, preA, okA := versionFields(candidate)
	b, preB, okB := versionFields(installed)
	if !okA || !okB {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return preB && !preA
}

func versionFields(v string) (fields []int, pre bool, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i] == '-'
		v = v[:i]
	}
	if v == "" {
		return nil, false, false
	}
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false, false
		}
		fields = append(fields, n)
	}
	return fields, pre, true
}
