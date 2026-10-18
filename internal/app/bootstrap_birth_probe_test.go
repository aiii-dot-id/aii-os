package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func firstbootApp(t *testing.T, dir string) *App {
	// Pre-birth surface: the genesis path validates the FORM fields
	// (endpoint/model/key travel raw on the request); the config LLM
	// pointer stays empty — it is written by a successful birth.
	a := New(&Config{
		Identity:   IdentityConfig{KeyPath: dir + "/id.sec", LedgerPath: dir + "/ledger.jsonl", DBPath: dir + "/aii.db"},
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		SourcePath: dir + "/config.json",
	})
	a.rings = ring.NewManager()
	a.ring0Content = "# Constitution\nHonesty."
	a.ring5Content = genesistest.NewRoot(t).Ring5(t)
	a.genesisClient = genesis.NewClient("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	return a
}

func fetchVerifiedBootstrap(t *testing.T, root *genesistest.Root) (*genesis.GenesisClient, string) {
	t.Helper()
	keyBundle, packet, _ := root.BootstrapArtifacts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bootstrap/pubkey.bundle":
			_, _ = w.Write(keyBundle)
		case "/bootstrap":
			_, _ = w.Write(packet)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := genesis.NewClient("", "", server.URL)
	result, err := client.FetchBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	return client, result.Content
}

// birthApp is an App at the Birth click: the founding artifacts verified
// and staged, and entry its one stored provider.
func birthApp(t *testing.T, entry providerEntry) *App {
	t.Helper()
	root := genesistest.NewRoot(t)
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	writeTestProviders(t, dir, entry)
	a := New(cfg)
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, root)
	a.ring0Content = "# Constitution\nHonesty."
	a.ring0Bundle = root.Ring0Bundle(t)
	a.ring5Content = root.Ring5(t)
	return a
}

func TestBirthRefusedWithoutVerifiedBootstrap(t *testing.T) {
	a := firstbootApp(t, t.TempDir())
	// bootstrapText empty: steps 3-4 never completed.
	_, err := a.handleGenesis(context.Background(), &dashboard.GenesisRequest{Provider: "Local"})
	if err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("a birth without the verified bootstrap packet must refuse naming bootstrap, got: %v", err)
	}
}

func TestBirthValidatesProviderFirst(t *testing.T) {
	a := firstbootApp(t, t.TempDir())
	root := genesistest.NewRoot(t)
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, root)
	// With the artifacts staged, the Birth click runs the operator's
	// four steps IN ORDER — and step 1 (provider validation) fires
	// first: the unreachable endpoint refuses before anything is
	// minted, proving the artifact gates cleared and validation leads.
	_, err := a.handleGenesis(context.Background(), &dashboard.GenesisRequest{
		Endpoint: "http://127.0.0.1:1", Model: "m", APIKey: "k",
	})
	if err == nil {
		t.Fatal("expected provider validation to refuse (unreachable endpoint) — a nil error means an identity was minted in a unit test")
	}
	if !strings.Contains(err.Error(), "provider validation failed") {
		t.Fatalf("the four steps start with provider validation, got: %v", err)
	}
}

func TestBirthCeremonyStillCompletes(t *testing.T) {
	testBirthLiveDashboard(t, false)
}

func TestBirthReusesFirstbootDashboardWithAllLiveSinks(t *testing.T) {
	testBirthLiveDashboard(t, true)
}

func testBirthLiveDashboard(t *testing.T, existingDashboard bool) {
	t.Helper()
	// The prompt is whatever the platform's own signed packet carries —
	// there is no fixture to pin it to any more.
	bootstrap := genesistest.NewRoot(t).BootstrapPrompt(t)

	prompt := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-birth-key" {
			http.Error(w, "missing stored provider key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
		case "/v1/chat/completions":
			var request struct {
				Messages []llm.Message `json:"messages"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 1 || request.Messages[0].Role != "system" {
				http.Error(w, "unexpected birth request", http.StatusBadRequest)
				return
			}
			prompt <- request.Messages[0].Content
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Mira."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	a := birthApp(t, providerEntry{
		Name: "Local", APIType: "openai", URL: provider.URL + "/v1",
		APIKey: "stored-birth-key", DefaultModel: "birth-model", Default: true,
	})
	defer stopApp(t, a)
	var first *dashboard.Server
	if existingDashboard {
		first = startFirstbootDashboardForTest(t, a)
	}

	greeting, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{
		Provider: "Local",
		Endpoint: provider.URL + "/v1", Model: "birth-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if greeting != "Hello, I am Mira." || !a.live.Load() || a.ledger == nil {
		t.Fatalf("birth did not reach live unchanged: greeting=%q live=%t ledger=%v", greeting, a.live.Load(), a.ledger != nil)
	}
	if got := a.store.IdentityName(); got != "Mira" {
		t.Fatalf("name minted from the founding reply: got %q, want %q", got, "Mira")
	}
	if a.ledger.LastSeq() != 1 {
		t.Fatalf("birth minted %d events, want only ring0.genesis", a.ledger.LastSeq())
	}
	identity, err := a.store.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.HasOperatorRelationship || identity.Charter != "" {
		t.Fatalf("birth must not mint Ring 1: %+v", identity)
	}
	composed, err := a.composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(composed.Text, "present a Ring 1 proposal for their affirmation or negation") {
		t.Fatal("post-birth prompt is missing the brief Ring 1 proposal reminder")
	}
	if got := <-prompt; got != bootstrap {
		t.Fatalf("verified bootstrap prompt changed: %q", got)
	}
	if first != nil && a.dashboard != first {
		t.Fatal("birth replaced the firstboot dashboard instead of wiring it")
	}
	assertLiveDashboardSinks(t, a)
}

func TestBootstrapNotFetchedWithoutVerifiedRing0(t *testing.T) {
	var bootstrapHits atomic.Int64
	bootstrapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bootstrapHits.Add(1)
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Genesis token required — download from genesis.aiii.id first"}`))
	}))
	defer bootstrapSrv.Close()

	// A genesis endpoint the shipped client cannot verify — the live
	// failure reproduced without naming a payload schema.
	genesisSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"not":"a signed bundle"}`))
	}))
	defer genesisSrv.Close()

	a := firstbootApp(t, t.TempDir())
	a.ring0Content = "" // the fixture presets it; this fetch must decide
	a.genesisClient = genesis.NewClient(genesisSrv.URL, "http://127.0.0.1:1", bootstrapSrv.URL)

	a.fetchFoundingArtifacts(Config{})

	if a.ring0Content != "" {
		t.Fatal("precondition: RING0 must fail for this test to mean anything")
	}
	if hits := bootstrapHits.Load(); hits != 0 {
		t.Fatalf("bootstrap was requested %d time(s) with no genesis token to present — "+
			"the 402 it earns sends the operator to genesis.aiii.id, which is where the real "+
			"failure already happened", hits)
	}
}

// foundingFakes stands in for the platform's genesis and bootstrap servers
// with the live artifacts' own bytes, and behaves as the platform does
// where it matters here: genesis binds each token it mints to the address
// that fetched Ring 0, and the bootstrap server serves the packet to that
// address alone, answering any other 402 "not valid for this client".
//
// The fake needs no memory for that: a token is "token-N@" and the address
// it is bound to.
//
// Both are reached under one name, founding.test, which has an address of
// each loopback family; the name is resolved in the transport the genesis
// client dials with, which is the transport a client pinned to one family
// copies. So a pinned dial is a real connection over that family, and the
// fake reads the family from the connection's own remote address.
type foundingFakes struct {
	foundingRig
	ring0Fetches, packetAsks, bootstrapAsks atomic.Int64
	ipv4Dials                               atomic.Int64 // dials of founding.test pinned to IPv4
	seen                                    chan string  // "ring0 ::1", "packet 127.0.0.1", … in the order asked
	waits                                   []int        // the backoff asked for before each re-staging
	genesisPort, bootstrapPort              string
	app                                     *App
}

// asked is every Ring 0 fetch and packet request so far, with the address
// each came from.
func (f *foundingFakes) asked() (seen []string) {
	for {
		select {
		case s := <-f.seen:
			seen = append(seen, s)
		default:
			return seen
		}
	}
}

// foundingRig is what one test changes about the fakes and the machine.
type foundingRig struct {
	split       bool                                   // an unpinned dial reaches genesis over IPv6 and the bootstrap server over IPv4
	noIPv4      bool                                   // founding.test has no IPv4 address, and nothing listens on one
	genesisIPv6 bool                                   // genesis listens on IPv6 alone, though its name has an IPv4 address
	bind        func(fetch int64, from string) string  // the address a token is bound to; nil: the one that fetched Ring 0
	ring0       func(fetch int64) int                  // the status a Ring 0 fetch is refused with; nil or 0: served
	retryAfter  string                                 // the Retry-After of a refused Ring 0 fetch
	bootstrap   func(path, token string) (int, string) // a refusal of the bootstrap server's own; nil or 0: the platform's rule
}

const (
	foundingHost  = "founding.test"
	tokenRefusal  = `{"error":"Genesis token not valid for this client — download from genesis.aiii.id first"}`
	elsewhere     = "192.0.2.1" // an address no request here comes from
	ring0Limit    = "the genesis server's hourly limit on Ring 0 downloads for this address has answered (HTTP 429)"
	limitedReport = "No identity can be born in this run: " + ring0Limit
)

// listenLoopback serves h on one port of the IPv6 loopback and, with ipv4,
// of the IPv4 loopback too. A machine without an IPv6 loopback FAILS the
// test: the fault these tests exist for is a machine with both families,
// and a skip would hide exactly that case.
func listenLoopback(t *testing.T, h http.Handler, ipv4 bool) (port string) {
	t.Helper()
	for range 20 {
		six, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Fatalf("this test needs an IPv6 loopback and cannot run without one: listening on [::1] failed: %v", err)
		}
		_, port, _ = net.SplitHostPort(six.Addr().String())
		four, err := net.Listen("tcp4", "127.0.0.1:"+port)
		if err != nil {
			six.Close() // the port is taken on IPv4: another
			continue
		}
		listeners := []net.Listener{six, four}
		if !ipv4 {
			four.Close() // it was free a moment ago: a dial there is refused
			listeners = listeners[:1]
		}
		for _, l := range listeners {
			srv := httptest.NewUnstartedServer(h)
			srv.Listener.Close()
			srv.Listener = l
			srv.Start()
			t.Cleanup(srv.Close)
		}
		return port
	}
	t.Fatal("no port free on both loopback families in 20 tries")
	return ""
}

func newFoundingFakes(t *testing.T, rig foundingRig) *foundingFakes {
	t.Helper()
	root := genesistest.NewRoot(t)
	bundle := root.Ring0Bundle(t)
	keyBundle, packet, _ := root.BootstrapArtifacts(t)
	f := &foundingFakes{foundingRig: rig, seen: make(chan string, 16)}
	saw := func(what string, r *http.Request) (from string) {
		from, _, _ = net.SplitHostPort(r.RemoteAddr)
		f.seen <- what + " " + from
		return from
	}
	f.genesisPort = listenLoopback(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, from := f.ring0Fetches.Add(1), saw("ring0", r)
		if status := 0; f.ring0 != nil {
			if status = f.ring0(n); status != 0 {
				if f.retryAfter != "" {
					w.Header().Set("Retry-After", f.retryAfter)
				}
				w.WriteHeader(status)
				return
			}
		}
		if f.bind != nil {
			from = f.bind(n, from)
		}
		w.Header().Set("X-Genesis-Token", fmt.Sprintf("token-%d@%s", n, from))
		_, _ = w.Write(bundle)
	}), !rig.noIPv4 && !rig.genesisIPv6)
	f.bootstrapPort = listenLoopback(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.bootstrapAsks.Add(1)
		if r.URL.Path != "/bootstrap" {
			if f.bootstrap != nil {
				if status, body := f.bootstrap(r.URL.Path, ""); status != 0 {
					w.WriteHeader(status)
					fmt.Fprint(w, body)
					return
				}
			}
			_, _ = w.Write(keyBundle)
			return
		}
		f.packetAsks.Add(1)
		from, token := saw("packet", r), r.Header.Get("X-Genesis-Token")
		status, body := 0, ""
		if f.bootstrap != nil {
			status, body = f.bootstrap(r.URL.Path, token)
		}
		if _, bound, _ := strings.Cut(token, "@"); status == 0 && bound != from {
			status, body = http.StatusPaymentRequired, tokenRefusal
		}
		if status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, body)
			return
		}
		_, _ = w.Write(packet)
	}), !rig.noIPv4)

	saved := foundingBackoff
	foundingBackoff = func(attempt int) time.Duration { f.waits = append(f.waits, attempt); return 0 }
	t.Cleanup(func() { foundingBackoff = saved })
	t.Cleanup(genesis.SetFetchBackoffForTest(time.Millisecond))

	f.app = firstbootApp(t, t.TempDir())
	f.app.ring0Content, f.app.ring5Content = "", "" // the fixture presets them; this fetch must decide
	f.app.genesisClient = genesis.NewClient("http://"+net.JoinHostPort(foundingHost, f.genesisPort), genesistest.Ring5Server(t), "http://"+net.JoinHostPort(foundingHost, f.bootstrapPort))
	transport := &http.Transport{DialContext: f.dial}
	t.Cleanup(transport.CloseIdleConnections)
	f.app.genesisClient.SetTransportForTest(transport)
	return f
}

// dial resolves founding.test as this rig's machine would and connects for
// real. A pinned dial names its family and gets that family's address,
// or, where the name has none, the dialer's own refusal of an address of
// the other family. An unpinned dial takes IPv4, except where the rig
// splits the two servers across the families or has no IPv4 at all.
func (f *foundingFakes) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, _ := net.SplitHostPort(addr)
	if host == foundingHost {
		if network == "tcp4" {
			f.ipv4Dials.Add(1)
		}
		host = "127.0.0.1"
		if network == "tcp6" || f.noIPv4 || network == "tcp" && f.split && port == f.genesisPort {
			host = "::1"
		}
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
}

// boundElsewhere binds the token of the named Ring 0 fetches, or of every
// fetch when none is named, to an address no request comes from, as a
// token fetched over the other family is bound.
func boundElsewhere(fetches ...int64) func(fetch int64, from string) string {
	return func(fetch int64, from string) string {
		if len(fetches) == 0 || slices.Contains(fetches, fetch) {
			return elsewhere
		}
		return from
	}
}

func TestFoundingFetchStagesThePairAgainOverOneFamilyWhenTheTokenIsRefused(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	f := newFoundingFakes(t, foundingRig{split: true})
	limited := f.app.fetchFoundingArtifacts(Config{})

	if f.app.bootstrapText != genesistest.NewRoot(t).BootstrapPrompt(t) {
		t.Fatalf("the packet was not stored:\n%s", logs)
	}
	if seen, want := f.asked(), []string{"ring0 ::1", "packet 127.0.0.1", "ring0 127.0.0.1", "packet 127.0.0.1"}; !slices.Equal(seen, want) {
		t.Fatalf("the servers were asked %v, want %v: the second staging reaches both over IPv4", seen, want)
	}
	if !slices.Equal(f.waits, []int{1}) {
		t.Fatalf("the pair was staged again after backoffs %v, want the client's first", f.waits)
	}
	if !logs.Contains("refused the genesis token RING0 minted (bootstrap server error: bundle fetch returned 402") || !logs.Contains("again over IPv4 alone, attempt 2 of 3") || logs.Contains("attempt 3 of 3") {
		t.Fatalf("the log does not say what was refused and which family the pair was fetched over:\n%s", logs)
	}
	if report := f.app.foundingReport(limited); !strings.Contains(report, "Bootstrap packet: verified ✓\n") || !strings.HasSuffix(report, "Open the dashboard to create a new identity.\n") {
		t.Fatalf("the console does not say the packet verified:\n%s", report)
	}
}

// IPv4 CANNOT REACH ONE OF THE HOSTS, so the staging over IPv4 fails as a
// transport error does, with the client's own retries inside it, and the
// pair is staged over IPv6. A host that does not answer is not a refusal
// and is not reported as one.
func TestFoundingFetchMovesToIPv6WhenIPv4CannotReachAHost(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	f := newFoundingFakes(t, foundingRig{split: true, genesisIPv6: true})
	f.app.fetchFoundingArtifacts(Config{})

	if f.app.bootstrapText == "" {
		t.Fatalf("the packet was not stored:\n%s", logs)
	}
	if seen, want := f.asked(), []string{"ring0 ::1", "packet 127.0.0.1", "ring0 ::1", "packet ::1"}; !slices.Equal(seen, want) {
		t.Fatalf("the servers were asked %v, want %v: the third staging reaches both over IPv6", seen, want)
	}
	if got := f.ipv4Dials.Load(); got != 3 {
		t.Fatalf("IPv4 was dialed %d time(s), want the client's three tries at a host that refuses the connection", got)
	}
	if !logs.Contains("could not be fetched over IPv4: genesis server error:") || !logs.Contains("again over IPv6 alone, attempt 3 of 3") || logs.Contains("fetched over IPv4 as well") {
		t.Fatalf("the log does not say IPv4 could not reach the host and IPv6 was tried:\n%s", logs)
	}
}

// A MACHINE WITH ONE FAMILY IS NOT REFUSED BY THE OTHER, AND DOES NOT WAIT
// ON IT. Where the host has no address of the pinned family (or the
// network is unreachable) the dial has its answer at once: one dial, no
// backoff, nothing asked of the servers, and the log says the pair could
// not be fetched over that family, never that the token was refused over
// it. The staging still counts toward the three.
func TestFoundingFetchPassesOverAFamilyTheMachineDoesNotHave(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	f := newFoundingFakes(t, foundingRig{noIPv4: true, bind: boundElsewhere(1)})
	limited := f.app.fetchFoundingArtifacts(Config{})

	if f.app.bootstrapText == "" {
		t.Fatalf("the packet was not stored:\n%s", logs)
	}
	if seen, want := f.asked(), []string{"ring0 ::1", "packet ::1", "ring0 ::1", "packet ::1"}; !slices.Equal(seen, want) {
		t.Fatalf("the servers were asked %v, want %v: nothing is asked over the family that is not there", seen, want)
	}
	if got := f.ipv4Dials.Load(); got != 1 {
		t.Fatalf("IPv4 was dialed %d time(s), want once: a family with no address is not waited on", got)
	}
	if !slices.Equal(f.waits, []int{1, 2}) {
		t.Fatalf("backoffs %v, want two: the staging that could not connect counts toward the three", f.waits)
	}
	if !logs.Contains("could not be fetched over IPv4:") || logs.Contains("fetched over IPv4 as well") || strings.Count(logs.String(), "refused the genesis token") != 1 {
		t.Fatalf("the family that is not there must be said, and not as a refusal:\n%s", logs)
	}
	if report := f.app.foundingReport(limited); !strings.HasSuffix(report, "Bootstrap packet: verified ✓\nOpen the dashboard to create a new identity.\n") {
		t.Fatalf("the console reflects only whether the packet verified:\n%s", report)
	}
}

// A SERVER THAT REFUSES THE TOKEN OVER EVERY PATH IS ASKED THREE TIMES AND
// NO MORE, and the start says what it is left with: no packet, a birth
// that refuses naming bootstrap, and a console that does not offer one.
func TestFoundingFetchStopsAfterThreeRefusedTokens(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	f := newFoundingFakes(t, foundingRig{bind: boundElsewhere()})
	limited := f.app.fetchFoundingArtifacts(Config{})

	if seen, want := f.asked(), []string{"ring0 127.0.0.1", "packet 127.0.0.1", "ring0 127.0.0.1", "packet 127.0.0.1", "ring0 ::1", "packet ::1"}; !slices.Equal(seen, want) {
		t.Fatalf("the servers were asked %v, want %v", seen, want)
	}
	if !slices.Equal(f.waits, []int{1, 2}) {
		t.Fatalf("backoffs %v, want the client's own two", f.waits)
	}
	if f.app.bootstrapText != "" || f.app.ring0Content == "" || f.app.ring5Content == "" || limited != nil {
		t.Fatalf("after three refusals: packet %d bytes, Ring 0 %d, Ring 5 %d, limited %v — want no packet, both rings verified, no limit", len(f.app.bootstrapText), len(f.app.ring0Content), len(f.app.ring5Content), limited)
	}
	if !logs.Contains("fetched over IPv4 as well") || !logs.Contains("fetched over IPv6 as well") || !logs.Contains("Bootstrap fetch FAILED") || !logs.Contains("Genesis token not valid for this client") {
		t.Fatalf("the log does not carry each refusal and the last:\n%s", logs)
	}
	if _, err := f.app.handleGenesis(context.Background(), &dashboard.GenesisRequest{Provider: "Local"}); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("a birth without the packet must refuse naming bootstrap, got: %v", err)
	}
	report := f.app.foundingReport(limited)
	if !strings.Contains(report, "Ring 0: verified ✓\nRing 5: verified ✓\nBootstrap packet: NOT verified — birth will fail\n") {
		t.Fatalf("the console does not say which artifact did not verify:\n%s", report)
	}
	if strings.Contains(report, "Open the dashboard to create a new identity.") || !strings.HasSuffix(report, "No identity can be born in this run: the bootstrap packet did not verify, and the log says why. Start the runtime again to fetch it anew.\n") {
		t.Fatalf("the console offers a birth this run cannot make:\n%s", report)
	}
}

func TestThereIsNoBirthWhenTheFoundingServersAreNotReached(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{
		Identity:   IdentityConfig{KeyPath: dir + "/id.sec", LedgerPath: dir + "/ledger.jsonl", DBPath: dir + "/aii.db"},
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		SourcePath: dir + "/config.json",
	})
	a.rings = ring.NewManager()
	a.genesisClient = genesis.NewClient("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	saved := foundingBackoff
	foundingBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { foundingBackoff = saved })
	t.Cleanup(genesis.SetFetchBackoffForTest(time.Millisecond))

	limited := a.fetchFoundingArtifacts(Config{})
	if a.ring0Content != "" || a.ring0Bundle != nil || a.ring5Content != "" || a.bootstrapText != "" || limited != nil {
		t.Fatalf("a start that reached no founding host holds Ring 0 %d bytes, Ring 5 %d, packet %d, limited %v — want nothing", len(a.ring0Content), len(a.ring5Content), len(a.bootstrapText), limited)
	}
	report := a.foundingReport(limited)
	if !strings.Contains(report, "Ring 0: NOT verified — birth will fail\nRing 5: NOT verified — birth will fail\nBootstrap packet: NOT verified — birth will fail\n") {
		t.Fatalf("the console does not say that nothing verified:\n%s", report)
	}
	if strings.Contains(report, "Open the dashboard to create a new identity.") || !strings.Contains(report, "No identity can be born in this run") {
		t.Fatalf("the console offers a birth this run cannot make:\n%s", report)
	}
	_, err := a.handleGenesis(context.Background(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: "http://127.0.0.1:1", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "RING0 not verified") {
		t.Fatalf("a birth with no founding artifact must refuse at the first of them, got: %v", err)
	}
	for _, minted := range []string{"id.sec", "ledger.jsonl", "aii.db"} {
		if _, serr := os.Stat(filepath.Join(dir, minted)); !os.IsNotExist(serr) {
			t.Errorf("a refused birth left %s (%v)", minted, serr)
		}
	}
}

// EVERY RING 0 DOWNLOAD SPENDS AN ALLOWANCE. The genesis server limits
// them per client address per hour and answers 429 with Retry-After; a
// start that meets the limit asks nothing more, and says so in the log
// and on the console with the wait the server named, because starting
// again at once would only spend more of it. Decided by the typed status,
// never the body.
func TestFoundingFetchStopsAtTheGenesisServersRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name, retryAfter, wait       string
		limitedAt, fetches, bootAsks int64
		bind                         func(fetch int64, from string) string
	}{
		{"on the staging over IPv4", "1800", ", and says to try again in 30m0s", 2, 2, 2, boundElsewhere(1)},
		{"on the first fetch", "1800", ", and says to try again in 30m0s", 1, 1, 0, nil},
		{"with no wait named", "", "", 1, 1, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			f := newFoundingFakes(t, foundingRig{bind: tc.bind, retryAfter: tc.retryAfter, ring0: func(fetch int64) int {
				if fetch == tc.limitedAt {
					return http.StatusTooManyRequests
				}
				return 0
			}})
			limited := f.app.fetchFoundingArtifacts(Config{})

			if r, b := f.ring0Fetches.Load(), f.bootstrapAsks.Load(); r != tc.fetches || b != tc.bootAsks {
				t.Fatalf("Ring 0 fetched %d time(s) and the bootstrap server asked %d, want %d and %d: nothing follows the limit", r, b, tc.fetches, tc.bootAsks)
			}
			if !logs.Contains(ring0Limit+tc.wait+" — no further attempt is made in this start") || logs.Contains("attempt 3 of 3") || tc.wait == "" && logs.Contains("try again in") {
				t.Fatalf("the log does not name the limit and the wait:\n%s", logs)
			}
			if report := f.app.foundingReport(limited); !strings.HasSuffix(report, "Bootstrap packet: NOT verified — birth will fail\n"+limitedReport+tc.wait+". Starting the runtime again at once only spends more of that limit.\n") {
				t.Fatalf("the console does not name the limit and the wait:\n%s", report)
			}
		})
	}
}

// EVERY OTHER FAILURE IS AN ANSWER, AS IT WAS. Only the token's refusal of
// the packet stages the pair again: a missing packet, a refused
// credential, a packet that does not verify and a refused key chain are
// each asked once, and a Ring 0 that cannot be fetched again ends it.
// None of them is the genesis server's rate limit, which is a 429 for
// Ring 0 and nothing else.
func TestFoundingFetchStagesNothingAgainForAnyOtherFailure(t *testing.T) {
	packet := func(status int, body string) func(string, string) (int, string) {
		return func(path, _ string) (int, string) {
			if path == "/bootstrap" {
				return status, body
			}
			return 0, ""
		}
	}
	for _, tc := range []struct {
		name               string
		ring0              func(int64) int
		bind               func(int64, string) string
		bootstrap          func(path, token string) (int, string)
		ring0Fetches, asks int64
	}{
		{"the packet is not found", nil, nil, packet(http.StatusNotFound, "no such packet"), 1, 1},
		{"the credential is refused", nil, nil, packet(http.StatusUnauthorized, "unauthorized"), 1, 1},
		{"the packet does not verify", nil, nil, packet(http.StatusOK, `{"not":"a signed bundle"}`), 1, 1},
		{"the packet is refused with the rate limit's status", nil, nil, packet(http.StatusTooManyRequests, "slow down"), 1, 1},
		{"the packet is refused with that status on the staging over IPv4", nil, boundElsewhere(1), func(path, token string) (int, string) {
			if path == "/bootstrap" && strings.HasPrefix(token, "token-2@") {
				return http.StatusTooManyRequests, "slow down"
			}
			return 0, ""
		}, 2, 2},
		{"the key chain is refused with the token's status", nil, nil, func(string, string) (int, string) { return http.StatusPaymentRequired, tokenRefusal }, 1, 0},
		{"Ring 0 cannot be fetched again", func(fetch int64) int {
			if fetch > 1 {
				return http.StatusNotFound
			}
			return 0
		}, nil, packet(http.StatusPaymentRequired, tokenRefusal), 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFoundingFakes(t, foundingRig{ring0: tc.ring0, bind: tc.bind, bootstrap: tc.bootstrap})
			if limited := f.app.fetchFoundingArtifacts(Config{}); limited != nil {
				t.Fatalf("a refusal that is not the genesis server's rate limit was taken for it: %v", limited)
			}
			if r, p := f.ring0Fetches.Load(), f.packetAsks.Load(); r != tc.ring0Fetches || p != tc.asks {
				t.Fatalf("Ring 0 fetched %d time(s) and the packet asked for %d, want %d and %d", r, p, tc.ring0Fetches, tc.asks)
			}
			if f.app.bootstrapText != "" || f.app.ring0Content == "" {
				t.Fatalf("packet %d bytes, Ring 0 %d — want no packet, and the Ring 0 that verified kept", len(f.app.bootstrapText), len(f.app.ring0Content))
			}
		})
	}
}

// foundingProvider is a local provider at the Birth click: it lists one
// model, keeps every inference request's messages in the order they came,
// and answers each as refuse says: a status and a body, or 0 to reply.
type foundingProvider struct {
	mu       sync.Mutex
	requests [][]llm.Message
	app      *App
	birth    *dashboard.GenesisRequest
}

func newFoundingProvider(t *testing.T, apiType string, refuse func(messages []llm.Message) (int, string)) *foundingProvider {
	t.Helper()
	p := &foundingProvider{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
			return
		}
		var request struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		p.mu.Lock()
		p.requests = append(p.requests, request.Messages)
		p.mu.Unlock()
		if status, body := refuse(request.Messages); status != 0 {
			w.WriteHeader(status)
			fmt.Fprint(w, body)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello."},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	p.app = birthApp(t, providerEntry{Name: "Local", APIType: apiType, URL: srv.URL + "/v1", APIKey: "k", DefaultModel: "birth-model", Default: true})
	t.Cleanup(func() { stopApp(t, p.app) })
	p.birth = &dashboard.GenesisRequest{Provider: "Local", Endpoint: srv.URL + "/v1", Model: "birth-model"}
	return p
}

// foundingReason is the transcript reason the born identity's first words
// were recorded with.
func (p *foundingProvider) foundingReason(t *testing.T) string {
	t.Helper()
	page, err := p.app.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: "recorded"})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Content != "Hello." {
		t.Fatalf("the transcript does not hold the founding greeting alone: %+v %v", page, err)
	}
	return page.Rows[0].Details.Reason
}

// minted reports whether anything of an identity was written.
func (p *foundingProvider) minted() bool {
	id := p.app.configSnapshot().Identity
	return fileExists(id.KeyPath) || fileExists(id.LedgerPath) || fileExists(id.DBPath)
}

const noUserQuery = `{"error":{"message":"Chat template error: No user query found in messages.","type":"invalid_request_error"}}`

// refusesSystemOnly answers as the local model did: a conversation holding
// no user message is refused with status, and anything else is answered.
func refusesSystemOnly(status int) func([]llm.Message) (int, string) {
	return func(messages []llm.Message) (int, string) {
		if slices.ContainsFunc(messages, func(m llm.Message) bool { return m.Role == "user" }) {
			return 0, ""
		}
		return status, noUserQuery
	}
}

func TestBirthSendsARefusedSystemPromptAsTheOnlyUserMessage(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			p := newFoundingProvider(t, "openai", refusesSystemOnly(status))
			greeting, err := p.app.handleGenesis(t.Context(), p.birth)
			if err != nil || greeting != "Hello." {
				t.Fatalf("a provider that refuses a conversation with no user message must still be born on: %q %v", greeting, err)
			}
			// The stream ladder asks a refused request on each of its three
			// rungs; the user message is then answered on the first.
			if len(p.requests) != 4 {
				t.Fatalf("the provider saw %d founding requests, want 4 (the system message on three rungs, the user message once)", len(p.requests))
			}
			for i, messages := range p.requests {
				role := "system"
				if i == 3 {
					role = "user"
				}
				if len(messages) != 1 || messages[0].Role != role || messages[0].Content != p.app.bootstrapText {
					t.Fatalf("founding request %d is not the verified prompt alone as the %s message: %+v", i+1, role, messages)
				}
			}
			if seq := p.app.ledger.LastSeq(); seq != 1 {
				t.Fatalf("birth minted %d events, want only ring0.genesis", seq)
			}
			want := fmt.Sprintf("founding greeting (the founding prompt was sent as the only user message: the provider refused it as the only system message, HTTP %d)", status)
			if got := p.foundingReason(t); got != want {
				t.Fatalf("the greeting's record does not say which role carried the prompt: %q", got)
			}
			if !logs.Contains("birth makes one more request, the same bytes as the only user message") {
				t.Fatalf("the log does not say the prompt was sent again as a user message:\n%s", logs)
			}
		})
	}
}

func TestBirthSendsTheFoundingPromptAsTheOnlySystemMessageFirst(t *testing.T) {
	p := newFoundingProvider(t, "openai", func([]llm.Message) (int, string) { return 0, "" })
	if _, err := p.app.handleGenesis(t.Context(), p.birth); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 || len(p.requests[0]) != 1 || p.requests[0][0].Role != "system" || p.requests[0][0].Content != p.app.bootstrapText {
		t.Fatalf("want one founding request, the verified prompt alone as the system message: %+v", p.requests)
	}
	if got := p.foundingReason(t); got != "founding greeting" {
		t.Fatalf("the greeting's record changed: %q", got)
	}
}

// ONLY A REFUSED REQUEST SHAPE IS SENT AGAIN, AND ONLY ON THE DIALECT THAT
// SENT A SYSTEM MESSAGE. A refused credential is not a refused shape,
// whatever status it arrives under; and the Anthropic dialect already
// sent the prompt as the sole user message, so its 400 is its answer.
func TestBirthDoesNotSendTheFoundingPromptAgainForAnyOtherRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, apiType, body string
		status              int
	}{
		{"401", "openai", `{"error":{"message":"bad key"}}`, http.StatusUnauthorized},
		{"403", "openai", `{"error":{"message":"forbidden"}}`, http.StatusForbidden},
		{"a credential refused under 400", "openai", `{"error":{"message":"No user query found in messages.","code":"invalid_api_key"}}`, http.StatusBadRequest},
		{"a model that is not there", "openai", `{"error":{"message":"no such model"}}`, http.StatusNotFound},
		{"the Anthropic dialect's own 400", "anthropic", `{"type":"error","error":{"type":"invalid_request_error","message":"refused"}}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newFoundingProvider(t, tc.apiType, func([]llm.Message) (int, string) { return tc.status, tc.body })
			_, err := p.app.handleGenesis(t.Context(), p.birth)
			var refusal *llm.HTTPError
			if !errors.As(err, &refusal) || refusal.StatusCode != tc.status {
				t.Fatalf("birth must fail with the provider's refusal, got: %v", err)
			}
			if len(p.requests) != 1 || slices.ContainsFunc(p.requests, func(m []llm.Message) bool { return len(m) != 1 || m[0].Role == "user" && tc.apiType == "openai" }) {
				t.Fatalf("the provider saw %d founding request(s), want the one: %+v", len(p.requests), p.requests)
			}
			if p.minted() {
				t.Fatal("a refused founding prompt minted something")
			}
		})
	}
}

// A PROVIDER THAT REFUSES BOTH SHAPES IS NOT BORN ON, and the operator
// reads the cause: the first refusal, with the second beside it.
func TestBirthRefusedInBothShapesReportsTheFirstRefusal(t *testing.T) {
	p := newFoundingProvider(t, "openai", func(messages []llm.Message) (int, string) {
		if messages[0].Role == "user" {
			return http.StatusBadRequest, `{"error":{"message":"the second refusal"}}`
		}
		return http.StatusBadRequest, noUserQuery
	})
	_, err := p.app.handleGenesis(t.Context(), p.birth)
	var refusal *llm.HTTPError
	if !errors.As(err, &refusal) || refusal.Body != noUserQuery {
		t.Fatalf("the reported error must be the first refusal, got: %v", err)
	}
	if first, second := strings.Index(err.Error(), "No user query found"), strings.Index(err.Error(), "the second refusal"); first < 0 || second < first {
		t.Fatalf("the operator must read the first refusal, then the second: %v", err)
	}
	// Each shape on each of the ladder's three rungs, and no more.
	if len(p.requests) != 6 {
		t.Fatalf("the provider saw %d founding requests, want 6", len(p.requests))
	}
	if p.minted() {
		t.Fatal("a founding prompt refused in both shapes minted something")
	}
}
