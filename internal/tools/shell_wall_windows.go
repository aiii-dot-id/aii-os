//go:build windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"

	"golang.org/x/sys/windows"
)

const shellWallDescription = " Commands run behind the operating system's wall: one that reaches a place the host keeps fails there with 'Access is denied'."

func shellWallDescriptionNow() string { return shellWallDescription }

func wallKeepsPlaces() bool { return false }

const SandboxLabelNote = "On Windows their home and each folder listed here carry a low-integrity label, so that their shell can write them; any program of this account that runs at low integrity can write them too. The label comes off a folder when you remove it from this list."

const (
	wallNoToken            = "the host could not give the shell its low-integrity token"
	wallNoJob              = "the host could not start the shell inside its job"
	wallNoLabel            = "a place the host keeps could not be labelled"
	wallKeptInLow          = "a file the host keeps lies directly in a folder your shell may write"
	wallKeptReadableParent = "a secret the host keeps would become readable when replaced in its folder"
	wallKeptRemedy         = "; your operator can move it"
	wallNoSession          = "the shell's wall needs an interactive Windows session (a desktop sign-in), and this host runs in a service session, where a program at low integrity cannot start"
	wallOnShare            = "a folder on a network share cannot be reached from behind the shell's wall on Windows"
	wallOnShareFix         = "; your operator can give you a local folder, or turn the sandbox off"
)

type shellHolder struct {
	mu       sync.Mutex
	held     map[string]windows.Handle
	labelled map[string]fileperm.Label
	told     map[string]bool
}

func (h *shellHolder) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for p, hd := range h.held {
		_ = windows.CloseHandle(hd)
		delete(h.held, p)
	}
}

func (h *shellHolder) tell(key, format string, args ...any) {
	if h.told == nil {
		h.told = map[string]bool{}
	}
	if h.told[key] {
		return
	}
	h.told[key] = true
	logsink.Warn("shell.refusal", format, args...)
}

func (t *ShellTool) behindTheWall(w shellWall, dir, bin string, argv []string) (string, []string, []*os.File, func(), error) {
	if !w.boundary {
		return bin, argv, nil, func() {}, nil
	}
	if inServiceSession() {
		return "", nil, nil, nil, errors.New(wallNoSession)
	}
	for _, p := range []string{dir, t.sandbox} {
		if onShare(p) {
			return "", nil, nil, nil, fmt.Errorf("%s: %s%s", wallOnShare, p, wallOnShareFix)
		}
	}
	if err := t.holder.ensure(t.sandbox, w); err != nil {
		return "", nil, nil, nil, err
	}
	return bin, argv, nil, func() {}, nil
}

func inServiceSession() bool {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return false
	}
	return session == 0
}

func onShare(path string) bool {
	v := filepath.VolumeName(path)
	switch {
	case len(v) >= 8 && strings.EqualFold(v[:8], `\\?\UNC\`):
		return true
	case strings.HasPrefix(v, `\\?\`), strings.HasPrefix(v, `\\.\`):
		return false
	}
	return strings.HasPrefix(v, `\\`) || strings.HasPrefix(v, `//`)
}

func releaseGrant(root string, still []string) {
	for _, s := range still {
		if s == "" {
			continue
		}
		if rel, err := filepath.Rel(s, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
	}
	if err := fileperm.UnlabelTree(root, fileperm.LabelIdentity); err != nil {
		logsink.Warn("shell.refusal", "the Low label of %s, whose grant was removed, could not be taken off: %v", root, err)
	}
}

func (t *ShellTool) endWall() { t.holder.end() }

func (h *shellHolder) ensure(home string, w shellWall) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil {
		h.held = map[string]windows.Handle{}
		h.labelled = map[string]fileperm.Label{}
	}

	if !filepath.IsAbs(home) {
		return fmt.Errorf("%s: the identity's home %q is not a full path", wallNoLabel, home)
	}
	low := []string{filepath.Clean(home)}
	for _, r := range w.roots {
		if !filepath.IsAbs(r) {
			return fmt.Errorf("%s: the granted root %q is not a full path", wallNoLabel, r)
		}
		if onShare(r) {
			h.tell("share "+r, "the shell's wall cannot reach %s: %s", r, wallOnShare)
			continue
		}
		low = append(low, filepath.Clean(r))
	}

	inLow := func(p string, secret bool) bool {
		parent := filepath.Dir(filepath.Clean(p))
		for _, l := range low {
			if strings.EqualFold(parent, l) {
				return true
			}
		}
		for _, l := range low {
			if within(l, parent) {

				for dir := parent; !strings.EqualFold(dir, l); dir = filepath.Dir(dir) {
					if yes, err := fileperm.HasLabel(dir, fileperm.LabelKept); err == nil && yes {
						return false
					}
					if yes, err := fileperm.HasLabel(dir, fileperm.LabelReadable); err == nil && yes {
						return secret
					}
					if yes, err := fileperm.HasLabel(dir, fileperm.LabelIdentity); err == nil && yes {
						return true
					}
				}
				return true
			}
		}
		lbl, err := fileperm.LabelOf(parent)
		return err == nil && (strings.HasSuffix(lbl, ";;;LW)") ||
			secret && strings.HasSuffix(lbl, ";NW;;;ME)"))
	}

	var except []string
	for _, r := range keptOnWindows(w.kept) {
		if r.Path == "" || r.Stem {
			continue
		}
		class := fileperm.LabelKept
		if r.ReadOpen || r.NativeReads {
			class = fileperm.LabelReadable
		}
		fi, err := os.Lstat(r.Path)
		if errors.Is(err, os.ErrNotExist) {

			if class != fileperm.LabelReadable || !inLow(r.Path, false) {
				continue
			}
			if err := os.Mkdir(r.Path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("%s: %s: %w", wallNoLabel, r.Path, err)
			}
			fi, err = os.Lstat(r.Path)
		}
		if err != nil {
			return fmt.Errorf("%s: %s: %w", wallNoLabel, r.Path, err)
		}
		if !fi.IsDir() && inLow(r.Path, class == fileperm.LabelKept) {
			why := wallKeptInLow
			if !inLow(r.Path, false) {
				why = wallKeptReadableParent
			}
			return fmt.Errorf("%s: %s%s", why, r.Path, wallKeptRemedy)
		}
		if err := h.label(r.Path, class, fi.IsDir()); err != nil {
			return fmt.Errorf("%s: %s: %w", wallNoLabel, r.Path, err)
		}
		if fi.IsDir() && inLow(r.Path, false) {
			if _, ok := h.held[r.Path]; !ok {
				hd, err := fileperm.Hold(r.Path)
				if err != nil {
					return fmt.Errorf("%s: %s: %w", wallNoLabel, r.Path, err)
				}
				h.held[r.Path] = hd
			}
		}
		except = append(except, r.Except...)
	}
	for _, p := range except {
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if err := h.label(p, fileperm.LabelIdentity, fi.IsDir()); err != nil {
			return fmt.Errorf("%s: %s: %w", wallNoLabel, p, err)
		}
	}
	for _, l := range low {
		if !fileperm.IsDirNotLink(l) {
			continue
		}
		if err := h.label(l, fileperm.LabelIdentity, true); err != nil {
			return fmt.Errorf("%s: %s: %w", wallNoLabel, l, err)
		}
	}
	return nil
}

func keptOnWindows(kept []*firewall.Rule) []*firewall.Rule {
	out := make([]*firewall.Rule, 0, len(kept))
	for _, r := range kept {
		if r == nil || r.ID != firewall.RuleOtherIdentity || r.Path == "" {
			out = append(out, r)
			continue
		}
		for _, sub := range []string{"data", "config"} {
			c := *r
			c.Path = filepath.Join(r.Path, sub)
			out = append(out, &c)
		}
	}

	for i := 1; i < len(out); i++ {
		parent := out[i]
		if parent == nil || parent.Path == "" || parent.Stem {
			continue
		}
		for j := 0; j < i; j++ {
			child := out[j]
			if child != nil && child.Path != "" && !strings.EqualFold(parent.Path, child.Path) && parent.Covers(child.Path) {
				copy(out[j+1:i+1], out[j:i])
				out[j] = parent
				break
			}
		}
	}
	return out
}

func (h *shellHolder) label(path string, l fileperm.Label, dir bool) error {
	ok, err := fileperm.HasLabel(path, l)
	if err != nil {
		return err
	}
	if ok {
		h.labelled[path] = l
		return nil
	}
	if !dir {
		if err := fileperm.SetLabel(path, l); err != nil {
			return err
		}
		h.labelled[path] = l
		return nil
	}
	if err := fileperm.LabelTree(path, l, func(p string, why error) {
		h.tell("skip "+p, "the shell's wall left %s as it was: %v", p, why)
	}); err != nil {
		return err
	}
	h.labelled[path] = l
	return nil
}

func wallProcess(cmd *exec.Cmd, w shellWall) (func(), error) {
	if !w.boundary {
		return func() {}, nil
	}
	tok, err := lowToken()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", wallNoToken, err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	return func() { _ = tok.Close() }, nil
}

var (
	modadvapi32               = windows.NewLazySystemDLL("advapi32.dll")
	procCreateRestrictedToken = modadvapi32.NewProc("CreateRestrictedToken")
)

const disableMaxPrivilege = 0x1

func lowToken() (windows.Token, error) {
	var own windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ASSIGN_PRIMARY, &own); err != nil {
		return 0, fmt.Errorf("open the host's token: %w", err)
	}
	defer own.Close()
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return 0, err
	}
	disable := []windows.SIDAndAttributes{{Sid: admins}}
	var restricted windows.Token
	r1, _, e1 := procCreateRestrictedToken.Call(uintptr(own), disableMaxPrivilege,
		uintptr(len(disable)), uintptr(unsafe.Pointer(&disable[0])), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r1 == 0 {
		return 0, fmt.Errorf("restrict the host's token: %w", e1)
	}
	low, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		restricted.Close()
		return 0, err
	}
	tml := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: low, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&tml)), tml.Size()); err != nil {
		restricted.Close()
		return 0, fmt.Errorf("lower the token to Low: %w", err)
	}
	if !isLow(restricted) {
		restricted.Close()
		return 0, errors.New("the token did not read back as Low")
	}
	return restricted, nil
}

func isLow(t windows.Token) bool {
	var n uint32
	_ = windows.GetTokenInformation(t, windows.TokenIntegrityLevel, nil, 0, &n)
	if n == 0 {
		return false
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenIntegrityLevel, &buf[0], n, &n); err != nil {
		return false
	}
	tml := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	return tml.Label.Sid.String() == "S-1-16-4096"
}
