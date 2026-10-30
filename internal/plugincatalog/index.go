package plugincatalog

import (
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

const (
	ArtifactKindPluginCatalog = "plugin.catalog"
	CatalogFile               = "aiios-plugins.md"
	CatalogSigFile            = "aiios-plugins.md.sig"
	catalogFence              = "```json"
)

type CatalogPackage struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`

	OSMinVersion string `json:"os_min_version,omitempty"`
}

func (p CatalogPackage) Portable() bool { return p.Platform == "*" && p.Arch == "*" }

var systemNames = map[string]string{"macos": "macOS", "windows": "Windows", "linux": "Linux"}

func (p CatalogPackage) RunsOn(osVersion string) (bool, string) {
	if p.OSMinVersion == "" {
		return true, ""
	}
	name := systemNames[p.Platform]
	if name == "" {
		name = p.Platform
	}
	have, ok := dottedVersion(osVersion)
	if !ok {
		return false, "needs " + name + " " + p.OSMinVersion + " or newer, and this system's version could not be read"
	}
	want, _ := dottedVersion(p.OSMinVersion)
	for i := range want {
		h := 0
		if i < len(have) {
			h = have[i]
		}
		if h != want[i] {
			if h > want[i] {
				return true, ""
			}
			return false, "needs " + name + " " + p.OSMinVersion + " or newer (this is " + osVersion + ")"
		}
	}
	return true, ""
}

func dottedVersion(s string) ([]int, bool) {
	parts := strings.Split(s, ".")
	if s == "" || len(parts) > 4 {
		return nil, false
	}
	out := make([]int, len(parts))
	for i, part := range parts {
		if part == "" || len(part) > 9 {
			return nil, false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return nil, false
			}
			out[i] = out[i]*10 + int(c-'0')
		}
	}
	return out, true
}

type CatalogEntry struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Tier        string `json:"tier"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`

	Title     string           `json:"title,omitempty"`
	Category  string           `json:"category,omitempty"`
	Keywords  []string         `json:"keywords,omitempty"`
	Updated   string           `json:"updated,omitempty"`
	Publisher string           `json:"publisher,omitempty"`
	Homepage  string           `json:"homepage,omitempty"`
	License   string           `json:"license,omitempty"`
	Packages  []CatalogPackage `json:"packages"`

	AiiosMinVersion          string `json:"aiios_min_version,omitempty"`
	AiiosMaxExclusiveVersion string `json:"aiios_max_exclusive_version,omitempty"`
}

const (
	LegacyLine = "0.1"

	catalogHome = "https://raw.githubusercontent.com/aiii-dot-id/plugin-catalog/main/"
	catalogStem = "aiios-plugins"
)

func HostLine(v string) string {
	v = trimV(v)
	if !version.Valid(v) {
		return ""
	}
	parts := strings.SplitN(v, ".", 3)
	return parts[0] + "." + parts[1]
}

func OwnLine() string { return lineOrLegacy(HostLine(version.Authored())) }

func lineOrLegacy(line string) string {
	if line == "" {
		return LegacyLine
	}
	return line
}

func CatalogFileFor(line string) string {
	if line = lineOrLegacy(line); line == LegacyLine {
		return CatalogFile
	}
	return catalogStem + "-" + line + ".md"
}

func LineOfCatalogFile(name string) (string, bool) {
	if name == CatalogFile {
		return LegacyLine, true
	}
	line, ok := strings.CutPrefix(name, catalogStem+"-")
	if !ok {
		return "", false
	}
	line, ok = strings.CutSuffix(line, ".md")
	if !ok || HostLine(line+".0") != line || line == LegacyLine {
		return "", false
	}
	return line, true
}

func LineURL(url, line string) string {
	if i := strings.LastIndexByte(url, '/'); i >= 0 {
		return url[:i+1] + CatalogFileFor(line)
	}
	return CatalogFileFor(line)
}

var DefaultCatalogURL = catalogHome + CatalogFileFor(OwnLine())

type Index struct {
	Version   int            `json:"catalog_version"`
	Generated string         `json:"generated"`
	Plugins   []CatalogEntry `json:"plugins"`

	MustUnderstand []string `json:"must_understand,omitempty"`

	Announced Announcement `json:"-"`
}

type Announcement struct {
	Release string
	At      string

	StatusEpochs map[string]int64
}

var catalogFeatures = map[string]bool{

	"compat": true,

	"host_line": true,
}

type catalogSig struct {
	CatalogVersion int    `json:"catalog_version"`
	Generated      string `json:"generated"`
	CatalogSHA256  string `json:"catalog_sha256"`

	HostRelease  string           `json:"host_release,omitempty"`
	StatusEpochs map[string]int64 `json:"status_epochs,omitempty"`

	HostLine string `json:"host_line,omitempty"`
}

func (s catalogSig) bindsLine(line string) error {
	line = lineOrLegacy(line)
	switch {
	case s.HostLine != "" && s.HostLine != line:
		return fmt.Errorf(catalogOtherLine, s.HostLine, line)
	case s.HostLine == "" && line != LegacyLine:
		return fmt.Errorf(catalogNoLine, line)
	}
	return nil
}

const (
	catalogOtherLine = "catalog: this index is signed for AII OS %s hosts, and this host is AII OS %s: each line reads its own catalog file — refused"
	catalogNoLine    = "catalog: this index's signature names no host line, and an AII OS %s host reads only an index signed for its line — refused"
)

func (s catalogSig) announcement() (Announcement, error) {
	a := Announcement{Release: s.HostRelease, StatusEpochs: s.StatusEpochs}
	if a.Release == "" && len(a.StatusEpochs) == 0 {
		return Announcement{}, nil
	}
	if a.Release != "" && !version.Valid(a.Release) {
		return Announcement{}, fmt.Errorf("catalog: the signed host_release %q is not a version", a.Release)
	}
	if _, err := time.Parse(time.RFC3339, s.Generated); err != nil {
		return Announcement{}, fmt.Errorf("catalog: the signed payload announces a release and its generated %q is not an RFC 3339 time", s.Generated)
	}
	for root, epoch := range a.StatusEpochs {
		if epoch < 1 {
			return Announcement{}, fmt.Errorf("catalog: the signed status_epochs names epoch %d for %s; an epoch is at least 1", epoch, root)
		}
	}
	a.At = s.Generated
	return a, nil
}

func SignedPayload(md []byte, hostRelease, generated string, statusEpochs map[string]int64) ([]byte, error) {
	block, err := catalogBlock(md)
	if err != nil {
		return nil, err
	}
	var cat Index
	if err := json.Unmarshal([]byte(block), &cat); err != nil {
		return nil, fmt.Errorf("catalog: index block malformed: %w", err)
	}
	if err := cat.validate(); err != nil {
		return nil, err
	}
	if hostRelease == "" {
		return nil, fmt.Errorf("catalog: an announcement names the newest release (host_release)")
	}

	line := HostLine(hostRelease)
	if line != LegacyLine && line != "" && !slices.Contains(cat.MustUnderstand, "host_line") {
		return nil, fmt.Errorf("catalog: an index for AII OS %s names host_line in must_understand, so a host of an earlier line pointed at it refuses it whole", line)
	}
	sig := catalogSig{CatalogVersion: cat.Version, Generated: generated, CatalogSHA256: sigenvelope.SHA256Prefixed(md),
		HostRelease: hostRelease, StatusEpochs: statusEpochs, HostLine: line}
	if _, err := sig.announcement(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(sig, "", "  ")
}

func PublishedBeside(catalogPath, name string) string { return catalogPath + "." + name }

func LoadCatalog(dir string, platformRoot *sigenvelope.PublicKeyEnvelope) (*Index, error) {
	return LoadCatalogForLine(dir, platformRoot, OwnLine())
}

func LoadCatalogForLine(dir string, platformRoot *sigenvelope.PublicKeyEnvelope, line string) (*Index, error) {
	name := CatalogFileFor(line)
	mdBytes, err := atomicfile.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	sigBytes, err := atomicfile.ReadFile(filepath.Join(dir, name+".sig"))
	if err != nil {
		return nil, fmt.Errorf("catalog: %s.sig: %w", name, err)
	}
	return ParseCatalogForLine(mdBytes, sigBytes, platformRoot, line)
}

func ParseCatalog(mdBytes, sigBytes []byte, platformRoot *sigenvelope.PublicKeyEnvelope) (*Index, error) {
	return ParseCatalogForLine(mdBytes, sigBytes, platformRoot, OwnLine())
}

func ParseCatalogForLine(mdBytes, sigBytes []byte, platformRoot *sigenvelope.PublicKeyEnvelope, line string) (*Index, error) {
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
		return nil, fmt.Errorf("catalog: the signature does not cover this file (%s declares %s, file hashes to %s) — refused", CatalogFileFor(line)+".sig", sig.CatalogSHA256, got)
	}
	if err := sig.bindsLine(line); err != nil {
		return nil, err
	}
	announced, err := sig.announcement()
	if err != nil {
		return nil, err
	}
	block, err := catalogBlock(mdBytes)
	if err != nil {
		return nil, err
	}

	var cat Index
	if err := json.Unmarshal([]byte(block), &cat); err != nil {
		return nil, fmt.Errorf("catalog: index block malformed: %w", err)
	}

	var written struct {
		Plugins []map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(block), &written); err == nil {
		for i, entry := range written.Plugins {
			for _, name := range []string{"aiios_min_version", "aiios_max_exclusive_version"} {
				if bound, present := entry[name]; present {
					if err := packagefmt.CheckHostBoundRaw(name, bound); err != nil {
						id := ""
						if i < len(cat.Plugins) {
							id = cat.Plugins[i].ID
						}
						return nil, fmt.Errorf("catalog: %s: %w", id, err)
					}
				}
			}
		}
	}
	if err := cat.validate(); err != nil {
		return nil, err
	}
	cat.Announced = announced
	return &cat, nil
}

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

func (c *Index) validate() error {
	if len(c.MustUnderstand) > 16 {
		return fmt.Errorf("catalog: must_understand names %d features; at most 16", len(c.MustUnderstand))
	}
	for _, f := range c.MustUnderstand {
		if !catalogFeatures[f] {
			return fmt.Errorf("catalog: this index requires %q, which this AII OS does not understand — update the host to read it", f)
		}
	}
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

		if err := packagefmt.CheckHostWindow(e.AiiosMinVersion, e.AiiosMaxExclusiveVersion); err != nil {
			return fmt.Errorf("catalog: %s: %w", e.ID, err)
		}
		for _, p := range e.Packages {
			if p.Platform == "" || p.Arch == "" || p.URL == "" || p.SHA256 == "" {
				return fmt.Errorf("catalog: %s has an incomplete package (platform, arch, url, sha256 are required)", e.ID)
			}
			if p.OSMinVersion != "" {
				if _, ok := dottedVersion(p.OSMinVersion); !ok {
					return fmt.Errorf("catalog: %s names os_min_version %q for %s; it is one to four numbers joined by dots", e.ID, p.OSMinVersion, p.Platform)
				}
				if p.Platform == "*" {
					return fmt.Errorf("catalog: %s names os_min_version for a build that runs on every system", e.ID)
				}
			}
		}
	}
	return nil
}

func (c *Index) Search(query string) []CatalogEntry {
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

func (e CatalogEntry) SupportedBy(hostVersion string) (bool, string) {
	if e.AiiosMinVersion == "" && e.AiiosMaxExclusiveVersion == "" {
		return true, ""
	}
	if !packagefmt.ValidHostBound(hostVersion) {
		return false, "this host cannot read its own version, so whether this release runs on it cannot be decided"
	}
	if e.AiiosMinVersion != "" && packagefmt.CompareHostBounds(hostVersion, e.AiiosMinVersion) < 0 {
		return false, "needs AII OS " + e.AiiosMinVersion + " or newer"
	}
	if e.AiiosMaxExclusiveVersion != "" && packagefmt.CompareHostBounds(hostVersion, e.AiiosMaxExclusiveVersion) >= 0 {
		return false, "needs AII OS below " + e.AiiosMaxExclusiveVersion
	}
	return true, ""
}

func (c *Index) SelectFor(id, platform, arch string) (*CatalogEntry, *CatalogPackage, error) {
	return c.SelectForHost(id, platform, arch, version.Authored())
}

func (c *Index) SelectForHost(id, platform, arch, hostVersion string) (*CatalogEntry, *CatalogPackage, error) {
	for i := range c.Plugins {
		if c.Plugins[i].ID != id {
			continue
		}
		e := &c.Plugins[i]
		if ok, why := e.SupportedBy(hostVersion); !ok {
			return e, nil, fmt.Errorf("catalog: %s %s — %s (this host is %s)", id, e.Version, why, hostVersion)
		}
		var portable *CatalogPackage
		for j := range e.Packages {
			p := &e.Packages[j]
			if p.Platform == platform && p.Arch == arch {
				return e, p, nil
			}
			if p.Portable() {
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

func (c *Index) Select(id string) (*CatalogEntry, *CatalogPackage, error) {
	return c.SelectFor(id, packagefmt.HostPlatform(), packagefmt.HostArch())
}

func NewerVersion(candidate, installed string) bool {
	if c, i := trimV(candidate), trimV(installed); version.Valid(c) && version.Valid(i) {
		return version.Compare(c, i) > 0
	}
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

func trimV(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func versionFields(v string) (fields []int, pre bool, ok bool) {
	v = trimV(v)
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

type Stranded struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

const (
	strandedNotCarried = "the AII OS %s catalog does not carry it"
	strandedNoBuild    = "the AII OS %s catalog has no build of it for %s/%s"
	strandedWindow     = "the AII OS %s catalog's %s %s"
)

func (c *Index) StrandedBy(installed map[string]string, offered, platform, arch string) []Stranded {
	line := lineOrLegacy(HostLine(offered))
	ids := make([]string, 0, len(installed))
	for id := range installed {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var out []Stranded
	for _, id := range ids {
		reason := ""
		e, pkg, _ := c.SelectForHost(id, platform, arch, offered)
		switch {
		case e == nil:
			reason = fmt.Sprintf(strandedNotCarried, line)
		case pkg == nil:
			if ok, why := e.SupportedBy(offered); !ok {
				reason = fmt.Sprintf(strandedWindow, line, e.Version, why)
			} else {
				reason = fmt.Sprintf(strandedNoBuild, line, platform, arch)
			}
		}
		if reason != "" {
			out = append(out, Stranded{ID: id, Version: installed[id], Reason: reason})
		}
	}
	return out
}
