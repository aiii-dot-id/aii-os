package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func (r *Registry) SetExtraRoots(roots []string) {
	resolved, refused := r.readExtraRoots(roots)
	for _, abs := range refused {
		logsink.Warn("tools.refusal", "REFUSED grant %q — it would expose the identity's own substrate", abs)
	}
	r.regMu.Lock()
	was := r.extraRoots
	r.extraRoots = resolved
	r.regMu.Unlock()
	for _, root := range was {
		if !slices.Contains(resolved, root) {
			releaseGrant(root, append([]string{r.sandbox}, resolved...))
		}
	}
}

func (r *Registry) ExtraRootsMoved(roots []string) bool {
	resolved, _ := r.readExtraRoots(roots)
	_, held := r.Roots()
	return !slices.Equal(resolved, held)
}

func (r *Registry) readExtraRoots(roots []string) (resolved, refused []string) {
	sandbox, places := r.sandbox, r.policy.Places()
	resolved = make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		abs := filepath.Clean(root)
		if _, err := os.Stat(abs); err == nil {
			abs = firewall.ResolvePath(abs)
		}

		if rootExposesSubstrate(abs, sandbox, places) {
			refused = append(refused, abs)
			continue
		}
		resolved = append(resolved, abs)
	}
	return resolved, refused
}

func (r *Registry) RootRejectionReason(path string) string {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return "must be an absolute path"
	}
	abs := filepath.Clean(path)
	if _, err := os.Stat(abs); err == nil {
		abs = firewall.ResolvePath(abs)
	}

	places := r.policy.Places()
	if _, granted := r.Roots(); !r.policy.Boundary() && !slices.Contains(granted, abs) {
		places = r.policy.Kept()
	}
	if rule := firewall.Unplaced(abs); rule != nil {
		return rule.Reason + " — refused"
	}
	if rootExposesSubstrate(abs, r.sandbox, places) {
		return "would expose the identity's own substrate (ledger, keys, config, data, logs, the running program, or home) — refused"
	}
	return ""
}

func rootExposesSubstrate(abs, sandbox string, places []string) bool {
	if abs == "/" || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return true
	}
	if firewall.Unplaced(abs) != nil {
		return true
	}
	if abs == sandbox || within(abs, sandbox) {
		return true
	}
	for _, p := range places {
		if within(abs, p) || within(p, abs) {
			return true
		}
	}
	return false
}

func (r *Registry) SetSandboxOff(off bool) { r.policy.SetBoundary(!off) }

func (r *Registry) SandboxOff() bool {
	if r.policy.Boundary() {
		return false
	}
	if r.safeSource != nil {
		if _, safe := r.safeSource(); safe {
			return false
		}
	}
	return true
}

type SubstrateError struct{ Rule *firewall.Rule }

func (e *SubstrateError) Error() string {
	if e.Rule.Kind == firewall.KindUnplaced {
		return e.Rule.Reason
	}
	return fmt.Sprintf("protected path (%s: %s)", e.Rule.ID, e.Rule.Reason)
}

func refused(rule *firewall.Rule, err error) error {
	if err != nil {
		return err
	}
	if rule != nil {
		return &SubstrateError{Rule: rule}
	}
	return nil
}

func (r *Registry) Admit(op, path string) (dir, rel string, err error) {
	return r.admitAs(r.policy, op, path)
}

func (r *Registry) admitAs(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	dir, rel, err = r.admit(policy, op, path)
	return dir, rel, r.asTold(err)
}

type Gate interface {
	Admit(op, path string) (dir, rel string, err error)
	Held(file os.FileInfo, by *os.File) error
}

func (r *Registry) Look() Gate {
	return lookGate{r: r, policy: r.policy.Look()}
}

type lookGate struct {
	r      *Registry
	policy *firewall.Policy
}

func (g lookGate) Admit(op, path string) (dir, rel string, err error) {
	return g.r.admitAs(g.policy, op, path)
}

func (g lookGate) Held(file os.FileInfo, by *os.File) error {
	return g.r.heldBy(g.policy, "", file, by)
}

func (r *Registry) asTold(err error) error {
	unchecked, ok := err.(*firewall.UncheckedError)
	if !ok {
		return err
	}
	told := *unchecked
	told.Reason = r.said(unchecked)
	return &told
}

func (r *Registry) Held(file os.FileInfo, by *os.File) error {
	return r.heldBy(r.policy, "", file, by)
}

func (r *Registry) heldBy(policy *firewall.Policy, tool string, file os.FileInfo, by *os.File) error {
	return r.asTold(refused(policy.KeptAsFor(tool, file, by)))
}

func (r *Registry) heldAt(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
	return r.heldAs(ctx, "write", path, file, by)
}

func (r *Registry) heldAs(ctx context.Context, tool, path string, file os.FileInfo, by *os.File) error {
	policy := r.wallOf(ctx)
	if err := r.heldBy(policy, tool, file, by); err != nil {
		return err
	}
	if tool != "read" {
		if err := r.heldWhere(ctx, tool, by); err != nil {
			return err
		}
	}
	return r.asTold(r.substrateDenied(policy, tool, path))
}

func (r *Registry) heldRead(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
	if err := r.heldWhere(ctx, "read", by); err != nil {
		return err
	}
	if firewall.Names(file, by) < 2 {
		return nil
	}
	return r.heldAs(ctx, "read", path, file, by)
}

func (r *Registry) heldWhere(ctx context.Context, op string, by *os.File) error {
	if by == nil {
		return nil
	}
	at, err := openedAt(by)
	if err != nil {
		return fmt.Errorf("where the opened file lies could not be asked (%v), so it is not read or written", err)
	}

	held, err := by.Stat()
	if err != nil {
		return err
	}
	if there, err := os.Stat(at); err != nil || !os.SameFile(there, held) {
		return fmt.Errorf("the opened file is no longer at %s, where the system says it lies, so it is not read or written", at)
	}
	_, _, err = r.admitOwn(r.wallOf(ctx), op, at)
	return err
}

type undoWall struct{}

type callLook struct{}

func (r *Registry) lookFor(t Tool) *firewall.Policy {
	if ro, ok := t.(interface{ ReadOnly() bool }); ok && ro.ReadOnly() {
		return r.policy.SteadyLook()
	}
	return r.policy.Look()
}

func (r *Registry) wallOf(ctx context.Context) *firewall.Policy {
	if wall, ok := ctx.Value(undoWall{}).(*firewall.Policy); ok && wall != nil {
		return wall
	}
	if wall, ok := ctx.Value(callLook{}).(*firewall.Policy); ok && wall != nil {
		return wall
	}
	return r.policy
}

func (r *Registry) keptAsMade(ctx context.Context, path string) error {
	return r.asTold(r.substrateDenied(r.wallOf(ctx), "write", path))
}

func (r *Registry) admit(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	abs := r.rooted(path)

	if rule := firewall.Unplaced(abs); rule != nil {
		return "", "", &SubstrateError{Rule: rule}
	}
	resolved := resolveForContainment(abs)
	if dir = r.holder(resolved); dir == "" {
		return "", "", ErrOutsideSandbox
	}
	if err := r.substrateDenied(policy, op, abs); err != nil {
		return "", "", err
	}
	var inside bool
	if rel, inside = relativeWithin(dir, resolved); !inside {
		return "", "", ErrOutsideSandbox
	}
	return dir, rel, nil
}

func (r *Registry) undoTarget(s firewall.Stop) string {
	inHome := func(path string) bool {
		rel, inside := relativeWithin(r.sandbox, path)
		return inside && rel != "."
	}
	if s.Link {
		if inHome(s.Path) {
			return s.Path
		}
		return ""
	}
	for at := filepath.Dir(s.Path); inHome(at); at = filepath.Dir(at) {
		if info, err := os.Lstat(at); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return at
		}
	}
	return ""
}

func (r *Registry) undoTargets(unchecked *firewall.UncheckedError) []string {
	var links []string
	for _, s := range unchecked.Stops {
		if t := r.undoTarget(s); t != "" && !slices.Contains(links, t) {
			links = append(links, t)
		}
	}
	return links
}

func (r *Registry) undo(policy *firewall.Policy, unchecked *firewall.UncheckedError, path string) (link, rel string, wall *firewall.Policy, ok bool) {
	at, err := os.Lstat(r.rooted(path))
	if err != nil || at.Mode()&os.ModeSymlink == 0 {
		return "", "", nil, false
	}
	targets := r.undoTargets(unchecked)
	for _, t := range targets {
		if l, err := os.Lstat(t); err == nil && os.SameFile(at, l) {
			link = t
			break
		}
	}
	if link == "" {
		return "", "", nil, false
	}
	all := policy.Without(targets...)
	var deletable []string
	for _, t := range targets {
		if _, _, err := r.admit(all, "write", t); err == nil {
			deletable = append(deletable, t)
		}
	}
	if !slices.Contains(deletable, link) {
		return "", "", nil, false
	}
	wall = policy.Without(deletable...)
	_, rel, err = r.admit(wall, "write", link)
	return link, rel, wall, err == nil
}

func (r *Registry) said(unchecked *firewall.UncheckedError) string {
	text := unchecked.Reason
	var told []string
	tell := func(clause string) {
		if !slices.Contains(told, clause) {
			told = append(told, clause)
			text += clause
		}
	}
	for _, s := range unchecked.Stops {
		if target := r.undoTarget(s); target != "" {
			if _, _, _, ok := r.undo(r.policy, unchecked, target); ok {
				rel, _ := relativeWithin(r.sandbox, target)
				tell("; " + rel + ", in your home, is a link that leads where the host cannot look: deleting it clears this")
				continue
			}
		}
		if rel, inside := relativeWithin(r.sandbox, s.Path); inside && rel != "." {
			tell("; " + rel + ", in your home, is where the host cannot look")
		}
	}
	return text
}

func (r *Registry) admitOwn(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	dir, rel, err = r.admitAs(policy, op, path)
	if !errors.Is(err, ErrOutsideSandbox) || !r.SandboxOff() {
		return dir, rel, err
	}
	abs := r.rooted(path)
	if err := r.substrateDenied(policy, op, abs); err != nil {
		return "", "", r.asTold(err)
	}
	resolved := resolveForContainment(abs)
	return filepath.Dir(resolved), filepath.Base(resolved), nil
}

func (r *Registry) holdsSubstrate(abs string) bool {
	resolved := resolveForContainment(abs)
	return r.holder(resolved) == "" && rootExposesSubstrate(resolved, r.sandbox, r.policy.Kept())
}

func (r *Registry) rooted(path string) string { return from(r.sandbox, path) }

func from(base, path string) string {
	if isRooted(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func (r *Registry) holder(resolved string) string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	best := ""
	for _, root := range r.extraRoots {
		if within(root, resolved) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" && within(r.sandbox, resolved) {
		best = r.sandbox
	}
	return best
}

func (r *Registry) rootTakenBy(target string) string {
	t := resolveForContainment(target)
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	for _, root := range append([]string{r.sandbox}, r.extraRoots...) {
		if within(t, resolveForContainment(root)) {
			return root
		}
	}
	return ""
}

func (r *Registry) Roots() (string, []string) {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	return r.sandbox, append([]string(nil), r.extraRoots...)
}

func within(root, abs string) bool {
	_, inside := relativeWithin(root, abs)
	return inside
}

func relativeWithin(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return rel, true
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", false
	}
	rel = "."
	for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
		if other, err := os.Stat(dir); err == nil && os.SameFile(info, other) {
			return rel, true
		}
		if filepath.Dir(dir) == dir {
			return "", false
		}
		rel = filepath.Join(filepath.Base(dir), rel)
	}
}

func resolveForContainment(abs string) string {
	return firewall.ResolvePath(abs)
}

func (r *Registry) inExtraRoot(path string) bool {
	h := r.holder(resolveForContainment(r.rooted(path)))
	return h != "" && h != r.sandbox
}

func (r *Registry) substrateDenied(policy *firewall.Policy, tool, path string) error {

	policy = policy.Look()

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	resolved := resolveForContainment(abs)

	for _, form := range distinct(resolved, abs) {
		if err := refused(policy.CoveredFor(tool, form)); err != nil {
			return err
		}
	}

	if r.inExtraRoot(path) {
		return nil
	}

	for _, form := range distinct(resolved, abs, path) {
		if v := policy.Check(tool, form); !v.Allowed {
			return refused(v.Rule, v.Err)
		}
	}
	return nil
}

func distinct(forms ...string) []string {
	var out []string
	for _, f := range forms {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

func (r *Registry) walkDenied(ctx context.Context, tool, path string) bool {
	if r.policy == nil {
		return false
	}
	policy := r.wallOf(ctx)
	if policy.Check(tool, path).Allowed {
		return false
	}
	if rule, err := policy.CoveredFor(tool, path); rule != nil || err != nil {
		return true
	}
	return !r.inExtraRoot(path)
}

func (r *Registry) isOutsideSandbox(path string) bool {
	return r.holder(resolveForContainment(r.rooted(path))) == ""
}
