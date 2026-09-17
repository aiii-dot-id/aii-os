package app

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .

// .
// .
// .
// .
// .
func (a *App) fetchFoundingArtifacts(cfg Config) {
	log.Printf("Fetching RING0 from %s...", cfg.Genesis.ServerURL)
	ring0Result, err := a.genesisClient.FetchRing0()
	if err != nil {
		log.Printf("RING0 fetch failed: %v", err)
		// .
	} else {
		a.ring0Content = ring0Result.Content
		a.ring0Bundle = ring0Result.Bundle
		a.genesisClient.SetToken(ring0Result.Token)
		log.Printf("RING0 verified (%d bytes)", len(a.ring0Content))
	}

	log.Printf("Fetching Ring 5 from %s...", cfg.Genesis.FirewallURL)
	ring5Result, err := a.genesisClient.FetchRing5()
	if err != nil {
		log.Printf("Ring 5 fetch failed — dashboard remains available, but birth will refuse: %v", err)
	} else {
		a.ring5Content = ring5Result.Content
		log.Printf("Ring 5 verified (%d bytes)", len(a.ring5Content))
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if a.ring0Content == "" {
		log.Printf("Bootstrap fetch SKIPPED — RING0 did not verify, so no genesis token was minted to present. Birth refuses until the RING0 failure above is resolved.")
		return
	}

	log.Printf("Fetching bootstrap packet from %s...", cfg.Genesis.BootstrapURL)
	bootstrapResult, err := a.genesisClient.FetchBootstrap()
	if err != nil {
		log.Printf("Bootstrap fetch FAILED — birth will refuse until the signed packet verifies (operator law: bootstrap births only): %v", err)
		return
	}
	a.bootstrapText = bootstrapResult.Content
	log.Printf("Bootstrap packet verified (%d bytes)", len(a.bootstrapText))
}

func (a *App) startFirstboot() error {
	log.Println("No identity found. Starting FIRSTBOOT flow.")
	cfg := a.configSnapshot()

	a.rings = ring.NewManager()
	a.genesisClient = genesis.NewClient(
		cfg.Genesis.ServerURL,
		cfg.Genesis.FirewallURL,
		cfg.Genesis.BootstrapURL,
	)

	a.fetchFoundingArtifacts(cfg)

	// .
	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	a.loadRing5()

	// .
	// .
	// .
	// .
	a.promptGate = prompt.NewGate(firstbootRings{a}, 0)

	handler := a.buildFirstbootHandler()
	d, derr := a.newDashboard(handler)
	if derr != nil {
		return fmt.Errorf("dashboard credentials: %w", derr)
	}
	a.dashboard = d
	a.dashboard.SetQuiesceGate(a.gate)
	_, err := a.dashboard.Start(tlsDirFor(cfg))
	if err != nil {
		// .
		// .
		// .
		return fmt.Errorf("dashboard start failed: %w", err)
	}

	fmt.Println("\n=== FIRSTBOOT ===")
	a.printDashboardURLs()
	if a.ring0Content != "" {
		fmt.Println("Ring 0: verified ✓")
	} else {
		fmt.Println("Ring 0: NOT verified — birth will fail")
	}
	if a.ring5Content != "" {
		fmt.Println("Ring 5: verified ✓")
	} else {
		fmt.Println("Ring 5: NOT verified — birth will fail")
	}
	fmt.Println("Open the dashboard to create a new identity.")
	return nil
}

func (a *App) buildFirstbootHandler() *dashboard.WSHandler {
	return &dashboard.WSHandler{
		Speaker: "system",
		GetStats: func() (*dashboard.StatsResponse, error) {
			return &dashboard.StatsResponse{}, nil
		},
		HandleMessage:  a.handleBootstrapMessage,
		SetProvider:    a.setProviderInfo,
		DeleteProvider: a.deleteProvider,
		HandleGenesis:  a.handleGenesis,
		// .
		// .
		// .
		// .
		// .
		GetProviders:        a.providerDirectoryLive,
		SignInProvider:      a.SignInProvider,
		CompleteSignIn:      a.CompleteSignIn,
		CancelSignIn:        a.CancelSignIn,
		CancelProfileSignIn: a.CancelProfileSignIn,
		OAuthCallback:       a.OAuthCallback,
		UpdateCheck:         a.checkForUpdateNow,
		DiscoverModels: func(provider, apiKey string) ([]string, error) {
			reg, err := a.loadProviders()
			if err != nil {
				return nil, err
			}
			return a.discoverForProvider(context.Background(), reg, provider, apiKey)
		},
	}
}

// .
// .
// .
type firstbootRings struct{ a *App }

func (f firstbootRings) Ring0() string { return f.a.ring0Content }
func (f firstbootRings) Ring5() string { return f.a.ring5Content }
func (f firstbootRings) Ring3() string { return "" }
func (f firstbootRings) Ring4() string { return "" }

// .
// .
// .
// .
func (a *App) handleBootstrapMessage(context.Context, string) (string, error) {
	// .
	// .
	// .
	// .
	// .
	return "No identity lives here yet. Fill in the provider, model, API key, and names, then click Birth — the founding conversation begins with the birth.", nil
}

// .
// .
// .
func (a *App) handleGenesis(ctx context.Context, req *dashboard.GenesisRequest) (string, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.birthMu.Lock()
	defer a.birthMu.Unlock()
	bootCfg := a.configSnapshot()

	// .
	// .
	// .
	// .
	// .
	if fileExists(bootCfg.Identity.LedgerPath) {
		return "", fmt.Errorf("an identity already exists at %s — restart the runtime to load it (a partial birth needs manual cleanup, not a resubmission)", bootCfg.Identity.LedgerPath)
	}

	// .
	// .
	// .
	// .
	if a.ring0Content == "" {
		return "", fmt.Errorf("RING0 not verified — cannot create identity without a signed constitution")
	}
	if a.ring5Content == "" {
		return "", fmt.Errorf("Ring 5 not verified — cannot create identity without the platform security posture (is %s reachable?)", bootCfg.Genesis.FirewallURL)
	}
	// .
	// .
	// .
	// .
	// .
	if a.bootstrapText == "" {
		return "", fmt.Errorf("bootstrap packet not verified — birth refused: identities are born only through the bootstrap process (is %s reachable?)", bootCfg.Genesis.BootstrapURL)
	}
	if req.Endpoint == "" || req.Model == "" {
		return "", fmt.Errorf("provider endpoint and model are required")
	}

	// .
	// .
	bentry, beff, err := a.birthEntry(req.Provider, req.Endpoint, req.Model)
	if err != nil {
		return "", fmt.Errorf("provider registry: %w", err)
	}
	models, meta, err := a.discoverMetaForEntry(ctx, bentry, req.APIKey)
	if err != nil {
		return "", fmt.Errorf("provider validation failed (%s): %w", req.Endpoint, err)
	}
	offered := false
	for _, m := range models {
		if m == req.Model {
			offered = true
			break
		}
	}
	if !offered {
		return "", fmt.Errorf("model %q is not offered by %s (%d models offered)", req.Model, req.Endpoint, len(models))
	}
	if discovered, ok := meta[req.Model]; ok {
		if bentry.ContextLength == 0 {
			bentry.ContextLength = discovered.Context
		}
		if bentry.MaxOutputTokens == 0 {
			bentry.MaxOutputTokens = discovered.MaxOut
		}
	}
	if err := validateModelWindow(bentry, req.Model); err != nil {
		return "", fmt.Errorf("provider validation failed: %w", err)
	}
	bentry = resolveOutputAllocation(bentry)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	bootConfig, terr := a.clientConfigForEntry(beff, bentry, bootCfg.LLM, req.Model, req.APIKey)
	if terr != nil {
		return "", terr
	}
	bootConfig.MaxInputTokens = promptBudgetFor(bentry, bootCfg.Prompt.MaxTokens)
	bootClient := llm.New(&bootConfig)
	bootResp, err := bootClient.Chat(ctx, []llm.Message{{Role: "system", Content: a.bootstrapText}}, llm.ChatOptions{})
	if err == nil && (bootResp == nil || len(bootResp.Choices) == 0) {
		err = fmt.Errorf("empty reply")
	}
	if err != nil {
		return "", fmt.Errorf("the firstboot prompt got no answer from %s: %w", req.Endpoint, err)
	}
	greeting := bootResp.Choices[0].Message.Content
	if strings.TrimSpace(greeting) == "" {
		return "", fmt.Errorf("the firstboot prompt returned an empty answer from %s — there is nothing to be born from (a model that puts its answer in a reasoning field returns exactly this); check the model's output settings and click Birth again", req.Endpoint)
	}
	if fr := bootResp.Choices[0].FinishReason; fr == "length" {
		return "", fmt.Errorf("the firstboot prompt was cut off by the output limit (finish_reason=%q) from %s — a founding record must not be half a sentence; raise the model's max output tokens and click Birth again", fr, req.Endpoint)
	}

	// .
	// .
	// .
	// .
	name := deriveName(greeting)

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
	provName, perr := a.upsertBirthProvider(req)
	if perr != nil {
		return "", fmt.Errorf("cannot record the provider that will serve this identity: %w", perr)
	}
	a.cfgMu.Lock()
	cfg := *a.cfg
	cfg.LLM.Provider = provName
	cfg.LLM.Model = req.Model
	published, persistErr := saveConfig(&cfg)
	if published {
		*a.cfg = cfg
	}
	a.cfgMu.Unlock()
	if persistErr != nil {
		if published {
			return "", fmt.Errorf("substrate pointer was published but directory durability is unconfirmed; birth did not start: %w", persistErr)
		}
		return "", fmt.Errorf("cannot persist the substrate pointer: %w", persistErr)
	}

	model := req.Model

	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        name,
		Ring0Bundle: a.ring0Bundle,
		Root:        a.genesisClient.Root(),
		KeyPath:     cfg.Identity.KeyPath,
		LedgerPath:  cfg.Identity.LedgerPath,
		DBPath:      cfg.Identity.DBPath,
		ModelID:     model,
	})
	if err != nil {
		return "", err
	}
	if err := result.Ledger.Close(); err != nil {
		return "", fmt.Errorf("identity created but final ledger durability failed; restart will verify the record before admitting it: %w", err)
	}

	// .
	// .
	// .
	if err := a.startLive(); err != nil {
		return "", fmt.Errorf("identity created but startup failed: %w", err)
	}

	// .
	a.dashboard.SwapHandler(a.buildLiveHandler())
	log.Printf("Genesis complete: identity=%s, handler swapped, store=%p, engine=%p", name, a.store, a.engine)

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
	if rerr := a.engine.RecordConversationTurn("resident", greeting); rerr != nil {
		return greeting, fmt.Errorf("identity created and greeted, but its first words were not recorded — "+
			"the chat replay will not show them: %w", rerr)
	}
	return greeting, nil
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
func (a *App) upsertBirthProvider(req *dashboard.GenesisRequest) (string, error) {
	name := req.Provider
	if name == "" {
		if reg, err := a.loadProviders(); err == nil {
			for _, e := range reg.Providers {
				if e.URL == req.Endpoint {
					name = e.Name
					break
				}
			}
		}
	}
	if name == "" {
		if u, err := url.Parse(req.Endpoint); err == nil && u.Host != "" {
			name = u.Host
		} else {
			name = req.Endpoint
		}
	}
	entry, _, err := a.birthEntry(name, req.Endpoint, req.Model)
	if err != nil {
		return "", fmt.Errorf("providers.json: %w", err)
	}
	entry.Name, entry.Default = name, true
	// .
	// .
	// .
	yes := true
	entry.Chat = &yes
	if req.APIKey != "" {
		entry.APIKey = req.APIKey
	}
	if err := a.setProvider(entry, false); err != nil {
		return "", fmt.Errorf("providers.json: %w", err)
	}
	return name, nil
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
func deriveName(greeting string) string {
	first := ""
	for _, line := range strings.Split(greeting, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			first = t
			break
		}
	}
	if first == "" {
		return "Unnamed"
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if name, found := nameAfterLead(first); found {
		return name
	}
	return "Unnamed"
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
// .
// .
// .
func nameAfterLead(line string) (string, bool) {
	leads := []string{
		"NAME:", "I am", "I'm", "My name is", "My name's", "Call me",
		"Je m'appelle", "Ich heiße", "Me llamo", "Mi chiamo",
	}
	type hit struct {
		at   int
		lead string
	}
	var hits []hit
	lower := strings.ToLower(line)
	for _, lead := range leads {
		from := 0
		for from <= len(line) {
			var idx int
			if lead == "NAME:" {
				idx = strings.Index(line[from:], lead)
			} else {
				idx = strings.Index(lower[from:], strings.ToLower(lead))
			}
			if idx < 0 {
				break
			}
			idx += from
			from = idx + len(lead)
			// .
			if idx > 0 && IsWordRune(rune(line[idx-1])) {
				continue
			}
			hits = append(hits, hit{idx, lead})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	for _, h := range hits {
		name, ok := nameAt(line, h.at+len(h.lead))
		if !ok {
			continue
		}
		if h.lead != "NAME:" && !startsUpper(name) {
			continue
		}
		return name, true
	}
	return "", false
}

// .
// .
func nameAt(line string, cut int) (string, bool) {
	rest := strings.TrimLeft(line[cut:], " \t.,;:!?'\u201c\u201d\"-")
	if rest == "" {
		return "", false
	}
	name := rest
	if i := strings.IndexAny(name, ".,;:!?\"'()[]-\u2014"); i > 0 {
		name = name[:i]
	}
	// .
	// .
	for _, stop := range []string{" and ", " & ", " so ", " but ", " as "} {
		if i := strings.Index(name, stop); i > 0 {
			name = name[:i]
		}
	}
	words := strings.Fields(name)
	if len(words) > 4 {
		words = words[:4]
	}
	name = strings.Trim(strings.Join(words, " "), " \t.,;:!?\"'")
	if len(name) > 32 {
		name = name[:32]
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// .
// .
func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// .
// .
func IsWordRune(r rune) bool {
	return strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", r)
}
