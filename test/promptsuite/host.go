package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

// build is one host binary the suite runs: its path, and how a verdict
// names it.
type build struct {
	path    string
	sha256  string
	version string
}

// label is how a verdict names the build: its version line and the
// first twelve hex of its hash, so two binaries with one version line
// are still told apart.
func (b build) label() string { return b.version + " sha256:" + b.sha256[:12] }

// buildHost builds cmd/aii from the source tree at src into out, with the
// go tool given (AII_GO, else go on the path), the toolchain the tree
// pins and nothing downloaded.
func buildHost(goTool, src, out string) (build, error) {
	cmd := exec.Command(goTool, "build", "-o", out, "./cmd/aii")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if blob, err := cmd.CombinedOutput(); err != nil {
		return build{}, fmt.Errorf("build the host from %s: %v\n%s", src, err, blob)
	}
	return takeHost(out)
}

// takeHost names a binary already built.
func takeHost(path string) (build, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return build{}, err
	}
	blob, err := os.ReadFile(abs)
	if err != nil {
		return build{}, err
	}
	sum := sha256.Sum256(blob)
	out, err := exec.Command(abs, "-version").Output()
	if err != nil {
		return build{}, fmt.Errorf("%s does not answer -version: %w", abs, err)
	}
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return build{path: abs, sha256: hex.EncodeToString(sum[:]), version: version}, nil
}

type home struct {
	root string // the trial's directory; everything is under it
	dir  string
	slot string // the identity's home, ~/.aii/identity-0 inside dir
	env  []string
	bin  build
}

// newHome makes a trial's world under base and its slot through `aii
// init --no-start`, the door every installer makes a slot through. live
// is the account's own ~/.aii: a world that would land under it is
// refused before anything is made.
func newHome(base string, bin build, live string, passEnv []string) (h *home, err error) {
	root, err := os.MkdirTemp(base, "trial-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(root) // a world that could not be made leaves nothing
		}
	}()
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	if resolved, rerr := filepath.EvalSymlinks(live); rerr == nil {
		live = resolved // compared as the world's root is, through its links
	}
	if live != "" && under(root, live) {
		return nil, fmt.Errorf("a trial's home %s would be inside the account's identities at %s", root, live)
	}
	h = &home{root: root, dir: filepath.Join(root, "home"), bin: bin}
	tmp := filepath.Join(root, "tmp")
	for _, d := range []string{h.dir, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	h.env = []string{"HOME=" + h.dir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp, "LANG=C.UTF-8"}
	if runtime.GOOS == "windows" {
		h.env = append(h.env, "USERPROFILE="+h.dir, "TEMP="+tmp, "TMP="+tmp,
			"SystemRoot="+os.Getenv("SystemRoot"), "LOCALAPPDATA="+filepath.Join(h.dir, "AppData", "Local"), "APPDATA="+filepath.Join(h.dir, "AppData", "Roaming"))
	}
	if runtime.GOOS == "linux" {
		h.env = append(h.env, "XDG_CONFIG_HOME="+filepath.Join(h.dir, ".config"), "XDG_CACHE_HOME="+filepath.Join(h.dir, ".cache"))
	}
	for _, name := range passEnv {
		if v, ok := os.LookupEnv(name); ok {
			h.env = append(h.env, name+"="+v)
		}
	}
	out, err := h.aii("init", "--no-start")
	if err != nil {
		return nil, fmt.Errorf("aii init: %w\n%s", err, out)
	}
	h.slot = filepath.Join(h.dir, install.Dir, install.SlotName(0))
	if _, err := os.Stat(install.ConfigPathIn(h.slot)); err != nil {
		return nil, fmt.Errorf("aii init made no slot at %s: %w\n%s", h.slot, err, out)
	}
	return h, nil
}

// aii runs one verb of the trial's binary in the trial's environment.
func (h *home) aii(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin.path, args...)
	cmd.Env = h.env
	cmd.Dir = h.dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

// remove deletes the trial's world. A host still running is stopped
// first by its owner; this only removes files.
func (h *home) remove() error { return os.RemoveAll(h.root) }

// configPath is the slot's config.json.
func (h *home) configPath() string { return install.ConfigPathIn(h.slot) }

func (h *home) editConfig(keys map[string]any) error {
	file := h.configPath()
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("the slot's config.json: %w", err)
	}
	merge(cfg, keys)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := writeWhole(file, append(out, '\n')); err != nil {
		return err
	}
	if _, err := app.ReadConfig(file); err != nil {
		return fmt.Errorf("the host refuses the trial's config.json: %w", err)
	}
	return nil
}

func writeWhole(file string, data []byte) error {
	if published, err := atomicfile.WriteReplace(file, data, 0o600); err != nil {
		if published {
			return fmt.Errorf("%s was replaced but its directory was not synced: %w", file, err)
		}
		return err
	}
	return nil
}

// merge sets every key of src into dst, object into object.
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sub, ok := v.(map[string]any); ok {
			if have, ok := dst[k].(map[string]any); ok {
				merge(have, sub)
				continue
			}
			fresh := map[string]any{}
			merge(fresh, sub)
			dst[k] = fresh
			continue
		}
		dst[k] = v
	}
}

// freePort is a loopback port nothing holds at this moment.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func quiet(platformURL string) map[string]any {
	return map[string]any{
		"genesis": map[string]any{
			"server_url":    platformURL,
			"firewall_url":  platformURL,
			"bootstrap_url": platformURL,
		},
		"witness_server": map[string]any{"url": ""},
		"certificate":    map[string]any{"server_url": "none"},
		"updates":        map[string]any{"automatic": false},
		"plugins":        map[string]any{"catalog_url": noCatalog},
	}
}

// process is one running host.
type process struct {
	cmd  *exec.Cmd
	home *home
	done chan struct{}
	err  error
	log  *os.File
}

func (h *home) start() (*process, error) {
	log, err := os.OpenFile(filepath.Join(h.root, "host.out"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(h.bin.path, "-dir", h.slot)
	cmd.Env = h.env
	cmd.Dir = h.slot
	cmd.Stdout, cmd.Stderr = log, log
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	p := &process{cmd: cmd, home: h, done: make(chan struct{}), log: log}
	go func() {
		p.err = cmd.Wait()
		log.Close()
		close(p.done)
	}()
	return p, nil
}

func (p *process) stop(grace time.Duration) error {
	if p == nil {
		return nil
	}
	select {
	case <-p.done:
		return nil
	default:
	}
	askToStop(p)
	select {
	case <-p.done:
		return nil
	case <-time.After(grace):
	}
	killGroup(p.cmd)
	select {
	case <-p.done:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("the host (pid %d) did not end", p.cmd.Process.Pid)
	}
}

func (p *process) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (h *home) dashboard() (origin, token string, err error) {
	cfg, err := app.ReadConfig(h.configPath())
	if err != nil {
		return "", "", err
	}
	d := cfg.Dashboard
	if d.TLS || d.Port <= 0 {
		return "", "", fmt.Errorf("the slot's dashboard is not plain loopback (tls %v, port %d)", d.TLS, d.Port)
	}
	if d.RequireToken && d.AccessToken == "" {
		return "", "", errNoToken
	}
	if err := install.WaitServing(d.Port, h.slot, 0); err != nil {
		return "", "", err
	}
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(d.Port)), d.AccessToken, nil
}

// noCatalog is the catalog address a trial's host is given: https, as
// the setting requires, and on loopback, which the egress guard refuses.
const noCatalog = "https://127.0.0.1:1/promptsuite-installs-no-catalog"

var errNoToken = errors.New("the slot's dashboard asks for a token its config does not hold yet")

func (h *home) ledger() ([]byte, error) {
	cmd := exec.Command(h.bin.path, "ledger", "show", "-json", "-dir", h.slot)
	cmd.Env = h.env
	cmd.Dir = h.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("aii ledger show: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// under reports whether path is root or inside it.
func under(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyFile copies a file the trial needs into its world.
func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
