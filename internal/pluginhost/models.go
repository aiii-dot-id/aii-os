package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const ModelsFile = "models.json"

const (
	MaxModels         = 256
	MaxProfileModels  = 128
	MaxModelBytes     = 16 << 30
	maxModelPathBytes = 255
	maxModelPathDepth = 8
	modelPartialSuffx = ".partial"
)

var (
	reModelName    = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,127}$`)
	reModelSegment = regexp.MustCompile(`^[A-Za-z0-9.][A-Za-z0-9._+-]{0,127}$`)
	reSHA256       = regexp.MustCompile(`^[0-9a-f]{64}$`)

	reWindowsDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)
)

type ModelDecl struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`

	When *ModelWhen `json:"when,omitempty"`
}

type ModelWhen struct {
	Setting string   `json:"setting"`
	Values  []string `json:"values"`
}

const MaxModelWhenValues = 64

func (d ModelDecl) Dest() string {
	if d.Path != "" {
		return d.Path
	}
	return d.Name
}

func validateModelPath(dest string) error {
	if len(dest) > maxModelPathBytes {
		return fmt.Errorf("longer than %d bytes", maxModelPathBytes)
	}
	segs := strings.Split(dest, "/")
	if len(segs) > maxModelPathDepth {
		return fmt.Errorf("deeper than %d segments", maxModelPathDepth)
	}
	for _, s := range segs {
		if !reModelSegment.MatchString(s) || strings.HasSuffix(s, ".") {
			return fmt.Errorf("segment %q is not a portable file name (letters, digits, dots, underscores, plus and dashes, up to 128 bytes, not ending in a dot; a leading dot is admitted)", s)
		}
		if reWindowsDevice.MatchString(s) {
			return fmt.Errorf("segment %q is a reserved device name on Windows", s)
		}
	}
	if strings.HasSuffix(dest, modelPartialSuffx) {
		return fmt.Errorf("the %s suffix is the host's", modelPartialSuffx)
	}
	return nil
}

func modelFile(dir string, d ModelDecl) string {
	return filepath.Join(dir, filepath.FromSlash(d.Dest()))
}

type ModelsError struct {
	PluginID string
	Detail   string
}

func (e *ModelsError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not a model declaration the host honors: %s", e.PluginID, ModelsFile, e.Detail)
}

type ModelsMissingError struct {
	PluginID string
	Missing  []string
	Cause    error
}

func (e *ModelsMissingError) Error() string {
	why := "the host is offline or fetching is unavailable"
	if e.Cause != nil {
		why = e.Cause.Error()
	}
	return fmt.Sprintf("pluginhost: %s: declared models are missing from the plugin store and could not be brought (%s): %s — the machine service fetches them when it can reach their address; offline, place the files with their declared hashes at their declared paths in the identity's own plugins-models directory, which it adopts by digest", e.PluginID, why, strings.Join(e.Missing, ", "))
}

func (e *ModelsMissingError) Unwrap() error { return e.Cause }

var errNoDownloadPath = errors.New("no download path")

func ParseModels(raw []byte) ([]ModelDecl, error) {
	var decls []ModelDecl
	if err := readDeclaration(raw, &decls, "a list of models", "trailing content"); err != nil {
		return nil, err
	}
	if len(decls) > MaxModels {
		return nil, fmt.Errorf("%d models; at most %d", len(decls), MaxModels)
	}
	seen := map[string]bool{}
	dests := map[string]ModelDecl{}
	for i, d := range decls {
		if !reModelName.MatchString(d.Name) || strings.Contains(d.Name, "..") {
			return nil, fmt.Errorf("model %d: name %q is not a file name of lowercase letters, digits, dots, plus and dashes", i, d.Name)
		}
		if seen[d.Name] {
			return nil, fmt.Errorf("model %q declared twice", d.Name)
		}
		seen[d.Name] = true
		if err := validateModelPath(d.Dest()); err != nil {
			return nil, fmt.Errorf("model %q: path %q: %v", d.Name, d.Dest(), err)
		}

		if other, dup := dests[strings.ToLower(d.Dest())]; dup {
			return nil, fmt.Errorf("model %q: path %q collides with model %q's %q on a case-insensitive filesystem", d.Name, d.Dest(), other.Name, other.Dest())
		}
		dests[strings.ToLower(d.Dest())] = d
		if !strings.HasPrefix(d.URL, "https://") || len(d.URL) > 1024 {
			return nil, fmt.Errorf("model %q: url must be https and short", d.Name)
		}
		if !reSHA256.MatchString(d.SHA256) {
			return nil, fmt.Errorf("model %q: sha256 must be 64 hex digits", d.Name)
		}
		if d.Size <= 0 || d.Size > MaxModelBytes {
			return nil, fmt.Errorf("model %q: size must be 1..%d bytes", d.Name, int64(MaxModelBytes))
		}
		if w := d.When; w != nil {

			if !settingKeyPattern.MatchString(w.Setting) {
				return nil, fmt.Errorf("model %q: when.setting %q is not a setting key", d.Name, w.Setting)
			}
			if len(w.Values) == 0 || len(w.Values) > MaxModelWhenValues {
				return nil, fmt.Errorf("model %q: when.values must name 1..%d values", d.Name, MaxModelWhenValues)
			}
			named := map[string]bool{}
			for _, v := range w.Values {
				if v == "" || named[v] {
					return nil, fmt.Errorf("model %q: when.values names %q twice or names nothing", d.Name, v)
				}
				named[v] = true
			}
		}
	}

	for a, da := range dests {
		for b, db := range dests {
			if strings.HasPrefix(b, a+"/") {
				return nil, fmt.Errorf("model %q's path %q is a directory of model %q's %q", da.Name, da.Dest(), db.Name, db.Dest())
			}
		}
	}
	return decls, nil
}

func selectModels(decls []ModelDecl, profile *AcceleratorProfile, settings map[string]any) ([]ModelDecl, error) {
	conditional := map[string]bool{}
	known := map[string]bool{}
	for _, d := range decls {
		known[d.Name] = true
		conditional[d.Name] = d.When != nil
	}
	wanted := map[string]bool{}
	if profile != nil {
		for _, name := range profile.Models {
			if !known[name] {
				return nil, fmt.Errorf("the accelerator profile names model %q, which %s does not declare", name, ModelsFile)
			}
			if conditional[name] {
				return nil, fmt.Errorf("the accelerator profile names model %q, which is needed only for some values of a setting: a profile names what its platform always needs", name)
			}
			wanted[name] = true
		}
	}
	selected := make([]ModelDecl, 0, len(decls))
	for _, d := range decls {
		switch {
		case d.When != nil:
			if d.When.holds(settings) {
				selected = append(selected, d)
			}
		case profile == nil || wanted[d.Name]:
			selected = append(selected, d)
		}
	}
	return selected, nil
}

func (w *ModelWhen) holds(settings map[string]any) bool {
	v, _ := settings[w.Setting].(string)
	if v == "" {
		return false
	}
	for _, want := range w.Values {
		if v == want {
			return true
		}
	}
	return false
}

func checkModelConditions(decls []ModelDecl, settings []SettingDecl, minHost string) error {
	byKey := map[string]SettingDecl{}
	for _, s := range settings {
		byKey[s.Key] = s
	}
	for _, d := range decls {
		w := d.When
		if w == nil {
			continue
		}
		if !packagefmt.ValidHostBound(minHost) || packagefmt.CompareHostBounds(minHost, packagefmt.ModelWhenMinHost) < 0 {
			return fmt.Errorf("model %q is needed only for some values of a setting, which requires aiios_min_version >= %s", d.Name, packagefmt.ModelWhenMinHost)
		}
		s, declared := byKey[w.Setting]
		if !declared {
			return fmt.Errorf("model %q: when.setting %q is not a setting this package declares", d.Name, w.Setting)
		}
		if s.Type != SettingEnum {
			return fmt.Errorf("model %q: when.setting %q is a %s setting; a model can be conditional on an enum alone", d.Name, w.Setting, s.Type)
		}
		values := map[string]bool{}
		for _, v := range s.Values {
			values[v] = true
		}
		for _, v := range w.Values {
			if !values[v] {
				return fmt.Errorf("model %q: when.values names %q, which is not a value of setting %q", d.Name, v, w.Setting)
			}
		}
	}
	return nil
}

func loadModels(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest, profile *AcceleratorProfile, stored map[string]any) (selected, declared []ModelDecl, err error) {
	if _, present := res.FileDigests[ModelsFile]; !present {
		if profile != nil && len(profile.Models) > 0 {
			return nil, nil, &ModelsError{PluginID: m.ID, Detail: "the accelerator profile names models but the package declares none"}
		}
		return nil, nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, ModelsFile)
	if err != nil {
		return nil, nil, err
	}
	decls, err := ParseModels(raw)
	if err != nil {
		return nil, nil, &ModelsError{PluginID: m.ID, Detail: err.Error()}
	}

	var effective map[string]any
	for _, d := range decls {
		if d.When == nil {
			continue
		}
		settings, serr := loadSettings(pkgPath, res, held, m)
		if serr != nil {
			return nil, nil, serr
		}
		if err := checkModelConditions(decls, settings, m.AiiosMinVersion); err != nil {
			return nil, nil, &ModelsError{PluginID: m.ID, Detail: err.Error()}
		}
		effective = EffectiveSettings(settings, stored)
		break
	}
	selected, err = selectModels(decls, profile, effective)
	if err != nil {
		return nil, nil, &ModelsError{PluginID: m.ID, Detail: err.Error()}
	}
	return selected, decls, nil
}

func hasConditional(decls []ModelDecl) bool {
	for _, d := range decls {
		if d.When != nil {
			return true
		}
	}
	return false
}

func (ap *ActivePlugin) ModelSettings() map[string]bool {
	keys := map[string]bool{}
	for _, d := range ap.ModelDecls {
		if d.When != nil {
			keys[d.When.Setting] = true
		}
	}
	return keys
}

func (ap *ActivePlugin) ModelsWanted(stored map[string]any) ([]ModelDecl, error) {
	if len(ap.ModelDecls) == 0 || ap.ModelsDir == "" {
		return nil, nil
	}
	return selectModels(ap.ModelDecls, ap.Accelerator, EffectiveSettings(ap.Settings, stored))
}

func (ap *ActivePlugin) ModelsAbsent(decls []ModelDecl) []ModelDecl {
	var absent []ModelDecl
	for i, st := range ap.ModelStatuses(decls) {
		if !st.Present {
			absent = append(absent, decls[i])
		}
	}
	return absent
}

func (ap *ActivePlugin) ModelStatuses(decls []ModelDecl) []ModelStatus {
	return ap.material.modelStatuses(decls)
}

func (ap *ActivePlugin) Material() Material { return ap.material }

func (ap *ActivePlugin) LateMaterial(models []ModelDecl) Material {
	m := Material{PluginID: ap.ID, Version: ap.Version, Models: models, ModelsDir: ap.ModelsDir,
		store: ap.material.store, view: ap.material.view, adopt: ap.material.adopt}
	return m
}

type ModelFetcher func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error)

type ModelStatus struct {
	Name    string
	Path    string
	Size    int64
	Present bool

	Stored  bool
	Partial int64
}

func noLinks(dir string, d ModelDecl) error {
	cur := dir
	for _, seg := range strings.Split(d.Dest(), "/") {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link; the host keeps only regular files here", cur)
		}
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	return hashFileContext(context.Background(), path)
}

type acquisitionReader struct {
	ctx context.Context
	r   io.Reader
}

func (r acquisitionReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.r.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}

func hashFileContext(ctx context.Context, path string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, acquisitionReader{ctx, f})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
