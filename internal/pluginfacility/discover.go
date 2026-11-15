package pluginfacility

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
)

type Found struct {
	Dir, Package string
	Size, MTime  int64

	Err error
}

type Discovery struct {
	Found     []Found
	Ambiguous []string

	Err error
}

type Skip struct {
	Kind         string
	Dir, Package string
	ID, Tier     string
	Reason       string

	Version      string
	Native       bool
	Capabilities []string
}

const (
	SkipPolicy     = "policy"
	SkipUnverified = "unverified"
	SkipAmbiguous  = "ambiguous"
	SkipDuplicate  = "duplicate"
)

type verifyMemo struct {
	size, mtime int64
	trustGen    uint64
	ev          Evidence
	err         error
}

func (f *Facility) VerifiedPackages(ctx context.Context) ([]Evidence, error) {
	var out []Evidence
	if f.cfg.Discover == nil {
		return out, nil
	}
	for _, found := range f.cfg.Discover().Found {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ev, err := f.cfg.Runtime.Verify(ctx, found.Package)
		if err == nil {
			out = append(out, ev)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *Facility) Rescan(pol Policy) {
	if f.cfg.Discover == nil {
		f.observeAgain(pol)
		return
	}
	disc := f.cfg.Discover()
	f.scanMu.Lock()
	defer f.scanMu.Unlock()

	if disc.Err != nil {
		if said := disc.Err.Error(); said != f.loggedUnlisted {
			f.loggedUnlisted = said
			f.cfg.Log("the plugins directory could not be listed (%v) — nothing is taken as removed; what was installed stands until it can be read", disc.Err)
		}
		f.observeAgain(pol)
		return
	}
	f.loggedUnlisted = ""
	if f.verified == nil {
		f.verified = map[string]verifyMemo{}
	}
	var skips []Skip
	sort.Strings(disc.Ambiguous)
	for _, dir := range disc.Ambiguous {
		if !f.loggedAmbiguous[dir] {
			f.cfg.Log("plugin dir %s: more than one package — ambiguous, REFUSED (one package per directory)", dir)
		}
		skips = append(skips, Skip{Kind: SkipAmbiguous, Dir: dir,
			Reason: "more than one package in this directory — refused whole; keep exactly one"})
	}
	nextAmbiguous := map[string]bool{}
	for _, dir := range disc.Ambiguous {
		nextAmbiguous[dir] = true
	}
	f.loggedAmbiguous = nextAmbiguous

	sort.Slice(disc.Found, func(i, j int) bool { return disc.Found[i].Dir < disc.Found[j].Dir })
	seen := map[string]bool{}
	var set []Observed
	byID := map[string]string{}
	nextUnread := map[string]bool{}
	for _, fd := range disc.Found {
		seen[fd.Package] = true
		memo, ok := f.verified[fd.Package]
		unread := fd.Err
		if unread == nil && (!ok || memo.size != fd.Size || memo.mtime != fd.MTime || memo.trustGen != pol.TrustGen) {
			ev, err := f.cfg.Runtime.Verify(f.ctxOrBackground(), fd.Package)
			if unreadable(err) {
				unread = err
			} else {
				memo = verifyMemo{size: fd.Size, mtime: fd.MTime, trustGen: pol.TrustGen, ev: ev, err: err}
				f.verified[fd.Package] = memo
				if err != nil {
					f.cfg.Log("plugin %s: verification FAILED, package skipped (identity unaffected; this is not T0 — invalid evidence refuses at every autoload level): %v", fd.Package, err)
				}
			}
		}
		if unread != nil {

			nextUnread[fd.Package] = true
			if !f.loggedUnread[fd.Package] {
				f.cfg.Log("plugin %s could not be read (%v) — not taken as invalid; whatever it serves stands, and it is read again at the next look", fd.Package, unread)
			}
			for _, o := range f.standingIn(fd.Dir) {
				if _, dup := byID[o.ID]; dup {
					continue
				}
				byID[o.ID] = fd.Dir
				set = append(set, o)
			}
			continue
		}
		if memo.err != nil {

			skips = append(skips, Skip{Kind: SkipUnverified, Dir: fd.Dir, Package: fd.Package,
				Reason: "verification failed — refused at every autoload level: " + memo.err.Error()})
			continue
		}
		id := memo.ev.ID
		if prev, dup := byID[id]; dup {
			f.cfg.Log("plugin dir %s: verified id %q already provided by %s — duplicate REFUSED", fd.Dir, id, prev)
			skips = append(skips, Skip{Kind: SkipDuplicate, Dir: fd.Dir, Package: fd.Package, ID: id, Tier: memo.ev.Tier,
				Reason: "its verified id is already provided by " + prev + " — duplicate refused; remove one"})
			continue
		}
		byID[id] = fd.Dir
		if base := filepath.Base(fd.Dir); base != id {
			f.cfg.Log("plugin dir %s: note — directory name differs from verified id %q (identity comes from the signature)", fd.Dir, id)
		}
		if ok, why := pol.allows(memo.ev); !ok {
			skips = append(skips, Skip{Kind: SkipPolicy, Dir: fd.Dir, Package: fd.Package, ID: id, Tier: memo.ev.Tier, Reason: why,
				Version: memo.ev.Version, Native: memo.ev.Native, Capabilities: memo.ev.Capabilities})
			f.cfg.Log("plugin %s (%s): %s — present, verified, NOT loaded", id, memo.ev.Tier, why)
			continue
		}
		set = append(set, Observed{ID: id, Dir: fd.Dir, Package: fd.Package, Hash: memo.ev.PackageHash, ManifestHash: memo.ev.ManifestHash, Family: memo.ev.Family})
	}
	f.loggedUnread = nextUnread

	for pkg := range f.verified {
		if !seen[pkg] {
			delete(f.verified, pkg)
		}
	}
	sort.Slice(set, func(i, j int) bool { return set[i].ID < set[j].ID })

	f.observe(set, pol, &skips)
}

func (f *Facility) observeAgain(pol Policy) {
	f.Observe(f.standing(func(*Instance) bool { return true }), pol)
}

func (f *Facility) standingIn(dir string) []Observed {
	return f.standing(func(inst *Instance) bool { return inst.Dir == dir })
}

func (f *Facility) standing(in func(*Instance) bool) []Observed {
	f.mu.Lock()
	defer f.mu.Unlock()
	set := make([]Observed, 0, len(f.instances))
	for _, inst := range f.instances {
		if in(inst) && (inst.Desired.Active || inst.Package != "") {
			set = append(set, Observed{ID: inst.ID, Dir: inst.Dir, Package: inst.Package, Hash: inst.PackageHash, ManifestHash: inst.ManifestHash, Family: inst.Family})
		}
	}
	sort.Slice(set, func(i, j int) bool { return set[i].ID < set[j].ID })
	return set
}

func unreadable(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr)
}

func (f *Facility) Skips() []Skip {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Skip(nil), f.skips...)
}

func (f *Facility) ctxOrBackground() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}
