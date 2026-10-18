//go:build acceptance

package main

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// liveIdentitiesEnv names where this host keeps identities that are
// somebody. The driver sends them nothing and reads nothing of theirs: a
// slot at or under that directory is refused on its spelling, before
// anything is opened, and again after its symlinks are resolved. The
// tree names no such directory — every host keeps its own — so the
// environment must: unset, or not an absolute path, and the driver
// refuses to run rather than guess that nothing is live.
const liveIdentitiesEnv = "AII_LIVE_IDENTITIES"

// Requirements a refusal names.
const (
	needDisposable    = "AII_ACCEPTANCE_DISPOSABLE=1"
	needSlotNamed     = "AII_ROOT and AII_SLOT naming one slot"
	needLiveNamed     = liveIdentitiesEnv + " naming, as an absolute path, where this host keeps live identities"
	needNotLive       = "a slot outside " + liveIdentitiesEnv
	needSlot          = "an existing slot with its own config"
	needPlainLoopback = "a dashboard bound to loopback without TLS"
	needAccessToken   = "the slot's own dashboard access token"
)

// refusal is why the driver will not run. It names the requirement.
type refusal struct {
	requirement string
	detail      string
}

func (r *refusal) Error() string {
	return fmt.Sprintf("acceptance driver refused: requires %s — %s", r.requirement, r.detail)
}

// target is the disposable slot a run drives.
type target struct {
	slotDir string
	dbPath  string
	// origin is the slot's plain loopback dashboard, scheme://host:port.
	origin string
	// token is the slot's dashboard access token, or empty when it has
	// none. It is sent to the slot's own /auth/token and nowhere else,
	// and never printed.
	token string
}

// under reports whether path is root or inside it. Both are cleaned
// absolute paths; a sibling that merely shares the prefix is not inside.
func under(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveTarget names the slot the way packaging/drills/lib.sh does
// (AII_ROOT, AII_SLOT) — but with no defaults, because the default slot
// is the operator's own identity-0 — and refuses anything it cannot
// prove is disposable and local. live is the path no slot may be under.
func resolveTarget(getenv func(string) string, live string) (target, error) {
	if getenv("AII_ACCEPTANCE_DISPOSABLE") != "1" {
		return target{}, &refusal{needDisposable, "the operator has not declared this slot disposable; the driver sends it operator messages and its jobs may act"}
	}
	if live == "" || !filepath.IsAbs(live) {
		return target{}, &refusal{needLiveNamed, fmt.Sprintf("%s is %q; the driver will not guess which identities are live", liveIdentitiesEnv, live)}
	}
	live = filepath.Clean(live)
	root, slot := getenv("AII_ROOT"), getenv("AII_SLOT")
	if root == "" || slot == "" {
		return target{}, &refusal{needSlotNamed, "both must be set; there is no default slot"}
	}
	if slot != filepath.Base(slot) || slot == "." || slot == ".." {
		return target{}, &refusal{needSlotNamed, fmt.Sprintf("AII_SLOT %q is not one directory name", slot)}
	}
	abs, err := filepath.Abs(filepath.Join(root, slot))
	if err != nil {
		return target{}, &refusal{needSlotNamed, err.Error()}
	}
	if under(abs, live) {
		return target{}, &refusal{needNotLive, abs + " is a live identity's"}
	}
	dir, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return target{}, &refusal{needSlot, err.Error()}
	}
	if under(dir, live) {
		return target{}, &refusal{needNotLive, abs + " resolves to " + dir}
	}

	// The slot's own settings, through the one config parser. It is
	// stat'd first: the parser CREATES a default config where none is.
	cfgPath := install.ConfigPathIn(dir)
	if _, err := os.Stat(cfgPath); err != nil {
		return target{}, &refusal{needSlot, err.Error()}
	}
	cfg, err := app.LoadConfig(cfgPath)
	if err != nil {
		return target{}, &refusal{needSlot, err.Error()}
	}
	d := cfg.Dashboard
	if !dashboard.IsLoopback(d.Host) || d.TLS || d.Port <= 0 {
		return target{}, &refusal{needPlainLoopback, fmt.Sprintf("the slot's dashboard is host %q port %d tls %v; a disposable slot is reached on this machine only", d.Host, d.Port, d.TLS)}
	}
	host := d.Host
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	if d.RequireToken && d.AccessToken == "" {
		return target{}, &refusal{needAccessToken, "require_token is on and the slot's config holds no access_token"}
	}

	db := cfg.Identity.DBPath
	if db == "" {
		return target{}, &refusal{needSlot, "identity.db_path is empty"}
	}
	if !filepath.IsAbs(db) {
		db = filepath.Join(dir, db)
	}
	if db, err = filepath.Abs(db); err != nil {
		return target{}, &refusal{needSlot, err.Error()}
	}
	if under(db, live) {
		return target{}, &refusal{needNotLive, "the slot's database " + db + " is a live identity's"}
	}
	if db, err = filepath.EvalSymlinks(db); err != nil {
		return target{}, &refusal{needSlot, err.Error()}
	}
	if under(db, live) {
		return target{}, &refusal{needNotLive, "the slot's database resolves to " + db}
	}
	return target{
		slotDir: dir,
		dbPath:  db,
		origin:  "http://" + net.JoinHostPort(host, strconv.Itoa(d.Port)),
		token:   d.AccessToken,
	}, nil
}
