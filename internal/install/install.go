package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	configdir "github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

const Dir = ".aii"

const ByName = "by-name"

const FirstStartLock = "first-start.lock"

const slotPrefix = "identity-"

const basePort = 8180

var slotRE = regexp.MustCompile(`^` + slotPrefix + `(\d+)$`)

func Root() (string, error) {
	home, err := operatorHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, Dir), nil
}

func OperatorName() (string, error) {
	if u := os.Getenv("SUDO_USER"); u != "" && u != "root" {
		return u, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("determine the operator: %w", err)
	}
	return u.Username, nil
}

func operatorHome() (string, error) {
	if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
		u, err := user.Lookup(name)
		if err != nil {
			return "", fmt.Errorf("look up %s (SUDO_USER): %w", name, err)
		}
		return u.HomeDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return home, nil
}

var ErrNotRunning = errors.New("not running")

func SlotName(n int) string { return slotPrefix + strconv.Itoa(n) }

func Port(n int) int { return basePort + n }

func Slots(root string) ([]int, error) {
	dirs, err := SlotDirs(root)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(dirs))
	for _, d := range dirs {
		n, err := strconv.Atoi(slotRE.FindStringSubmatch(filepath.Base(d))[1])
		if err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func SlotDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	type slot struct {
		n   int
		dir string
	}
	var found []slot
	for _, e := range entries {
		m := slotRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if st, err := os.Stat(dir); err == nil && st.Mode().IsRegular() {
			continue
		}
		found = append(found, slot{n, dir})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].n < found[j].n })
	out := make([]string, 0, len(found))
	for _, s := range found {
		out = append(out, s.dir)
	}
	return out, nil
}

func SiblingSlots(slot string) ([]string, error) {
	abs, err := filepath.Abs(slot)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", slot, err)
	}
	own := resolved(abs)
	all, err := SlotDirs(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range all {
		if resolved(d) != own {
			out = append(out, d)
		}
	}
	return out, nil
}

func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

func NextSlot(root string) (int, error) {
	used, err := Slots(root)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]bool, len(used))
	for _, n := range used {
		taken[n] = true
	}
	for n := 0; ; n++ {
		if !taken[n] {
			return n, nil
		}
	}
}

var errSlotHeld = errors.New("refusing to adopt a directory this command did not create; it may hold an identity")

func Create(root string, n int) (string, error) {
	dir := filepath.Join(root, SlotName(n))

	if _, err := os.Lstat(dir); err == nil {
		return "", fmt.Errorf("%s already exists — %w", dir, errSlotHeld)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", dir, err)
	}
	if err := fileperm.MkdirOwnerOnly(dir); err != nil {

		if errors.Is(err, fs.ErrExist) {
			if _, lerr := os.Lstat(dir); lerr == nil {
				return "", fmt.Errorf("%s already exists — %w", dir, errSlotHeld)
			}
		}
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	if err := writeConfig(dir, Port(n)); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func writeConfig(dir string, port int) error {
	var cfg map[string]any
	if err := json.Unmarshal(configdir.Config, &cfg); err != nil {
		return fmt.Errorf("embedded config is invalid: %w", err)
	}
	dash, ok := cfg["dashboard"].(map[string]any)
	if !ok {
		return fmt.Errorf("embedded config has no dashboard section")
	}
	dash["port"] = port

	dash["require_token"] = true

	delete(cfg, "identity")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	path := ConfigPathIn(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	published, err := atomicfile.WriteReplace(path, data, 0o600)
	if err != nil {
		if published {
			return fmt.Errorf("write %s: published but not durable: %w", path, err)
		}
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func LinkName(root, name string, n int) error {
	if name == "" {
		return nil
	}

	if filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("refusing by-name link for %q: a name must be a single path component", name)
	}
	dir := filepath.Join(root, ByName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	link := filepath.Join(dir, name)
	target := filepath.Join("..", SlotName(n))
	if existing, err := os.Readlink(link); err == nil && existing == target {
		return nil
	}

	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("link %s: %w", link, err)
	}
	return nil
}
