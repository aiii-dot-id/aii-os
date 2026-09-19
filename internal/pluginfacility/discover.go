package pluginfacility

import (
	"context"
	"path/filepath"
	"sort"
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
type Found struct {
	Dir, Package string
	Size, MTime  int64
}

// .
// .
// .
type Discovery struct {
	Found     []Found
	Ambiguous []string
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
// .
// .
type Skip struct {
	Kind         string
	Dir, Package string
	ID, Tier     string
	Reason       string
}

// .
const (
	SkipPolicy     = "policy"
	SkipUnverified = "unverified"
	SkipAmbiguous  = "ambiguous"
	SkipDuplicate  = "duplicate"
)

// .
// .
// .
// .
// .
type verifyMemo struct {
	size, mtime int64
	trustGen    uint64
	ev          Evidence
	err         error
}

// .
// .
// .
// .
// .
// .
func (f *Facility) Rescan(pol Policy) {
	if f.cfg.Discover == nil {
		f.mu.Lock()
		set := make([]Observed, 0, len(f.instances))
		for _, inst := range f.instances {
			if inst.Desired.Active || inst.Package != "" {
				set = append(set, Observed{ID: inst.ID, Package: inst.Package, Hash: inst.PackageHash})
			}
		}
		f.mu.Unlock()
		f.Observe(set, pol)
		return
	}
	disc := f.cfg.Discover()
	f.scanMu.Lock()
	defer f.scanMu.Unlock()
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
	for _, fd := range disc.Found {
		seen[fd.Package] = true
		memo, ok := f.verified[fd.Package]
		if !ok || memo.size != fd.Size || memo.mtime != fd.MTime || memo.trustGen != pol.TrustGen {
			ev, err := f.cfg.Runtime.Verify(f.ctxOrBackground(), fd.Package)
			memo = verifyMemo{size: fd.Size, mtime: fd.MTime, trustGen: pol.TrustGen, ev: ev, err: err}
			f.verified[fd.Package] = memo
			if err != nil {
				f.cfg.Log("plugin %s: verification FAILED, package skipped (identity unaffected; this is not T0 — invalid evidence refuses at every autoload level): %v", fd.Package, err)
			}
		}
		if memo.err != nil {
			// .
			// .
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
			skips = append(skips, Skip{Kind: SkipPolicy, Dir: fd.Dir, Package: fd.Package, ID: id, Tier: memo.ev.Tier, Reason: why})
			f.cfg.Log("plugin %s (%s): %s — present, verified, NOT loaded", id, memo.ev.Tier, why)
			continue
		}
		set = append(set, Observed{ID: id, Dir: fd.Dir, Package: fd.Package, Hash: memo.ev.PackageHash})
	}
	// .
	for pkg := range f.verified {
		if !seen[pkg] {
			delete(f.verified, pkg)
		}
	}
	sort.Slice(set, func(i, j int) bool { return set[i].ID < set[j].ID })
	// .
	// .
	// .
	f.observe(set, pol, &skips)
}

// .
// .
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
