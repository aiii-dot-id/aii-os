package app

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// --- FIRSTBOOT ---

// fetchFoundingArtifacts runs Step 0 of FIRSTBOOT: fetch and verify the
// three founding artifacts. Extracted from startFirstboot so the
// ordering rule below is provable at a real boundary rather than
// asserted — startFirstboot also builds the dashboard, which a unit
// test has no business starting.
func (a *App) fetchFoundingArtifacts(cfg Config) {
	log.Printf("Fetching RING0 from %s...", cfg.Genesis.ServerURL)
	ring0Result, err := a.genesisClient.FetchRing0()
	if err != nil {
		log.Printf("RING0 fetch failed: %v", err)
		// Start dashboard anyway — it will show the error
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

	// The bootstrap packet is token-gated, and only a verified RING0
	// mints the token. Asking for it without one earns a 402 "Genesis
	// token required — download from genesis.aiii.id first", which tells
	// the operator to do the exact thing that just failed a line above
	// and buries the real cause. Observed live 2026-08-23: a RING0
	// schema mismatch surfaced to the operator as a payment-required
	// error about downloading genesis. A call that cannot succeed is not
	// a diagnosis. Report the cause that already exists.
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

	// Load Ring 5 into ring manager (platform bundle + local floor)
	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	a.loadRing5()

	// Start dashboard
	// Pre-birth substrate messages use the verified Ring 0/Ring 5 view.
	// The identity's only pre-mint inference happens in handleGenesis,
	// where the verified Firstboot bundle prompt is sent by itself.
	a.promptGate = prompt.NewGate(firstbootRings{a}, 0)

	handler := a.buildFirstbootHandler()
	a.dashboard = a.newDashboard(handler)
	a.dashboard.SetQuiesceGate(a.gate) // firstboot backgrounds park the sweep too (quiesce, 2026-08-19)
	_, err := a.dashboard.Start(tlsDirFor(cfg))
	if err != nil {
		// Returned, never fatal: on mobile this code runs inside the
		// host app's process, and a Fatalf here killed the whole app
		// (Sev 2026-08-26, P1). The desktop caller exits on it instead.
		return fmt.Errorf("dashboard start failed: %w", err)
	}

	fmt.Println("\n=== FIRSTBOOT ===")
	fmt.Printf("Dashboard: %s\n", a.dashboard.Origin())
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
		IdentityName: "(not born)",
		Speaker:      "system", // the substrate answers here, as itself
		GetStats: func() (*dashboard.StatsResponse, error) {
			return &dashboard.StatsResponse{}, nil
		},
		HandleMessage:  a.handleBootstrapMessage,
		SetProvider:    a.setProviderInfo,
		DeleteProvider: a.deleteProvider,
		HandleGenesis:  a.handleGenesis,
		// The provider registry + live discovery belong HERE most of all —
		// the birth form is the FIRSTBOOT surface. (Found the hard way
		// 2026-08-17: the wiring had landed only on buildLiveHandler, so
		// every FIRSTBOOT test served an empty directory while live mode
		// carried providers nobody asked it for.)
		GetProviders: a.providerDirectoryLive,
		DiscoverModels: func(provider, apiKey string) ([]string, error) {
			reg, err := a.loadProviders()
			if err != nil {
				return nil, err
			}
			return a.discoverForProvider(context.Background(), reg, provider, apiKey)
		},
	}
}

// firstbootRings supplies the gate's before-birth view: verified Ring 0
// and Ring 5 only — no charter, no self yet (that is the point of
// firstboot).
type firstbootRings struct{ a *App }

func (f firstbootRings) Ring0() string { return f.a.ring0Content }
func (f firstbootRings) Ring5() string { return f.a.ring5Content }
func (f firstbootRings) Ring3() string { return "" }
func (f firstbootRings) Ring4() string { return "" }

// handleBootstrapMessage answers pre-birth chat attempts: before the
// Birth click there is no conversation to have — the founding
// conversation begins with the birth (operator's four steps,
// 2026-08-20). Substrate voice only; never fiction from an unborn mind.
func (a *App) handleBootstrapMessage(context.Context, string) (string, error) {
	// No "[system]" prefix: the speaker travels as a KEY on the frame
	// now (WSHandler.Speaker), and the browser renders substrate speech
	// as a system line — never in the identity's bubble. Text prefixes
	// doing a protocol's job is how the substrate ended up in the
	// identity's voice slot in the first place.
	return "No identity lives here yet. Fill in the provider, model, API key, and names, then click Birth — the founding conversation begins with the birth.", nil
}

// handleGenesis is called from the dashboard WebSocket when the operator
// submits the FIRSTBOOT form. It creates the identity and transitions
// to live mode without restarting the server.
func (a *App) handleGenesis(ctx context.Context, req *dashboard.GenesisRequest) (string, error) {
	// ONE birth at a time (review 2026-08-20). Two tabs clicking Birth in
	// the same second both passed the virgin-ground checks — which are
	// stat-then-write, so they cannot be the backstop — and wrote two
	// keypairs and two ring0.genesis events into one ledger, which
	// VerifyChain then refuses at every boot afterwards. Held across the
	// whole ceremony, so the second click meets an existing ledger and
	// gets the honest refusal instead of a race.
	a.birthMu.Lock()
	defer a.birthMu.Unlock()
	bootCfg := a.configSnapshot()

	// Virgin ground (finding 13, 2026-08-17 review): if a previous birth
	// attempt created the ledger and startLive then failed, a form
	// resubmission re-ran Birth against the EXISTING ledger — a second
	// ring0.genesis under a new key, chain poisoned at next boot. The
	// artifact on disk IS the identity; refuse and say what to do.
	if fileExists(bootCfg.Identity.LedgerPath) {
		return "", fmt.Errorf("an identity already exists at %s — restart the runtime to load it (a partial birth needs manual cleanup, not a resubmission)", bootCfg.Identity.LedgerPath)
	}

	// Ring 0 AND Ring 5 must be fetched and verified — no hardcoded
	// default, and no birth without the platform security posture (canon
	// alignment minimum, D3 2026-08-20: the mind probe below is a
	// firstboot LLM call and requires both in working context).
	if a.ring0Content == "" {
		return "", fmt.Errorf("RING0 not verified — cannot create identity without a signed constitution")
	}
	if a.ring5Content == "" {
		return "", fmt.Errorf("Ring 5 not verified — cannot create identity without the platform security posture (is %s reachable?)", bootCfg.Genesis.FirewallURL)
	}
	// Operator law (2026-08-20): identities are born only through the
	// bootstrap process — and the process runs INSIDE this click, in
	// order, in Go (operator's four steps, 2026-08-20): validate the
	// provider exactly as entered; send the firstboot prompt to it; the
	// reply means birth is underway and becomes the start of chat.
	if a.bootstrapText == "" {
		return "", fmt.Errorf("bootstrap packet not verified — birth refused: identities are born only through the bootstrap process (is %s reachable?)", bootCfg.Genesis.BootstrapURL)
	}
	if req.Endpoint == "" || req.Model == "" {
		return "", fmt.Errorf("provider endpoint and model are required")
	}

	// Step 1: validate the provider — the endpoint must answer /models
	// and offer the model. Refusal mints nothing; the operator sees why.
	bentry, err := a.birthEntry(req.Provider, req.Endpoint, req.Model)
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

	// Step 2: the firstboot prompt — the verified bundle text, nothing
	// else — to the validated provider. The reply is the mind's first
	// words through the founding prompt: birth is underway.
	// The founding exchange goes through the SAME transport resolution as
	// every later turn: an identity born on a Claude Max subscription
	// speaks the native dialect with a bearer, and one born on an
	// Anthropic entry does not get OpenAI-shaped requests. Hand-building
	// a four-field client here made both impossible.
	bootConfig, terr := a.clientConfigForEntry(bentry, bootCfg.LLM, req.Model, req.APIKey)
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

	// ── Everything above this line can refuse; nothing above it is
	// durable. Everything below it is the mint. That is the rule
	// genesis.Birth states and follows for its own artifacts (M7:
	// "every refusal that can happen before the ground is touched must
	// happen before the ground is touched"); the ceremony had the same
	// two phases with no line between them, so a refusal living below
	// the mint left an identity that existed and could not boot.
	//
	// The provider is persisted FIRST, and its failure is a REFUSAL, not
	// a log line: minting over an unwritten entry leaves llm.provider
	// pointing at nothing. A provider entry with no identity is
	// harmless — the operator sees it in the form and in the UI.
	provName, perr := a.upsertBirthProvider(req)
	if perr != nil {
		return "", fmt.Errorf("cannot record the provider that will serve this identity: %w", perr)
	}
	a.cfgMu.Lock()
	cfg := *a.cfg
	cfg.Identity.Name = req.Name
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
		Name:        req.Name,
		Ring0Bundle: a.ring0Bundle,          // Birth re-verifies the signed bundle and derives Ring 0
		Root:        a.genesisClient.Root(), // the shipped pinned root Birth verifies against
		KeyPath:     cfg.Identity.KeyPath,
		LedgerPath:  cfg.Identity.LedgerPath,
		DBPath:      cfg.Identity.DBPath,
		ModelID:     model, // signed substrate provenance from birth
	})
	if err != nil {
		return "", err
	}
	if err := result.Ledger.Close(); err != nil {
		return "", fmt.Errorf("identity created but final ledger durability failed; restart will verify the record before admitting it: %w", err)
	}

	// Transition to live mode — swap the dashboard handler. The
	// substrate pointer and the provider entry are already on disk
	// (above the mint), so this resolves what birth just proved works.
	if err := a.startLive(); err != nil {
		return "", fmt.Errorf("identity created but startup failed: %w", err)
	}

	// Swap the handler on the existing server (no restart needed)
	a.dashboard.SwapHandler(a.buildLiveHandler())
	log.Printf("Genesis complete: identity=%s, handler swapped, store=%p, engine=%p", req.Name, a.store, a.engine)

	// Steps 3-4: the greeting — the mind's own words through the
	// founding prompt, never substrate fiction — is recorded as the
	// identity's first transcript turn and returned as the start of
	// chat. Chat proceeds as the born identity's life.
	// A persistence failure is not a success, even at the very end. The
	// greeting is the identity's FIRST WORDS and lives only here — the
	// transcript is a process witness, not ledger truth, so nothing
	// replays it back. Logging and returning nil told the operator birth
	// went fine while those words were already gone.
	//
	// The greeting still comes back: the mind was born, its ledger
	// events stand, and the operator should see what it said. The error
	// rides beside it in the shape this file already uses one screen up
	// ("identity created but startup failed").
	if rerr := a.engine.RecordConversationTurn("resident", greeting); rerr != nil {
		return greeting, fmt.Errorf("identity created and greeted, but its first words were not recorded — "+
			"the chat replay will not show them: %w", rerr)
	}
	return greeting, nil
}

// upsertBirthProvider persists the provider that just birthed a mind
// into providers.json through the registry's one upsert door
// (a set default clears the others) and returns the entry name the config
// pointer carries.
// The form sends the selected entry's name; a missing name adopts an
// existing entry with this endpoint before inventing one named after
// the endpoint host.
//
// The write is a REFUSAL point, not best-effort: it now runs BEFORE the
// mint (review 2026-08-20), so a failure here costs nothing but a retry,
// whereas letting it through left llm.provider pointing at an entry that
// was never written.
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
	entry, err := a.birthEntry(name, req.Endpoint, req.Model)
	if err != nil {
		return "", fmt.Errorf("providers.json: %w", err)
	}
	entry.Name, entry.Default = name, true
	if req.APIKey != "" {
		entry.APIKey = req.APIKey
	}
	if err := a.setProvider(entry, false); err != nil {
		return "", fmt.Errorf("providers.json: %w", err)
	}
	return name, nil
}
