//go:build !windows

package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

/*
A PAGE IS THE SIZE ITS TEST ASKS FOR, IN EVERY ENGINE, OR THE RUN FAILS.

The rig used to ask each browser for a window of the test's size, by a
launch flag. A window is not a page: Chrome and Firefox keep a window at
least 500 pixels wide (450 for Firefox on Linux), so every phone test at
390 ran at 500 in two engines of three, and the window's own frame came off
the height of every run, 600 becoming 457 in Chrome. Only WebKit, given a
viewport by Playwright, was the size asked.

So the page is sized through the engine's own channel, before its first
load, and read back when the run ends:

  Chrome, Edge  the devtools pipe (--remote-debugging-pipe, fds 3 and 4):
                each page waits at its start, is given the size
                (Emulation.setDeviceMetricsOverride) and is then let go,
                and the test's page opens as a new one
  Firefox       WebDriver BiDi (--remote-debugging-port=0): a new tab,
                its viewport set, then the page
  WebKit        Playwright's viewport, read back by webkit-playwright.mjs
                when the rig asks on its input

A page that is not the size asked fails its run, naming the engine and both
sizes. Each such engine starts at about:blank and opens the page itself.
*/

// sizeHolder is an engine's channel to the page it opens.
type sizeHolder interface {
	// open opens url as the run's page, already w by h at its first load.
	open(url string, w, h int) error
	// size is the page's innerWidth and innerHeight now.
	size() (int, int, error)
	close()
}

// channelBudget bounds one call on an engine's channel. It is for a loaded
// gate host, as the page's own budget is, not a product bound.
const channelBudget = 60 * time.Second

// heldSize checks the page against the size asked, once it has reported.
func heldSize(holder sizeHolder, engine string, w, h int) error {
	gw, gh, err := holder.size()
	if err != nil {
		return fmt.Errorf("%s: the page's size could not be read: %w", engine, err)
	}
	if gw != w || gh != h {
		return fmt.Errorf("%s: the rig asked for a page %dx%d and the page is %dx%d", engine, w, h, gw, gh)
	}
	return nil
}

// devtoolsPipe is Chrome's: --remote-debugging-pipe, a message of JSON
// ended by a NUL each way, the browser reading fd 3 and writing fd 4.
func devtoolsPipe(cmd *exec.Cmd, _ string) (func(<-chan struct{}) (sizeHolder, error), func(), error) {
	toBrowser, theirIn, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	theirOut, fromBrowser, err := os.Pipe()
	if err != nil {
		_ = toBrowser.Close()
		_ = theirIn.Close()
		return nil, nil, err
	}
	cmd.Args = append([]string{cmd.Args[0], "--remote-debugging-pipe"}, cmd.Args[1:]...)
	cmd.ExtraFiles = []*os.File{toBrowser, fromBrowser}
	release := func() {
		for _, f := range []*os.File{toBrowser, fromBrowser, theirIn, theirOut} {
			_ = f.Close()
		}
	}
	return func(<-chan struct{}) (sizeHolder, error) {
		// The browser holds its own ends now; a browser that goes closes
		// them, and every call then fails.
		_ = toBrowser.Close()
		_ = fromBrowser.Close()
		c := &cdp{out: theirIn, in: theirOut, waiting: map[int]chan cdpMessage{}, pages: map[string]string{}, events: make(chan cdpMessage, 64)}
		go c.read()
		go c.letGo()
		return c, nil
	}, release, nil
}

type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type cdp struct {
	out, in *os.File
	events  chan cdpMessage

	mu      sync.Mutex
	next    int
	waiting map[int]chan cdpMessage
	w, h    int
	pages   map[string]string // a page's target to its session
	page    string            // the run's page's target
	gone    error
}

func (c *cdp) read() {
	r := bufio.NewReader(c.in)
	for {
		raw, err := r.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			c.gone = fmt.Errorf("the devtools pipe closed: %w", err)
			for id, ch := range c.waiting {
				close(ch)
				delete(c.waiting, id)
			}
			c.mu.Unlock()
			close(c.events)
			return
		}
		var m cdpMessage
		if json.Unmarshal(raw[:len(raw)-1], &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.waiting[m.ID]
			delete(c.waiting, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method == "Target.attachedToTarget" {
			c.events <- m
		}
	}
}

func (c *cdp) call(method string, params any, session string) (json.RawMessage, error) {
	c.mu.Lock()
	if c.gone != nil {
		c.mu.Unlock()
		return nil, c.gone
	}
	c.next++
	id := c.next
	ch := make(chan cdpMessage, 1)
	c.waiting[id] = ch
	c.mu.Unlock()
	b, err := json.Marshal(struct {
		ID        int    `json:"id"`
		Method    string `json:"method"`
		Params    any    `json:"params"`
		SessionID string `json:"sessionId,omitempty"`
	}{id, method, params, session})
	if err != nil {
		return nil, err
	}
	if _, err := c.out.Write(append(b, 0)); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("%s: the devtools pipe closed", method)
		}
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	case <-time.After(channelBudget):
		return nil, fmt.Errorf("%s: no answer in %s", method, channelBudget)
	}
}

// letGo sizes each page as it starts and lets every target that waits go on.
func (c *cdp) letGo() {
	for m := range c.events {
		var a struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
			} `json:"targetInfo"`
			Waiting bool `json:"waitingForDebugger"`
		}
		if json.Unmarshal(m.Params, &a) != nil {
			continue
		}
		go func() {
			if a.TargetInfo.Type == "page" {
				c.mu.Lock()
				c.pages[a.TargetInfo.TargetID] = a.SessionID
				w, h := c.w, c.h
				c.mu.Unlock()
				if w > 0 {
					_, _ = c.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": 1, "mobile": false}, a.SessionID)
				}
			}
			if a.Waiting {
				_, _ = c.call("Runtime.runIfWaitingForDebugger", map[string]any{}, a.SessionID)
			}
		}()
	}
}

func (c *cdp) open(url string, w, h int) error {
	c.mu.Lock()
	c.w, c.h = w, h
	c.mu.Unlock()
	// Tabs are left out: the page in a tab attaches, and waits, itself.
	filter := []map[string]any{{"type": "tab", "exclude": true}, {}}
	if _, err := c.call("Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true, "filter": filter}, ""); err != nil {
		return err
	}
	res, err := c.call("Target.getTargets", map[string]any{}, "")
	if err != nil {
		return err
	}
	var targets struct {
		Infos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(res, &targets); err != nil {
		return err
	}
	res, err = c.call("Target.createTarget", map[string]any{"url": url}, "")
	if err != nil {
		return err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		return err
	}
	c.mu.Lock()
	c.page = created.TargetID
	c.mu.Unlock()
	// The blank page the browser started with goes, so the run's page is
	// the one page there is.
	for _, t := range targets.Infos {
		if t.Type == "page" && t.TargetID != created.TargetID {
			_, _ = c.call("Target.closeTarget", map[string]any{"targetId": t.TargetID}, "")
		}
	}
	return nil
}

func (c *cdp) size() (int, int, error) {
	c.mu.Lock()
	session := c.pages[c.page]
	c.mu.Unlock()
	if session == "" {
		return 0, 0, errors.New("the page never attached to the devtools pipe")
	}
	res, err := c.call("Runtime.evaluate", map[string]any{"expression": "[innerWidth, innerHeight]", "returnByValue": true}, session)
	if err != nil {
		return 0, 0, err
	}
	var v struct {
		Result struct {
			Value []int `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &v); err != nil || len(v.Result.Value) != 2 {
		return 0, 0, fmt.Errorf("Runtime.evaluate answered %s", res)
	}
	return v.Result.Value[0], v.Result.Value[1], nil
}

func (c *cdp) close() {
	_ = c.out.Close()
	_ = c.in.Close()
}

var bidiListening = regexp.MustCompile(`WebDriver BiDi listening on (ws://\S+)`)

// webDriverBiDi is Firefox's: --remote-debugging-port=0, the address read
// from what the browser writes to its output.
func webDriverBiDi(cmd *exec.Cmd, output string) (func(<-chan struct{}) (sizeHolder, error), func(), error) {
	cmd.Args = append([]string{cmd.Args[0], "--remote-debugging-port=0"}, cmd.Args[1:]...)
	return func(exited <-chan struct{}) (sizeHolder, error) {
		// waits by design: the address is in the browser's output once it
		// listens, and nothing else says when that is.
		deadline := time.Now().Add(channelBudget)
		var address string
		for address == "" {
			said, _ := os.ReadFile(output)
			if m := bidiListening.FindSubmatch(said); m != nil {
				address = string(m[1])
				break
			}
			select {
			case <-exited:
				return nil, fmt.Errorf("Firefox exited before WebDriver BiDi listened")
			default:
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("Firefox never said where WebDriver BiDi listens")
			}
			time.Sleep(20 * time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(context.Background(), channelBudget)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, address+"/session", nil)
		if err != nil {
			return nil, fmt.Errorf("WebDriver BiDi at %s: %w", address, err)
		}
		conn.SetReadLimit(1 << 20)
		b := &bidi{conn: conn, waiting: map[int]chan bidiMessage{}}
		go b.read()
		if _, err := b.call("session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
			b.close()
			return nil, err
		}
		return b, nil
	}, func() {}, nil
}

type bidiMessage struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

type bidi struct {
	conn *websocket.Conn

	mu      sync.Mutex
	next    int
	waiting map[int]chan bidiMessage
	tab     string
	gone    error
}

func (b *bidi) read() {
	for {
		_, raw, err := b.conn.Read(context.Background())
		if err != nil {
			b.mu.Lock()
			b.gone = fmt.Errorf("WebDriver BiDi closed: %w", err)
			for id, ch := range b.waiting {
				close(ch)
				delete(b.waiting, id)
			}
			b.mu.Unlock()
			return
		}
		var m bidiMessage
		if json.Unmarshal(raw, &m) != nil || m.ID == 0 {
			continue
		}
		b.mu.Lock()
		ch := b.waiting[m.ID]
		delete(b.waiting, m.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

func (b *bidi) call(method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.gone != nil {
		b.mu.Unlock()
		return nil, b.gone
	}
	b.next++
	id := b.next
	ch := make(chan bidiMessage, 1)
	b.waiting[id] = ch
	b.mu.Unlock()
	msg, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), channelBudget)
	defer cancel()
	if err := b.conn.Write(ctx, websocket.MessageText, msg); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("%s: WebDriver BiDi closed", method)
		}
		if m.Type == "error" {
			return nil, fmt.Errorf("%s: %s: %s", method, m.Error, m.Message)
		}
		return m.Result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: no answer in %s", method, channelBudget)
	}
}

func (b *bidi) open(url string, w, h int) error {
	res, err := b.call("browsingContext.getTree", map[string]any{})
	if err != nil {
		return err
	}
	var tree struct {
		Contexts []struct {
			Context string `json:"context"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(res, &tree); err != nil {
		return err
	}
	// A new tab, so the run's page is its first entry, as a page opened
	// from the command line is; the tab the browser started with goes.
	res, err = b.call("browsingContext.create", map[string]any{"type": "tab"})
	if err != nil {
		return err
	}
	var created struct {
		Context string `json:"context"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		return err
	}
	b.mu.Lock()
	b.tab = created.Context
	b.mu.Unlock()
	for _, c := range tree.Contexts {
		_, _ = b.call("browsingContext.close", map[string]any{"context": c.Context})
	}
	if _, err := b.call("browsingContext.setViewport", map[string]any{"context": created.Context, "viewport": map[string]int{"width": w, "height": h}}); err != nil {
		return err
	}
	_, err = b.call("browsingContext.navigate", map[string]any{"context": created.Context, "url": url, "wait": "none"})
	return err
}

func (b *bidi) size() (int, int, error) {
	b.mu.Lock()
	tab := b.tab
	b.mu.Unlock()
	res, err := b.call("script.evaluate", map[string]any{"expression": "innerWidth + 'x' + innerHeight", "target": map[string]string{"context": tab}, "awaitPromise": false})
	if err != nil {
		return 0, 0, err
	}
	var v struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	var w, h int
	if json.Unmarshal(res, &v) != nil {
		return 0, 0, fmt.Errorf("script.evaluate answered %s", res)
	}
	if _, err := fmt.Sscanf(v.Result.Value, "%dx%d", &w, &h); err != nil {
		return 0, 0, fmt.Errorf("script.evaluate answered %s", strings.TrimSpace(string(res)))
	}
	return w, h, nil
}

func (b *bidi) close() { _ = b.conn.CloseNow() }

var playwrightPage = regexp.MustCompile(`(?m)^(?:the page is (\d+)x(\d+)|the page's size could not be read: (.*))$`)

// playwrightSays is WebKit's: webkit-playwright.mjs sizes the page by
// Playwright's viewport, and says the size the page has when the rig asks on
// its input.
func playwrightSays(cmd *exec.Cmd, output string) (func(<-chan struct{}) (sizeHolder, error), func(), error) {
	theirs, ask, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdin = theirs
	connect := func(<-chan struct{}) (sizeHolder, error) {
		_ = theirs.Close() // the script holds its own end now
		return &playwrightOutput{ask: ask, output: output}, nil
	}
	release := func() {
		_ = theirs.Close()
		_ = ask.Close()
	}
	return connect, release, nil
}

type playwrightOutput struct {
	ask    *os.File
	output string
}

func (*playwrightOutput) open(string, int, int) error { return nil }

func (o *playwrightOutput) size() (int, int, error) {
	said, _ := os.ReadFile(o.output)
	before := len(playwrightPage.FindAllIndex(said, -1))
	if _, err := o.ask.WriteString("size\n"); err != nil {
		return 0, 0, fmt.Errorf("webkit-playwright.mjs could not be asked: %w", err)
	}
	// waits by design: the answer is in the script's output once it has read
	// the page, and nothing else says when that is.
	deadline := time.Now().Add(channelBudget)
	for {
		said, _ = os.ReadFile(o.output)
		if all := playwrightPage.FindAllSubmatch(said, -1); len(all) > before {
			last := all[len(all)-1]
			if len(last[3]) > 0 {
				return 0, 0, errors.New(string(last[3]))
			}
			var w, h int
			if _, err := fmt.Sscanf(string(last[1])+" "+string(last[2]), "%d %d", &w, &h); err != nil {
				return 0, 0, err
			}
			return w, h, nil
		}
		if time.Now().After(deadline) {
			return 0, 0, errors.New("webkit-playwright.mjs never said the page's size")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (o *playwrightOutput) close() { _ = o.ask.Close() }

// THE RIG HOLDS THE SIZE IT ASKS, in each engine: a phone's and a desktop's.
// A run that names no size is not held, and opens no channel (the test below
// this one). The page judges nothing
// and says what it measured; the rig's own read-back is the assertion, so a
// run whose page is another size fails in the rig's words.
func TestTheRigHoldsTheSizeItAsks(t *testing.T) {
	const page = `<!doctype html><meta charset="utf-8"><script type="module">
import { report } from '/__harness.js';
report('OK\n' + innerWidth + 'x' + innerHeight + ', phone rules ' + matchMedia('(max-width: 767px)').matches);
</script>`
	for _, size := range []struct{ w, h int }{{390, 844}, {375, 812}, {1280, 800}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			runPageInEnginesAtSize(t, page, nil, size.w, size.h)
		})
	}
}

// A RUN THAT NAMES NO SIZE OPENS NO CHANNEL, with a stand-in for a browser: its
// window is the browser's own, as it was before the rig held sizes, and the
// launch is the browser's alone. A channel opened for every launch was most of a
// gate's ~1,200 launches paying for a promise only 24 tests ask.
func TestARunThatNamesNoSizeOpensNoChannel(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for a browser")
	}
	opened := 0
	engine := browserEngine{
		name:     "unsized",
		args:     func(string, string) []string { return []string{"-c", "sleep 60"} },
		sizeArgs: func(int, int) []string { return nil },
		channel: func(*exec.Cmd, string) (func(<-chan struct{}) (sizeHolder, error), func(), error) {
			opened++
			return func(<-chan struct{}) (sizeHolder, error) { return slowHolder{}, nil }, func() {}, nil
		},
		opens: true,
	}
	rec := &failureRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runServerInOneEngine(rec, engine, sh, "http://127.0.0.1:1/", make(chan string), 0, 0, 200*time.Millisecond)
	}()
	<-done
	if opened != 0 {
		t.Fatalf("a run that names no size opened its channel %d time(s)", opened)
	}
	if rec.failed != "" {
		t.Fatalf("a run that names no size ended as %q", rec.failed)
	}
}

// A RUN'S CHANNEL SPENDS NEITHER ITS BUDGET NOR ITS INERT WINDOW, with a stand-in
// for a browser: an inert run whose channel takes longer to open than the run's
// budget is still watched for its whole window, and its page's size is still
// checked when the window ends. The budget used to start before the channel, so
// a slow channel could spend it, and an inert run then ended as a pass with no
// size read.
func TestAnInertRunsWindowStartsOnceItsChannelHasOpened(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for a browser")
	}
	was := browserBudget
	browserBudget = time.Second
	t.Cleanup(func() { browserBudget = was })
	slow := browserEngine{
		name:     "slow-channel",
		args:     func(string, string) []string { return []string{"-c", "sleep 60"} },
		sizeArgs: func(int, int) []string { return nil },
		channel: func(*exec.Cmd, string) (func(<-chan struct{}) (sizeHolder, error), func(), error) {
			return func(<-chan struct{}) (sizeHolder, error) { return slowHolder{}, nil }, func() {}, nil
		},
		opens: true,
	}
	rec := &failureRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runServerInOneEngine(rec, slow, sh, "http://127.0.0.1:1/", make(chan string), 390, 844, 300*time.Millisecond)
	}()
	<-done
	if !strings.Contains(rec.failed, "the rig asked for a page 390x844 and the page is 1x1") {
		t.Fatalf("an inert run whose channel outlasted the run's budget ended as %q, not on its page's size", rec.failed)
	}
}

// slowHolder opens its page after longer than the lowered budget, and the page
// is the wrong size.
type slowHolder struct{}

func (slowHolder) open(string, int, int) error {
	time.Sleep(2 * time.Second) // waits by design: a channel slower than the budget is the case
	return nil
}
func (slowHolder) size() (int, int, error) { return 1, 1, nil }
func (slowHolder) close()                  {}

// A BROWSER THAT CANNOT START LEAVES NONE OF ITS CHANNEL OPEN: the pipes made for
// it are closed when the run fails, not left to the collector.
func TestABrowserThatCannotStartLeavesItsChannelClosed(t *testing.T) {
	for _, c := range []struct {
		name    string
		channel func(*exec.Cmd, string) (func(<-chan struct{}) (sizeHolder, error), func(), error)
	}{{"the devtools pipe", devtoolsPipe}, {"WebKit's input", playwrightSays}} {
		t.Run(c.name, func(t *testing.T) {
			engine := browserEngine{name: "cannot-start", args: func(string, string) []string { return nil },
				sizeArgs: func(int, int) []string { return nil }, channel: c.channel, opens: true}
			missing := filepath.Join(t.TempDir(), "no-such-browser")
			before := openDescriptors(t)
			rec := &failureRecorder{TB: t}
			done := make(chan struct{})
			go func() {
				defer close(done)
				runServerInOneEngine(rec, engine, missing, "http://127.0.0.1:1/", make(chan string), 390, 844, 0)
			}()
			<-done
			if !strings.Contains(rec.failed, "failed to start") {
				t.Fatalf("a browser that cannot start was reported as %q", rec.failed)
			}
			if after := openDescriptors(t); after > before {
				t.Fatalf("a browser that could not start left %d of its channel's descriptors open", after-before)
			}
		})
	}
}

// openDescriptors counts this process's open file descriptors.
func openDescriptors(t *testing.T) int {
	t.Helper()
	fds, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skipf("this system lists no /dev/fd: %v", err)
	}
	return len(fds)
}
