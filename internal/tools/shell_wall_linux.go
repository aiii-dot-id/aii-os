//go:build linux && !android

package tools

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/oswall"
)

const shellWallSentenceLinux = " Commands run behind the operating system's wall: a place the host keeps is empty there, or refuses with 'Permission denied', and a write outside your home and granted roots fails with 'Read-only file system'. ps and kill reach only the processes your commands started; sudo does not run behind it."

func shellWallDescriptionNow() string {
	if !wallKeepsPlaces() {
		return ""
	}
	return shellWallSentenceLinux
}

func wallKeepsPlaces() bool { return !shellAccountRoot() }

const (
	shellWallNoBubblewrap = "bubblewrap is not installed; your operator can install the bubblewrap package"
	shellWallNoNsenter    = "nsenter is not installed; your operator can install the util-linux package"
	shellWallNotBuilt     = "bubblewrap could not build the wall on this host"
)

type wallNotBuilt struct{ observed string }

func (e *wallNotBuilt) Error() string { return shellWallNotBuilt + ": " + e.observed }

const holderReady = "event=walled"

var holderProgram = []string{"/bin/sh", "-c", "echo " + holderReady + "; read _"}

const enterProgram = `cd -- "$1" || exit; shift; exec "$@"`

type shellHolder struct {
	once  sync.Once
	state *holderState
}

type holderState struct {
	mu   sync.Mutex
	cur  *holder
	told map[string]bool
}

type holder struct {
	key     string
	cmd     *exec.Cmd
	input   *os.File
	ns      []*os.File
	pidNS   string
	exited  chan struct{}
	refs    int
	retired bool
}

func (t *ShellTool) holderState() *holderState {
	t.holder.once.Do(func() {
		s := &holderState{told: map[string]bool{}}
		t.holder.state = s
		runtime.AddCleanup(t, func(s *holderState) { s.end() }, s)
	})
	return t.holder.state
}

func (t *ShellTool) behindTheWall(w shellWall, dir, bin string, argv []string) (string, []string, []*os.File, func(), error) {
	s := t.holderState()
	if !wallKeepsPlaces() {
		s.tell("root", "shell.decision", "the shell of an identity run as root is not walled on Linux: it runs as it always has (the operator's ruling of 2026-10-06; docs/KNOWN_LIMITATIONS.md)")
		return bin, argv, nil, func() {}, nil
	}
	nsenter, err := exec.LookPath("nsenter")
	if err != nil {
		s.tell("nsenter", "shell.refusal", "the shell is refused: nsenter (util-linux) is not on this host's PATH, and the shell runs only behind its wall")
		return wallUnavailable(errors.New(shellWallNoNsenter))
	}
	h, err := s.acquire(t.sandbox, w)
	if err != nil {
		return wallUnavailable(err)
	}
	files := make([]*os.File, 0, len(h.ns))
	for _, f := range h.ns {
		d, err := dupFile(f)
		if err != nil {
			for _, g := range files {
				g.Close()
			}
			s.release(h)
			return wallUnavailable(&wallNotBuilt{observed: err.Error()})
		}
		files = append(files, d)
	}
	wrapped := []string{
		"--user=/proc/self/fd/3", "--mount=/proc/self/fd/4", "--pid=/proc/self/fd/5",
		"--root=/proc/self/fd/6", "--wd=/proc/self/fd/7",

		"--preserve-credentials",
		"--", "/bin/sh", "-c", enterProgram, "sh", dir, bin,
	}
	release := func() {
		for _, f := range files {
			f.Close()
		}
		s.release(h)
	}
	return nsenter, append(wrapped, argv...), files, release, nil
}

func wallUnavailable(cause error) (string, []string, []*os.File, func(), error) {
	return "", nil, nil, nil, cause
}

func dupFile(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), f.Name()), nil
}

func (t *ShellTool) endWall() { t.holder.end() }

func (h *shellHolder) end() {
	if h.state != nil {
		h.state.end()
	}
}

func (s *holderState) tell(kind, category, line string) {
	s.mu.Lock()
	told := s.told[kind]
	s.told[kind] = true
	s.mu.Unlock()
	if !told {
		logsink.Warn(category, "%s", line)
	}
}

func (s *holderState) acquire(home string, w shellWall) (*holder, error) {
	bwrap, err := oswall.Program()
	if err != nil {
		s.tell("bwrap", "shell.refusal", "the shell is refused: bubblewrap is not installed, and the shell runs only behind its wall (install the bubblewrap package)")
		return nil, errors.New(shellWallNoBubblewrap)
	}
	args, key := holderArgs(home, w)
	s.mu.Lock()
	if h := s.cur; h != nil && h.key == key && h.alive() {
		h.refs++
		s.mu.Unlock()
		return h, nil
	}
	s.mu.Unlock()
	h, err := startHolder(bwrap, args, key, w.settings.start())
	if err != nil {
		var nb *wallNotBuilt
		observed := err.Error()
		if errors.As(err, &nb) {
			observed = nb.observed
		}
		line := "the shell is refused: bubblewrap could not build its wall: " + observed
		if oswall.UsernsRestricted() && oswall.UsernsDenial(observed) {
			line += "\n" + oswall.ProfileRemedy("the shell")
		}
		s.tell("built:"+observed, "shell.refusal", line)
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.cur; cur != nil && cur.key == key && cur.alive() {

		h.stop()
		cur.refs++
		return cur, nil
	}
	if old := s.cur; old != nil {

		old.retired = true
		if old.refs == 0 {
			old.stop()
		}
		logsink.Info("shell.start", "the shell's wall was built again for what it now holds; jobs left running in the earlier one end with it")
	}
	h.refs = 1
	s.cur = h
	return h, nil
}

func (s *holderState) release(h *holder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h.refs--
	if h.retired && h.refs == 0 {
		h.stop()
	}
}

func (s *holderState) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.cur; h != nil {
		s.cur = nil
		h.retired = true
		if h.refs == 0 {
			h.stop()
		}
	}
}

func (h *holder) alive() bool {
	select {
	case <-h.exited:
		return false
	default:
		return true
	}
}

func (h *holder) stop() {
	h.input.Close()
	if h.cmd.Process != nil {

		if h.alive() {
			_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = h.cmd.Process.Kill()
	}
	for _, f := range h.ns {
		f.Close()
	}
}

func startHolder(bwrap string, args []string, key string, within time.Duration) (*holder, error) {
	infoR, infoW, err := os.Pipe()
	if err != nil {
		return nil, &wallNotBuilt{observed: err.Error()}
	}
	defer infoR.Close()
	outR, outW, err := os.Pipe()
	if err != nil {
		infoW.Close()
		return nil, &wallNotBuilt{observed: err.Error()}
	}
	defer outR.Close()
	inR, inW, err := os.Pipe()
	if err != nil {
		infoW.Close()
		outW.Close()
		return nil, &wallNotBuilt{observed: err.Error()}
	}
	stderr := &cappedBuffer{cap: 4096}
	cmd := exec.Command(bwrap, append(args, append([]string{"--"}, holderProgram...)...)...)
	cmd.ExtraFiles = []*os.File{infoW}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = stderr

	cmd.Env = []string{"PATH=" + shellPath(runtime.GOOS)}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = holderWaitDelay
	err = cmd.Start()
	infoW.Close()
	outW.Close()
	inR.Close()
	if err != nil {
		inW.Close()
		return nil, &wallNotBuilt{observed: err.Error()}
	}
	h := &holder{key: key, cmd: cmd, input: inW, exited: make(chan struct{})}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait(); close(h.exited) }()
	ready := make(chan bool, 1)
	go func() {
		line, _ := bufio.NewReader(outR).ReadString('\n')
		ready <- strings.TrimSpace(line) == holderReady
	}()
	fail := func(why string) (*holder, error) {
		h.stop()
		<-h.exited
		if words := strings.TrimSpace(stderr.buf.String()); words != "" {
			why = words
		}
		return nil, &wallNotBuilt{observed: why}
	}
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case ok := <-ready:
		if !ok {

			<-h.exited
			return fail("it ended before the wall was built")
		}
	case <-timer.C:
		return fail(fmt.Sprintf("it was not built within %s", within))
	}
	var info struct {
		ChildPID int `json:"child-pid"`
	}
	if err := json.NewDecoder(infoR).Decode(&info); err != nil || info.ChildPID <= 0 {
		return fail("it did not say which process holds it")
	}
	proc := "/proc/" + strconv.Itoa(info.ChildPID)
	for _, name := range []string{"ns/user", "ns/mnt", "ns/pid", "root", "cwd"} {
		f, err := os.Open(filepath.Join(proc, name))
		if err != nil {
			return fail(err.Error())
		}
		h.ns = append(h.ns, f)
	}
	if h.pidNS, err = os.Readlink(filepath.Join(proc, "ns", "pid")); err != nil {
		return fail(err.Error())
	}

	if !h.alive() {
		return fail("it ended as it was entered")
	}
	return h, nil
}

const holderWaitDelay = 2 * time.Second

func holderArgs(home string, w shellWall) ([]string, string) {
	set := w.settings
	view := shellWallView()
	args := []string{
		"--unshare-user", "--unshare-pid",
		"--die-with-parent",

		"--new-session",
		"--info-fd", "3",
	}

	args = append(args, oswall.View(view, !w.boundary)...)
	args = append(args, wallDevices(set.devices())...)
	args = append(args, "--proc", "/proc")
	layers, writable := shellLayers(home, w)
	args = append(args, oswall.Args(layers, oswall.Shown(view, writable))...)
	var key strings.Builder
	key.WriteString(strings.Join(args, "\x00"))
	for _, l := range layers {
		if l.Source != "" {
			continue
		}
		if info, err := os.Lstat(l.Path); err == nil && info.Mode().IsRegular() {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				fmt.Fprintf(&key, "\x00%s=%d:%d", l.Path, st.Dev, st.Ino)
			}
		}
	}
	return args, key.String()
}

func shellLayers(home string, w shellWall) (layers []oswall.Layer, writable []string) {
	if w.boundary {
		for _, p := range append(append(slices.Clone(shellWallTemp), home), w.roots...) {
			if p == "" || !filepath.IsAbs(p) {
				continue
			}
			p = firewall.ResolvePath(p)
			layers = append(layers, oswall.Bind(p, true))
			writable = append(writable, p)
		}
	}
	for _, r := range resolvedPlaces(w.kept) {

		if r.Path == processTable {
			continue
		}
		for _, p := range stemPlaces(r) {
			if r.ReadOpen {
				layers = append(layers, oswall.Bind(p, false))
				continue
			}
			layers = append(layers, oswall.Mask(p))
		}
		for _, e := range r.Except {
			if oswall.ExceptionInPlace(r.Path, e) {
				layers = append(layers, oswall.Bind(e, true))
			}
		}
	}
	layers = wholeDirectories(layers, writable)
	oswall.Sort(layers)
	return layers, writable
}

func wholeDirectories(layers []oswall.Layer, writable []string) []oswall.Layer {
	masked := map[string]bool{}
	byDir := map[string][]string{}
	for _, l := range layers {
		if l.Source != "" {
			continue
		}
		if info, err := os.Lstat(l.Path); err == nil && info.Mode().IsRegular() {
			masked[l.Path] = true
			byDir[filepath.Dir(l.Path)] = append(byDir[filepath.Dir(l.Path)], l.Path)
		}
	}
	whole := map[string]bool{}
	for dir := range byDir {
		if dir == "/" || masked[dir] {
			continue
		}
		above := false
		for _, w := range writable {
			above = above || oswall.Within(dir, w)
		}
		for _, l := range layers {

			above = above || l.Source != "" && oswall.Below(dir, l.Path)
		}
		if above {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			continue
		}
		all := true
		for _, e := range entries {
			all = all && masked[filepath.Join(dir, e.Name())]
		}
		if all {
			whole[dir] = true
		}
	}
	if len(whole) == 0 {
		return layers
	}
	out := layers[:0:0]
	for _, l := range layers {
		if l.Source == "" && whole[filepath.Dir(l.Path)] && masked[l.Path] {
			continue
		}
		out = append(out, l)
	}
	for dir := range whole {
		out = append(out, oswall.Mask(dir))
	}
	return out
}

const processTable = "/proc"

func resolvedPlaces(rules []*firewall.Rule) []*firewall.Rule {
	out := make([]*firewall.Rule, 0, len(rules))
	for _, r := range rules {
		if r == nil || r.Path == "" || !filepath.IsAbs(r.Path) {
			continue
		}
		c := *r
		c.Path = firewall.ResolvePath(r.Path)
		if r.Stem {
			c.Path = firewall.ResolveSlot(r.Path)
		}
		c.Except = nil
		for _, e := range r.Except {
			if e == "" || r.Stem {
				continue
			}
			if x := firewall.ResolveSlot(e); oswall.Below(c.Path, x) {
				c.Except = append(c.Except, x)
			}
		}
		out = append(out, &c)
	}
	return out
}

func stemPlaces(r *firewall.Rule) []string {
	if !r.Stem {
		return []string{r.Path}
	}
	dir, prefix := filepath.Split(r.Path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func wallDevices(nodes []string) []string {
	args := []string{"--tmpfs", "/dev"}
	for _, n := range nodes {
		if !filepath.IsAbs(n) || !oswall.Within("/dev", filepath.Clean(n)) || filepath.Clean(n) == "/dev" {
			continue
		}
		n = filepath.Clean(n)
		if info, err := os.Stat(n); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			args = append(args, "--dev-bind", n, n)
		}
	}
	return append(args,
		"--tmpfs", "/dev/shm",
		"--symlink", "/proc/self/fd", "/dev/fd",
		"--symlink", "/proc/self/fd/0", "/dev/stdin",
		"--symlink", "/proc/self/fd/1", "/dev/stdout",
		"--symlink", "/proc/self/fd/2", "/dev/stderr",
		"--remount-ro", "/dev")
}

func shellWallView() []string { return []string{"/"} }

var shellWallTemp = []string{"/tmp", "/var/tmp"}

func DefaultShellWallDevices() []string {
	return []string{"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty"}
}

func (s ShellWallSettings) devices() []string {
	devices := DefaultShellWallDevices()
	for _, d := range s.Devices {
		if !slices.Contains(devices, d) {
			devices = append(devices, d)
		}
	}
	return devices
}

func (s ShellWallSettings) start() time.Duration { return oswall.StartWait(s.StartSeconds) }
