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
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	configdir "github.com/aiii-dot-id/aii-os/config"
)

// .
const Dir = ".aii"

// .
const ByName = "by-name"

// .
// .
const slotPrefix = "identity-"

// .
// .
// .
// .
const basePort = 8180

var slotRE = regexp.MustCompile(`^` + slotPrefix + `(\d+)$`)

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
func Root() (string, error) {
	home, err := operatorHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, Dir), nil
}

// .
// .
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

// .
func SlotName(n int) string { return slotPrefix + strconv.Itoa(n) }

// .
func Port(n int) int { return basePort + n }

// .
func Slots(root string) ([]int, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var out []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m := slotRE.FindStringSubmatch(e.Name()); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				out = append(out, n)
			}
		}
	}
	sort.Ints(out)
	return out, nil
}

// .
// .
// .
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

// .
// .
// .
// .
func Create(root string, n int) (string, error) {
	dir := filepath.Join(root, SlotName(n))
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s already exists — refusing to adopt a directory this command did not create; it may hold an identity", dir)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	if err := writeConfig(dir, Port(n)); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// .
// .
// .
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
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// .
// .
// .
// .
func LinkName(root, name string, n int) error {
	if name == "" {
		return nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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
	// .
	// .
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("link %s: %w", link, err)
	}
	return nil
}
