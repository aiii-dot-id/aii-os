//go:build !windows

package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// chromiumNetLog keeps Chromium's own record of its requests. Nothing else names
// ERR_NETWORK_CHANGED: Chrome's console and its logging switches do not say it.
func chromiumNetLog(path string) []string { return []string{"--log-net-log=" + path} }

// errNetworkChanged is Chromium's ERR_NETWORK_CHANGED.
const errNetworkChanged = -21

// requestAliveType finds the number Chromium's log gives a request's life. Its
// first line names every event's number, and they differ between versions.
var requestAliveType = regexp.MustCompile(`"REQUEST_ALIVE":(\d+)`)

// lostRequest is one request of the page's that Chromium refused for a network
// change, and that nothing answered. id is its request in the net log.
type lostRequest struct {
	url string
	id  int64
}

// lostPage is an attempt whose page never reported, with requests of its last visit
// lost. required holds what the page cannot run without: only those say why it was
// silent. other holds the rest, which are said and explain nothing.
type lostPage struct {
	required, other []lostRequest
	process, group  string
	files           string
	said            string
	served          string
}

func (p *lostPage) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "page never reported a result within the timeout, and Chrome refused %d of what it needs with ERR_NETWORK_CHANGED, with nothing answering them:\n", len(p.required))
	for _, r := range p.required {
		fmt.Fprintf(&b, "  %s (request %d)\n", r.url, r.id)
	}
	if len(p.other) > 0 {
		fmt.Fprintf(&b, "and %d more it can run without:\n", len(p.other))
		for _, r := range p.other {
			fmt.Fprintf(&b, "  %s (request %d)\n", r.url, r.id)
		}
	}
	fmt.Fprintf(&b, "the browser process %s and its process group was %s; %sthe browser said:\n%s", p.process, p.group, p.files, p.said)
	if p.served != "" {
		fmt.Fprintf(&b, "\nthe page's server answered:\n%s", p.served)
	}
	return b.String()
}

// lostToNetworkChange reads the page's own requests out of the net log, from its last
// visit: a reload starts again, so an import answered earlier does not hide one lost
// after it. A match elsewhere in the log can be any request of the browser's and says
// nothing, and a log killed with its browser ends mid-line; its whole lines are enough.
//
// THE RULE FOR A RERUN. It is admitted when the document, or a script or sheet on the
// page's origin, went unanswered with ERR_NETWORK_CHANGED on the last visit. Anything
// else lost, an image or a report, is named in the failure and admits nothing. A page
// that hangs on its own while such a request is lost can therefore pass on its second
// attempt. That risk is owned: every rerun prints its lost addresses and request ids,
// the gate's owner reads each one at the gate's end, and a rerun whose lost addresses
// include nothing the visited page imports is reported as a failure, not a recovery.
// After 0.2.1 callers declare the imports they need, and unknown necessity fails.
func lostToNetworkChange(netLog, startURL string) (required, other []lostRequest) {
	if netLog == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(netLog)
	if err != nil {
		return nil, nil
	}
	start, err := url.Parse(startURL)
	if err != nil {
		return nil, nil
	}
	origin := start.Scheme + "://" + start.Host + "/"
	head, rest, _ := bytes.Cut(raw, []byte("\n"))
	named := requestAliveType.FindSubmatch(head)
	if named == nil {
		return nil, nil
	}
	alive, err := strconv.Atoi(string(named[1]))
	if err != nil {
		return nil, nil
	}
	urls := map[int64]string{}
	documents := map[int64]bool{}
	var refused []lostRequest
	answered := map[string]bool{}
	for _, line := range bytes.Split(rest, []byte("\n")) {
		var event struct {
			Params struct {
				URL         string `json:"url"`
				NetError    *int   `json:"net_error"`
				RequestType string `json:"request_type"`
			} `json:"params"`
			Phase  int `json:"phase"`
			Source struct {
				ID int64 `json:"id"`
			} `json:"source"`
			Type int `json:"type"`
		}
		if json.Unmarshal(bytes.TrimSuffix(line, []byte(",")), &event) != nil {
			continue
		}
		if event.Params.RequestType == "main frame" && strings.HasPrefix(event.Params.URL, origin) {
			urls, refused, answered = map[int64]string{event.Source.ID: event.Params.URL}, nil, map[string]bool{}
			documents[event.Source.ID] = true
			continue
		}
		if event.Type != alive {
			continue
		}
		const begins, ends = 1, 2
		switch event.Phase {
		case begins:
			if strings.HasPrefix(event.Params.URL, origin) && event.Params.URL != origin+"favicon.ico" {
				urls[event.Source.ID] = event.Params.URL
			}
		case ends:
			address, ours := urls[event.Source.ID]
			switch {
			case !ours:
			case event.Params.NetError == nil:
				answered[address] = true
			case *event.Params.NetError == errNetworkChanged:
				refused = append(refused, lostRequest{url: address, id: event.Source.ID})
			}
		}
	}
	for _, r := range refused {
		if answered[r.url] {
			continue
		}
		if address, _, _ := strings.Cut(r.url, "?"); documents[r.id] || strings.HasSuffix(address, ".js") || strings.HasSuffix(address, ".css") {
			required = append(required, r)
		} else {
			other = append(other, r)
		}
	}
	return required, other
}

const netLogPage = "http://127.0.0.1:18765/"

// netLogOf is a net log in Chromium's own shape, with its records as a run of the
// address-change proof wrote them, unanswered requests and all.
func netLogOf(events ...string) string {
	return `{"constants":{"logEventTypes":{"REQUEST_ALIVE":2}},"timeTickOffset":1},` + "\n" + `"events": [` + "\n" + strings.Join(events, "\n") + "\n"
}

func requestBegan(id int, address string) string {
	return fmt.Sprintf(`{"params":{"priority":"MEDIUM","traffic_annotation":101845102,"url":%q},"phase":1,"source":{"id":%d,"start_time":"1","type":1},"time":"1","type":2},`, address, id)
}

func documentAsked(id int, address string) string {
	return fmt.Sprintf(`{"params":{"initiator":"not an origin","load_flags":98560,"method":"GET","request_type":"main frame","url":%q},"phase":1,"source":{"id":%d,"start_time":"1","type":1},"time":"1","type":136},`, address, id)
}

func requestRefused(id int) string {
	return fmt.Sprintf(`{"params":{"net_error":-21},"phase":2,"source":{"id":%d,"start_time":"1","type":1},"time":"2","type":2},`, id)
}

func requestAnswered(id int) string {
	return fmt.Sprintf(`{"phase":2,"source":{"id":%d,"start_time":"1","type":1},"time":"2","type":2},`, id)
}

// WHAT COUNTS IS THE LAST VISIT'S OWN REQUEST, REFUSED AND NEVER ANSWERED, and only
// what the page needs can say why it was silent.
func TestANetLogNamesOnlyThePagesOwnLostRequests(t *testing.T) {
	module, sheet, image := netLogPage+"m18.js", netLogPage+"views/a.css", netLogPage+"mark.png"
	for _, c := range []struct {
		name            string
		log             string
		required, other []lostRequest
	}{
		{"a module refused", netLogOf(requestBegan(423, module), requestRefused(423)), []lostRequest{{module, 423}}, nil},
		{"a stylesheet refused", netLogOf(requestBegan(5, sheet), requestRefused(5)), []lostRequest{{sheet, 5}}, nil},
		{"a module with a query", netLogOf(requestBegan(6, module+"?v=1"), requestRefused(6)), []lostRequest{{module + "?v=1", 6}}, nil},
		{"the document refused", netLogOf(requestBegan(1, netLogPage), documentAsked(1, netLogPage), requestRefused(1)), []lostRequest{{netLogPage, 1}}, nil},
		{"a log that ends mid-line", netLogOf(requestBegan(423, module), requestRefused(423)) + `{"params":{"net_`, []lostRequest{{module, 423}}, nil},
		{"only an image refused", netLogOf(requestBegan(7, image), requestRefused(7)), nil, []lostRequest{{image, 7}}},
		{"a module and an image", netLogOf(requestBegan(7, image), requestRefused(7), requestBegan(8, module), requestRefused(8)), []lostRequest{{module, 8}}, []lostRequest{{image, 7}}},
		{"another origin's request", netLogOf(requestBegan(7, "http://clients2.google.com/time.js"), requestRefused(7)), nil, nil},
		{"answered by another request", netLogOf(requestBegan(1, module), requestRefused(1), requestBegan(2, module), requestAnswered(2)), nil, nil},
		{"answered, then lost after a reload", netLogOf(requestBegan(1, module), requestAnswered(1), requestBegan(2, netLogPage), documentAsked(2, netLogPage), requestAnswered(2), requestBegan(3, module), requestRefused(3)), []lostRequest{{module, 3}}, nil},
		{"lost, then answered after a reload", netLogOf(requestBegan(1, module), requestRefused(1), requestBegan(2, netLogPage), documentAsked(2, netLogPage), requestAnswered(2), requestBegan(3, module), requestAnswered(3)), nil, nil},
		{"an old visit's request ended after the reload", netLogOf(requestBegan(1, module), requestBegan(2, netLogPage), documentAsked(2, netLogPage), requestRefused(1)), nil, nil},
		{"another error", netLogOf(requestBegan(1, module), `{"params":{"net_error":-3},"phase":2,"source":{"id":1,"start_time":"1","type":1},"time":"2","type":2},`), nil, nil},
		{"the error on another event", netLogOf(requestBegan(1, module), `{"params":{"net_error":-21},"phase":2,"source":{"id":1,"start_time":"1","type":1},"time":"2","type":136},`), nil, nil},
		{"the browser's favicon", netLogOf(requestBegan(1, netLogPage+"favicon.ico"), requestRefused(1)), nil, nil},
		{"a log with no names", `{"constants":{}},` + "\n" + requestBegan(1, module) + "\n" + requestRefused(1), nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "net.json")
			if err := os.WriteFile(path, []byte(c.log), 0o600); err != nil {
				t.Fatal(err)
			}
			required, other := lostToNetworkChange(path, netLogPage)
			if fmt.Sprint(required) != fmt.Sprint(c.required) || fmt.Sprint(other) != fmt.Sprint(c.other) {
				t.Fatalf("needed %v and other %v, want %v and %v", required, other, c.required, c.other)
			}
		})
	}
	if required, other := lostToNetworkChange("", netLogPage); required != nil || other != nil {
		t.Fatalf("an engine with no net log lost %v and %v", required, other)
	}
	if required, other := lostToNetworkChange(filepath.Join(t.TempDir(), "never-written"), netLogPage); required != nil || other != nil {
		t.Fatalf("a log never written lost %v and %v", required, other)
	}
}

// runRecorder is failureRecorder that also keeps what the run logged.
type runRecorder struct {
	failureRecorder
	logs []string
}

func (r *runRecorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func mustSh(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for a browser")
	}
	return sh
}

// onPage is an address on the origin of the address the browser was given.
func onPage(page, path string) string {
	start, err := url.Parse(page)
	if err != nil {
		return page + "/" + path
	}
	return start.Scheme + "://" + start.Host + "/" + path
}

// visited is a client as a browser is, sent to the address the rig gave it: it keeps
// the cookie that gave it, and sends it wherever on that host it next reports.
func visited(page string) *http.Client {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if answer, err := client.Get(page); err == nil {
		answer.Body.Close()
	}
	return client
}

func reportFrom(client *http.Client, page string) {
	if answer, err := client.Post(onPage(page, "__result"), "text/plain", strings.NewReader("OK")); err == nil {
		answer.Body.Close()
	}
}

// reportBare is a report from no page at all.
func reportBare(page, verdict string) {
	if answer, err := http.Post(onPage(page, "__result"), "text/plain", strings.NewReader(verdict)); err == nil {
		answer.Body.Close()
	}
}

// A PAGE LOST TO A NETWORK CHANGE IS RUN ONCE MORE, IN A FRESH BROWSER WITH A SERVER
// OF ITS OWN, AND BOTH ATTEMPTS ARE IN THE TEST'S LOG. A second loss is the test's
// failure, and nothing of the first attempt can pass for the second.
func TestAPageLostToANetworkChangeRunsOnceMoreAndSaysSo(t *testing.T) {
	lost := func(page string) string {
		return netLogOf(requestBegan(423, onPage(page, "m18.js")), requestRefused(423))
	}
	t.Run("the second attempt reports for itself", func(t *testing.T) {
		made, rec := lostRun(t, lost, func(page string) { reportFrom(visited(page), page) })
		if rec.failed != "" || made != 2 {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
		said := strings.Join(rec.logs, "\n")
		for _, want := range []string{"the first attempt was lost to a network change", "m18.js (request 423)", "the second attempt, in a fresh browser, passed"} {
			if !strings.Contains(said, want) {
				t.Errorf("the log does not say %q:\n%s", want, said)
			}
		}
	})
	t.Run("the first attempt's report comes late to a silent second", func(t *testing.T) {
		// It reaches the second attempt's own server, as it could if the second were
		// given the first's port, carrying the first attempt's token.
		first := make(chan *http.Client, 1)
		made, rec := lostRunWith(t, lost, func(page string) { first <- visited(page) }, func(page string) {
			reportFrom(<-first, page)
		})
		if made != 2 || !strings.Contains(rec.failed, "page never reported") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
	t.Run("a report with no token is refused", func(t *testing.T) {
		made, rec := lostRun(t, lost, func(page string) { reportBare(page, "OK") })
		if made != 2 || !strings.Contains(rec.failed, "page never reported") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
	t.Run("refused reports queue nothing, and the page's own still counts", func(t *testing.T) {
		made, rec := lostRun(t, lost, func(page string) {
			for range 8 {
				reportBare(page, "FAIL: from no page at all")
			}
			reportFrom(visited(page), page)
		})
		if rec.failed != "" || made != 2 {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
	t.Run("a late first page cannot take the second's token by asking for its files", func(t *testing.T) {
		// The first page, on the second's port, asks for what the second serves: the
		// harness, a module it does not stub, and the page itself in place of a sheet.
		first := make(chan *http.Client, 1)
		made, rec := lostRunWith(t, lost, func(page string) { first <- visited(page) }, func(page string) {
			late := <-first
			for _, file := range []string{"__harness.js", "app.js", "", "missing.css"} {
				if answer, err := late.Get(onPage(page, file)); err == nil {
					answer.Body.Close()
				}
			}
			reportFrom(late, page)
		})
		if made != 2 || !strings.Contains(rec.failed, "page never reported") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
	t.Run("the second attempt is lost too", func(t *testing.T) {
		made, rec := lostRun(t, lost, nil, lost)
		if made != 2 || !strings.Contains(rec.failed, "was lost too") || !strings.Contains(rec.failed, "m18.js (request 423)") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
	t.Run("the second attempt is silent for its own reason", func(t *testing.T) {
		made, rec := lostRun(t, lost, nil)
		if made != 2 || !strings.Contains(rec.failed, "page never reported a result within the timeout; the browser process") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
		if said := strings.Join(rec.logs, "\n"); !strings.Contains(said, "the first attempt was lost to a network change") {
			t.Errorf("the first attempt is not in the log:\n%s", said)
		}
	})
}

// lostRun runs a first attempt whose log the given function writes for its page, and
// a second that does what second says, leaving secondLog if one is given.
func lostRun(t *testing.T, firstLog func(string) string, second func(string), secondLog ...func(string) string) (int, *runRecorder) {
	t.Helper()
	return lostRunWith(t, firstLog, nil, second, secondLog...)
}

func lostRunWith(t *testing.T, firstLog func(string) string, first, second func(string), secondLog ...func(string) string) (int, *runRecorder) {
	t.Helper()
	dir, made, logPath := t.TempDir(), 0, ""
	was := browserBudget
	browserBudget = 400 * time.Millisecond
	t.Cleanup(func() { browserBudget = was })
	stand := browserEngine{
		name:   "stands-in",
		netLog: func(path string) []string { logPath = path; return nil },
		args: func(_, page string) []string {
			made++
			log, run := "", second
			if made == 1 {
				log, run = firstLog(page), first
			} else if len(secondLog) > 0 {
				log = secondLog[0](page)
			}
			written := filepath.Join(dir, fmt.Sprintf("attempt-%d", made))
			if err := os.WriteFile(written, []byte(log), 0o600); err != nil {
				t.Error(err)
			}
			if run != nil {
				go run(page)
			}
			return []string{"-c", `cp "$0" "$1"; sleep 60`, written, logPath}
		},
	}
	rec := &runRecorder{failureRecorder: failureRecorder{TB: t}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPageInOneEngine(rec, stand, mustSh(t), "<!doctype html>", nil, nil, 0, 0, 0)
	}()
	<-done
	return made, rec
}

// ANY OTHER SILENCE STAYS THE FAILURE IT IS, at once. So does a loss the rig cannot
// say stopped the page, and one on a server the caller keeps, which names the loss.
func TestAPageSilentForAnyOtherReasonIsNotRunAgain(t *testing.T) {
	for name, log := range map[string]func(string) string{
		"no net log": func(string) string { return "" },
		"only the browser's request": func(string) string {
			return netLogOf(requestBegan(7, "http://clients2.google.com/time.js"), requestRefused(7))
		},
	} {
		t.Run(name, func(t *testing.T) {
			made, rec := lostRun(t, log, nil)
			if made != 1 || !strings.Contains(rec.failed, "page never reported a result within the timeout; the browser process") {
				t.Fatalf("%d attempts, failed with %q", made, rec.failed)
			}
		})
	}
	t.Run("only an image was lost", func(t *testing.T) {
		made, rec := lostRun(t, func(page string) string {
			return netLogOf(requestBegan(7, onPage(page, "mark.png")), requestRefused(7))
		}, nil)
		if made != 1 || !strings.Contains(rec.failed, "mark.png (request 7)") || !strings.Contains(rec.failed, "it can run without") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
	t.Run("the caller keeps the server", func(t *testing.T) {
		dir, made, logPath := t.TempDir(), 0, ""
		was := browserBudget
		browserBudget = 400 * time.Millisecond
		t.Cleanup(func() { browserBudget = was })
		stand := browserEngine{
			name:   "stands-in",
			netLog: func(path string) []string { logPath = path; return nil },
			args: func(_, page string) []string {
				made++
				written := filepath.Join(dir, "attempt")
				if err := os.WriteFile(written, []byte(netLogOf(requestBegan(423, onPage(page, "m18.js")), requestRefused(423))), 0o600); err != nil {
					t.Error(err)
				}
				return []string{"-c", `cp "$0" "$1"; sleep 60`, written, logPath}
			},
		}
		rec := &runRecorder{failureRecorder: failureRecorder{TB: t}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			runServerInOneEngine(rec, stand, mustSh(t), "http://127.0.0.1:1/", make(chan string), 0, 0, 0)
		}()
		<-done
		if made != 1 || !strings.Contains(rec.failed, "m18.js (request 423)") {
			t.Fatalf("%d attempts, failed with %q", made, rec.failed)
		}
	})
}

// CHROME KEEPS THE LOG THE RERUN READS, and names its events at the top of it, in
// each engine that says it can.
func TestChromiumKeepsTheNetLogTheRigReads(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	for _, found := range engines {
		if found.engine.netLog == nil {
			continue
		}
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			results := make(chan string, 1)
			engine := found.engine
			var logPath atomic.Pointer[string] // written here, read by the page's handler
			engine.netLog = func(path string) []string { logPath.Store(&path); return found.engine.netLog(path) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/__result" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					fmt.Fprint(w, `<!doctype html><script>fetch('/__result', { method: 'POST', body: 'reported' });</script>`)
					return
				}
				raw, err := os.ReadFile(*logPath.Load())
				switch {
				case err != nil:
					results <- "the browser kept no net log: " + err.Error()
				case !requestAliveType.Match(raw):
					results <- "the net log does not name its events"
				default:
					results <- "OK"
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			runServerInOneEngine(t, engine, found.path, server.URL, results, 0, 0, 0)
		})
	}
}

// A BROWSER HAS THE OPEN FILES IT NEEDS, whatever the shell that started the tests
// allows, and a higher limit is not lowered to get there. Each case is a test binary
// of its own, started under the limit named, since the limit is the process's.
func TestABrowserInheritsEnoughOpenFiles(t *testing.T) {
	if os.Getenv("AII_RIG_FILES_CHILD") == "silent" {
		browserBudget = 300 * time.Millisecond
		rec := &runRecorder{failureRecorder: failureRecorder{TB: t}}
		stand := browserEngine{name: "stands-in", args: func(string, string) []string { return []string{"-c", "sleep 60"} }}
		done := make(chan struct{})
		go func() {
			defer close(done)
			runServerInOneEngine(rec, stand, mustSh(t), "http://127.0.0.1:1/", make(chan string), 0, 0, 0)
		}()
		<-done
		fmt.Printf("IT SAID %s\n", rec.failed)
		return
	}
	if os.Getenv("AII_RIG_FILES_CHILD") != "" {
		out := filepath.Join(t.TempDir(), "limit")
		results := make(chan string, 1)
		stand := browserEngine{
			name: "stands-in",
			args: func(string, string) []string {
				return []string{"-c", `ulimit -n > "$0.part"; mv "$0.part" "$0"; sleep 60`, out}
			},
		}
		go func() {
			for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) { // waits by design: the stand-in has no other way to say it has written
				if _, err := os.Stat(out); err == nil {
					break
				}
			}
			results <- "OK"
		}()
		runServerInOneEngine(t, stand, mustSh(t), "http://127.0.0.1:1/", results, 0, 0, 0)
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("the stand-in said nothing: %v", err)
		}
		fmt.Printf("A BROWSER HOLDS %s\n", strings.TrimSpace(string(raw)))
		return
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	holds := func(t *testing.T, soft uint64) uint64 {
		t.Helper()
		child := exec.Command(mustSh(t), "-c", fmt.Sprintf(`ulimit -S -n %d && exec "$0" -test.run='^TestABrowserInheritsEnoughOpenFiles$' -test.count=1`, soft), os.Args[0])
		child.Env = append(os.Environ(), "AII_RIG_FILES_CHILD=1")
		out, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("the child under %d: %v\n%s", soft, err, out)
		}
		said := regexp.MustCompile(`A BROWSER HOLDS (\S+)`).FindSubmatch(out)
		if said == nil {
			t.Fatalf("the child under %d said nothing:\n%s", soft, out)
		}
		if string(said[1]) == "unlimited" {
			return math.MaxUint64
		}
		got, err := strconv.ParseUint(string(said[1]), 10, 64)
		if err != nil {
			t.Fatalf("the child under %d said %q", soft, said[1])
		}
		return got
	}
	t.Run("a low limit is raised to the floor", func(t *testing.T) {
		low := min(1024, limit.Max)
		if got, want := holds(t, low), min(browserFiles, limit.Max); got < want {
			t.Fatalf("under a shell's %d a browser may hold %d open files; the rig means at least %d", low, got, want)
		}
	})
	t.Run("a floor it cannot reach is said", func(t *testing.T) {
		hard := min(2048, limit.Max)
		child := exec.Command(mustSh(t), "-c", fmt.Sprintf(`ulimit -n %d && exec "$0" -test.run='^TestABrowserInheritsEnoughOpenFiles$' -test.count=1`, hard), os.Args[0])
		child.Env = append(os.Environ(), "AII_RIG_FILES_CHILD=silent")
		out, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("the child: %v\n%s", err, out)
		}
		if want := fmt.Sprintf("a browser may hold only %d open files", hard); !strings.Contains(string(out), want) {
			t.Fatalf("a silent page under a hard limit of %d does not say so:\n%s", hard, out)
		}
	})
	t.Run("a higher limit is not lowered", func(t *testing.T) {
		const high = 200_000
		if limit.Max < high {
			t.Skipf("the hard limit here is %d", limit.Max)
		}
		if got := holds(t, high); got < high {
			t.Fatalf("under %d a browser may hold only %d open files", high, got)
		}
	})
}
