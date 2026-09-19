package pluginhost

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
// .
// .

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
const ModelsFile = "models.json"

const (
	// .
	// .
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
	// .
	// .
	// .
	reWindowsDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)
)

// .
// .
// .
// .
type ModelDecl struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// .
func (d ModelDecl) Dest() string {
	if d.Path != "" {
		return d.Path
	}
	return d.Name
}

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

// .
func modelFile(dir string, d ModelDecl) string {
	return filepath.Join(dir, filepath.FromSlash(d.Dest()))
}

// .
type ModelsError struct {
	PluginID string
	Detail   string
}

func (e *ModelsError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not a model declaration the host honors: %s", e.PluginID, ModelsFile, e.Detail)
}

// .
// .
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
	return fmt.Sprintf("pluginhost: %s: declared models are missing and could not be fetched (%s): %s — place the files with their declared hashes at their declared paths under the plugin's models directory, or connect", e.PluginID, why, strings.Join(e.Missing, ", "))
}

// .
// .
func (e *ModelsMissingError) Unwrap() error { return e.Cause }

// .
// .
// .
var errNoDownloadPath = errors.New("no download path")

// .
func ParseModels(raw []byte) ([]ModelDecl, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var decls []ModelDecl
	if err := dec.Decode(&decls); err != nil {
		return nil, fmt.Errorf("not a list of models: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content")
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
		// .
		// .
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
	}
	// .
	// .
	for a, da := range dests {
		for b, db := range dests {
			if strings.HasPrefix(b, a+"/") {
				return nil, fmt.Errorf("model %q's path %q is a directory of model %q's %q", da.Name, da.Dest(), db.Name, db.Dest())
			}
		}
	}
	return decls, nil
}

// .
// .
// .
// .
func selectModels(decls []ModelDecl, profile *AcceleratorProfile) ([]ModelDecl, error) {
	if profile == nil {
		return decls, nil
	}
	known := map[string]bool{}
	for _, d := range decls {
		known[d.Name] = true
	}
	wanted := map[string]bool{}
	for _, name := range profile.Models {
		if !known[name] {
			return nil, fmt.Errorf("the accelerator profile names model %q, which %s does not declare", name, ModelsFile)
		}
		wanted[name] = true
	}
	selected := make([]ModelDecl, 0, len(wanted))
	for _, d := range decls {
		if wanted[d.Name] {
			selected = append(selected, d)
		}
	}
	return selected, nil
}

// .
// .
func loadModels(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest, profile *AcceleratorProfile) ([]ModelDecl, error) {
	if _, present := res.FileDigests[ModelsFile]; !present {
		if profile != nil && len(profile.Models) > 0 {
			return nil, &ModelsError{PluginID: m.ID, Detail: "the accelerator profile names models but the package declares none"}
		}
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, ModelsFile)
	if err != nil {
		return nil, err
	}
	decls, err := ParseModels(raw)
	if err != nil {
		return nil, &ModelsError{PluginID: m.ID, Detail: err.Error()}
	}
	selected, err := selectModels(decls, profile)
	if err != nil {
		return nil, &ModelsError{PluginID: m.ID, Detail: err.Error()}
	}
	return selected, nil
}

// .
// .
// .
type ModelFetcher func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error)

// .
type ModelStatus struct {
	Name    string
	Path    string
	Size    int64
	Present bool
	Partial int64
}

// .
// .
// .
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

// .
func hashFile(path string) (string, int64, error) {
	return hashFileContext(context.Background(), path)
}

// .
// .
// .
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

// .
func ModelStatuses(decls []ModelDecl, dir string) []ModelStatus {
	out := make([]ModelStatus, 0, len(decls))
	for _, d := range decls {
		st := ModelStatus{Name: d.Name, Path: d.Dest(), Size: d.Size}
		if fi, err := os.Stat(modelFile(dir, d)); err == nil && fi.Mode().IsRegular() && fi.Size() == d.Size {
			st.Present = true
		}
		if fi, err := os.Stat(modelFile(dir, d) + modelPartialSuffx); err == nil && fi.Mode().IsRegular() {
			st.Partial = fi.Size()
		}
		out = append(out, st)
	}
	return out
}

// .
// .
// .
// .
// .
func EnsureModels(ctx context.Context, pluginID string, decls []ModelDecl, dir string, fetch ModelFetcher, logf func(string, ...interface{})) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(decls) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("pluginhost: models dir: %w", err)
	}
	var missing []string
	var cause error
	for _, d := range decls {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := noLinks(dir, d); err != nil {
			missing = append(missing, d.Name)
			if cause == nil {
				cause = err
			}
			continue
		}
		final := modelFile(dir, d)
		if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
			return fmt.Errorf("pluginhost: models dir for %s: %w", d.Name, err)
		}
		if sum, n, err := hashFileContext(ctx, final); err == nil {
			if sum == d.SHA256 && n == d.Size {
				continue
			}
			// .
			// .
			if logf != nil {
				logf("plugin %s: model %s on disk does not match its declared hash — refetching", pluginID, d.Name)
			}
			_ = os.Remove(final)
		} else if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if err := fetchModel(ctx, pluginID, d, dir, fetch, logf); err != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return cancelled
			}
			missing = append(missing, d.Name)
			if cause == nil && !errors.Is(err, errNoDownloadPath) {
				cause = err
			}
		}
	}
	if len(missing) > 0 {
		return &ModelsMissingError{PluginID: pluginID, Missing: missing, Cause: cause}
	}
	return nil
}

func fetchModel(ctx context.Context, pluginID string, d ModelDecl, dir string, fetch ModelFetcher, logf func(string, ...interface{})) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	final := modelFile(dir, d)
	partial := final + modelPartialSuffx
	var offset int64
	// .
	// .
	if fi, err := os.Lstat(partial); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("partial for %s is not a regular file", d.Name)
		}
		offset = fi.Size()
		if offset > d.Size {
			if err := os.Remove(partial); err != nil {
				return fmt.Errorf("discard oversized partial for %s: %w", d.Name, err)
			}
			offset = 0
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect partial for %s: %w", d.Name, err)
	}
	var n int64
	// .
	// .
	if offset < d.Size {
		if fetch == nil {
			// .
			// .
			// .
			// .
			return fmt.Errorf("model %s is incomplete (%d of %d bytes): %w", d.Name, offset, d.Size, errNoDownloadPath)
		}
		f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open partial: %w", err)
		}
		if logf != nil {
			logf("plugin %s: fetching model %s (%d of %d bytes present)", pluginID, d.Name, offset, d.Size)
		}
		// .
		// .
		limited := &limitedWriter{w: f, remaining: d.Size - offset}
		var ferr error
		n, ferr = fetch(ctx, d.URL, offset, limited)
		cerr := f.Close()
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		if limited.overflow {
			_ = os.Remove(partial)
			return fmt.Errorf("fetch %s: the server sent more than the declared %d bytes", d.Name, d.Size)
		}
		if ferr != nil {
			if offset > 0 && strings.Contains(ferr.Error(), "does not resume") {
				_ = os.Remove(partial)
			}
			return fmt.Errorf("fetch %s: %w", d.Name, ferr)
		}
		if cerr != nil {
			return fmt.Errorf("write %s: %w", d.Name, cerr)
		}
	}
	sum, size, err := hashFileContext(ctx, partial)
	if err != nil {
		return err
	}
	if size != d.Size {
		return fmt.Errorf("fetch %s: %d of %d bytes after %d more; the download will resume", d.Name, size, d.Size, n)
	}
	if sum != d.SHA256 {
		_ = os.Remove(partial)
		return fmt.Errorf("fetch %s: the bytes do not hash to the declared sha256 — discarded", d.Name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Chmod(partial, 0o600); err != nil {
		return err
	}
	if err := os.Rename(partial, final); err != nil {
		return err
	}
	if logf != nil {
		logf("plugin %s: model %s verified (%d bytes)", pluginID, d.Name, size)
	}
	return nil
}

type limitedWriter struct {
	w         io.Writer
	remaining int64
	overflow  bool
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.remaining {
		l.overflow = true
		if l.remaining > 0 {
			_, _ = l.w.Write(p[:l.remaining])
			l.remaining = 0
		}
		return 0, errors.New("declared size exceeded")
	}
	n, err := l.w.Write(p)
	l.remaining -= int64(n)
	return n, err
}
