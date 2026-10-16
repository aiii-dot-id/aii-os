package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
)

func (a *App) fetchFoundingArtifactsAsConfigured(cfg Config) (limited *genesis.StatusError) {
	a.genesisClient = cfg.Genesis.client(a.lifetime())
	return a.fetchFoundingArtifacts(cfg)
}

func (a *App) fetchFoundingArtifacts(cfg Config) (limited *genesis.StatusError) {
	logsink.Info("genesis.start", "Fetching RING0 from %s...", cfg.Genesis.ServerURL)
	ring0Result, err := a.genesisClient.FetchRing0()
	if err != nil {
		a.ring0Why = foundingWhy(err)
		logsink.Error("genesis.error", "RING0 fetch failed: %s", tlsfault.ForTheLog(err))
		limited = ring0Limited(err)

	} else {
		a.ring0Content = ring0Result.Content
		a.ring0Bundle = ring0Result.Bundle
		a.genesisClient.SetToken(ring0Result.Token)
		logsink.Info("genesis.end", "RING0 verified (%d bytes)", len(a.ring0Content))
	}

	logsink.Info("genesis.start", "Fetching Ring 5 from %s...", cfg.Genesis.FirewallURL)
	ring5Result, err := a.genesisClient.FetchRing5()
	if err != nil {
		a.ring5Why = foundingWhy(err)
		logsink.Error("genesis.error", "Ring 5 fetch failed — dashboard remains available, but birth will refuse: %s", tlsfault.ForTheLog(err))
	} else {
		a.ring5Content = ring5Result.Content
		logsink.Info("genesis.end", "Ring 5 verified (%d bytes)", len(a.ring5Content))
	}

	if a.ring0Content == "" {
		logsink.Error("genesis.refusal", "Bootstrap fetch SKIPPED — RING0 did not verify, so no genesis token was minted to present. Birth refuses until the RING0 failure above is resolved.")
		return
	}

	logsink.Info("genesis.start", "Fetching bootstrap packet from %s...", cfg.Genesis.BootstrapURL)
	bootstrapResult, err := a.genesisClient.FetchBootstrap()

	if tokenRefused(err) {
		logsink.Warn("genesis.refusal", "the bootstrap server refused the genesis token RING0 minted (%v) — RING0 and the bootstrap packet are staged again with both hosts reached over one address family", err)
		for i, family := range foundingFamilies {
			logsink.Info("genesis.start", "Fetching RING0 and the bootstrap packet again over %s alone, attempt %d of %d...", family.name, i+2, foundingAttempts)
			time.Sleep(foundingBackoff(i + 1))
			pinned := a.genesisClient.OverFamily(family.network)
			ring0Result, serr := pinned.FetchRing0()
			if serr == nil {
				pinned.SetToken(ring0Result.Token)
				if bootstrapResult, serr = pinned.FetchBootstrap(); serr == nil {
					a.ring0Content, a.ring0Bundle, err = ring0Result.Content, ring0Result.Bundle, nil
					break
				}
			}
			if tokenRefused(serr) {
				logsink.Warn("genesis.refusal", "the bootstrap server refused the genesis token fetched over %s as well (%v)", family.name, serr)
				err = serr
				continue
			}
			logsink.Error("genesis.error", "RING0 and the bootstrap packet could not be fetched over %s: %s", family.name, tlsfault.ForTheLog(serr))
			if foundingWhy(serr) != "" {
				err = serr
			}
			limited = ring0Limited(serr)

			if unreachable := new(*url.Error); !errors.As(serr, unreachable) {
				break
			}
		}
	}
	if err != nil {
		a.bootstrapWhy = foundingWhy(err)
		logsink.Error("genesis.error", "Bootstrap fetch FAILED — birth will refuse until the signed packet verifies (operator law: bootstrap births only): %s", tlsfault.ForTheLog(err))
		return
	}
	a.bootstrapText = bootstrapResult.Content
	logsink.Info("genesis.end", "Bootstrap packet verified (%d bytes)", len(a.bootstrapText))
	return
}

var foundingFamilies = [...]struct{ network, name string }{{"tcp4", "IPv4"}, {"tcp6", "IPv6"}}

const foundingAttempts = 1 + len(foundingFamilies)

func tokenRefused(err error) bool {
	var refused *genesis.StatusError
	return errors.As(err, &refused) && refused.Artifact == "bootstrap" && refused.StatusCode == http.StatusPaymentRequired
}

func ring0Limited(err error) *genesis.StatusError {
	var refused *genesis.StatusError
	if !errors.As(err, &refused) || refused.Artifact != "ring0" || refused.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	logsink.Warn("genesis.refusal", "%s — no further attempt is made in this start", ring0LimitNotice(refused))
	return refused
}

func ring0LimitNotice(limited *genesis.StatusError) string {
	notice := "the genesis server's hourly limit on Ring 0 downloads for this address has answered (HTTP 429)"
	if limited.RetryAfter > 0 {
		notice += fmt.Sprintf(", and says to try again in %s", limited.RetryAfter)
	}
	return notice
}

func (a *App) startFirstboot() (retErr error) {
	logsink.Info("genesis.start", "no identity found — starting the FIRSTBOOT flow")
	cfg := a.configSnapshot()

	release, err := a.claimIdentity(filepath.Dir(cfg.Identity.LedgerPath))
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			release()
		}
	}()

	a.seedHomeDocs()

	a.rings = ring.NewManager()
	limited := a.fetchFoundingArtifactsAsConfigured(cfg)
	if a.lifetime().Err() != nil {

		return fmt.Errorf("the first boot ended before its dashboard started: %w", errStopping)
	}

	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	a.loadRing5()

	a.promptGate = prompt.NewGate(firstbootRings{a}, 0)

	handler := a.buildFirstbootHandler()
	d, derr := a.newDashboard(handler)
	if derr != nil {
		return fmt.Errorf("dashboard credentials: %w", derr)
	}
	a.dashboard = d
	a.dashboard.SetQuiesceGate(a.gate)
	_, err = a.dashboard.Start(tlsDirFor(cfg))
	if err != nil {

		return fmt.Errorf("dashboard start failed: %w", err)
	}

	fmt.Println("\n=== FIRSTBOOT ===")
	a.printDashboardURLs()
	fmt.Print(a.foundingReport(limited))
	return nil
}

func (a *App) foundingReport(limited *genesis.StatusError) string {
	var b strings.Builder
	for _, artifact := range []struct{ name, content string }{
		{"Ring 0", a.ring0Content}, {"Ring 5", a.ring5Content}, {"Bootstrap packet", a.bootstrapText},
	} {
		if artifact.content != "" {
			fmt.Fprintf(&b, "%s: verified ✓\n", artifact.name)
		} else {
			fmt.Fprintf(&b, "%s: NOT verified — birth will fail\n", artifact.name)
		}
	}
	switch {
	case a.bootstrapText != "" && a.ring5Content != "":
		b.WriteString("Open the dashboard to create a new identity.\n")
	case limited != nil:
		fmt.Fprintf(&b, "No identity can be born in this run: %s. Starting the runtime again at once only spends more of that limit.\n", ring0LimitNotice(limited))
	case a.foundingFault() != "":
		fmt.Fprintf(&b, "No identity can be born in this run: %s. Starting the runtime again helps once that is mended.\n", a.foundingFault())
	case a.bootstrapText != "":

		b.WriteString("No identity can be born in this run: Ring 5 did not verify, and the log says why. Start the runtime again to fetch it anew.\n")
	default:
		b.WriteString("No identity can be born in this run: the bootstrap packet did not verify, and the log says why. Start the runtime again to fetch it anew.\n")
	}
	return b.String()
}

func (a *App) buildFirstbootHandler() *dashboard.WSHandler {
	return &dashboard.WSHandler{
		Background: a.runBackground,
		Restart:    a.Restart,
		Speaker:    "system",
		GetStats: func() (*dashboard.StatsResponse, error) {
			return &dashboard.StatsResponse{}, nil
		},
		HandleMessage:  a.handleBootstrapMessage,
		SetProvider:    a.setProviderInfo,
		DeleteProvider: a.deleteProvider,

		RepairProvider:       a.repairBrokenProvider,
		RemoveBrokenProvider: a.removeBrokenProvider,
		HandleGenesis:        a.handleGenesis,

		RestoreNew: &dashboard.RestoreNewHooks{Keys: a.continuity.NewMachineKeys, Upload: a.continuity.NewMachineUpload, Restore: a.continuity.NewMachineRestore},

		GetProviders:        a.providerDirectory,
		SignInProvider:      a.SignInProvider,
		CompleteSignIn:      a.CompleteSignIn,
		CancelSignIn:        a.CancelSignIn,
		CancelProfileSignIn: a.CancelProfileSignIn,
		OAuthCallback:       a.OAuthCallback,

		UpdateCheck:    func() (*dashboard.UpdateState, error) { return nil, errUpdatesUnavailable },
		DiscoverModels: a.discoverModels,
	}
}

type firstbootRings struct{ a *App }

func (f firstbootRings) Ring0() string { return f.a.ring0Content }
func (f firstbootRings) Ring5() string { return f.a.ring5Content }
func (f firstbootRings) Ring3() string { return "" }
func (f firstbootRings) Ring4() string { return "" }

func (a *App) handleBootstrapMessage(ctx context.Context, _ string) (string, error) {
	dashboard.ChatNotRecorded(ctx, "create or restore an identity before sending conversation messages")

	return "No identity lives here yet. Fill in the provider, model, API key, and names, then click Birth — the founding conversation begins with the birth.", nil
}

func (a *App) handleGenesis(ctx context.Context, req *dashboard.GenesisRequest) (string, error) {

	a.birthMu.Lock()
	defer a.birthMu.Unlock()
	bootCfg := a.configSnapshot()

	if fileExists(bootCfg.Identity.LedgerPath) {
		return "", fmt.Errorf("an identity already exists at %s — restart the runtime to load it (a partial birth needs manual cleanup, not a resubmission)", bootCfg.Identity.LedgerPath)
	}

	if a.ring0Content == "" {
		if a.ring0Why != "" {
			return "", fmt.Errorf("RING0 not verified — cannot create identity without a signed constitution (%s)", a.ring0Why)
		}
		return "", fmt.Errorf("RING0 not verified — cannot create identity without a signed constitution")
	}
	if a.ring5Content == "" {
		return "", fmt.Errorf("Ring 5 not verified — cannot create identity without the platform security posture (%s)", orReachable(a.ring5Why, bootCfg.Genesis.FirewallURL))
	}

	if a.bootstrapText == "" {
		return "", fmt.Errorf("bootstrap packet not verified — birth refused: identities are born only through the bootstrap process (%s)", orReachable(a.bootstrapWhy, bootCfg.Genesis.BootstrapURL))
	}
	if req.Endpoint == "" || req.Model == "" {
		return "", fmt.Errorf("provider endpoint and model are required")
	}

	bentry, beff, err := a.birthEntry(req.Provider, req.Endpoint, req.Model)
	if err != nil {
		return "", fmt.Errorf("provider registry: %w", err)
	}
	models, meta, err := a.discoverMetaForEntry(ctx, bentry, req.APIKey)
	if err != nil {
		return "", fmt.Errorf("provider validation failed (%s): %w", req.Endpoint, err)
	}
	if !modelOffered(models, meta, req.Model) {
		return "", fmt.Errorf("model %q is not offered by %s (%d models offered)", req.Model, req.Endpoint, len(models))
	}

	if discovered, ok := meta[req.Model]; ok {
		window, reserve := discoveredWindow(discovered, bentry, bentry.Name, req.Model)
		if bentry.ContextLength == 0 {
			bentry.ContextLength = window
		}
		if bentry.MaxOutputTokens == 0 {
			bentry.MaxOutputTokens = reserve
		}
	}

	bentry = limitModelWindow(bentry, req.Model, beff)
	if err := validateModelWindow(bentry, req.Model); err != nil {
		return "", fmt.Errorf("provider validation failed: %w", err)
	}
	bentry = resolveOutputAllocation(bentry)

	bootConfig, terr := a.clientConfigForEntry(beff, bentry, bootCfg.LLM, req.Model, req.APIKey)
	if terr != nil {
		return "", terr
	}
	bootConfig.MaxInputTokens, _ = promptBudgetFor(bentry, bootCfg.Prompt.MaxTokens)
	bootClient := llm.New(&bootConfig)
	bootResp, err := bootClient.Chat(ctx, []llm.Message{{Role: "system", Content: a.bootstrapText}}, llm.ChatOptions{})

	foundingReason := "founding greeting"
	var refusal *llm.HTTPError
	if errors.As(err, &refusal) && (refusal.StatusCode == http.StatusBadRequest || refusal.StatusCode == http.StatusUnprocessableEntity) &&
		!refusal.AuthFailure() && llm.DialectFor(bootConfig.Provider) == llm.DialectOpenAI {
		logsink.Warn("genesis.refusal", "%s refused the firstboot prompt as the only system message (HTTP %d) — birth makes one more request, the same bytes as the only user message", req.Endpoint, refusal.StatusCode)
		first := err
		if bootResp, err = bootClient.Chat(ctx, []llm.Message{{Role: "user", Content: a.bootstrapText}}, llm.ChatOptions{}); err != nil {
			err = fmt.Errorf("%w — and as the only user message: %v", first, err)
		}
		foundingReason += fmt.Sprintf(" (the founding prompt was sent as the only user message: the provider refused it as the only system message, HTTP %d)", refusal.StatusCode)
	}
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

	name := deriveName(greeting)

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

	if err := a.startLive(); err != nil {
		return "", fmt.Errorf("identity created but startup failed: %w", err)
	}

	a.runBackground(func() { a.pn.PrepareAtBirth(publicNameSettingsOf(a.configSnapshot())) })

	a.dashboard.SwapHandler(a.buildLiveHandler())
	logsink.Info("genesis.end", "genesis complete: identity=%s, handler swapped", name)

	if _, rerr := a.engine.RecordConversationRef(ctx, string(interaction.Resident), greeting, interaction.Details{Reason: foundingReason}); rerr != nil {
		return greeting, fmt.Errorf("identity created and greeted, but its first words were not recorded — "+
			"the chat replay will not show them: %w", rerr)
	}
	return greeting, nil
}

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
	written := birthWritten(entry, name, req.APIKey)
	if err := a.setProvider(written, false); err != nil {
		return "", fmt.Errorf("providers.json: %w", err)
	}
	if entry.typedOnly && a.cfg != nil {
		if env := a.configSnapshot().LLM.APIKeyEnv; env != "" && blockEnvIsFor(written, env) && providerAPIKey(written, "", "") == "" {
			logsink.Warn("config.decision", "%s", movedBirthKeyLine(name, written.URL, env))
		}
	}
	return name, nil
}

func movedBirthKeyLine(provider, address, env string) string {
	return fmt.Sprintf("provider %q is now at %s, and config.json names the variable %s for the identity's key (llm.api_key_env): from the next start its value is sent there. To send another key, enter it on the provider's card (Settings → Providers); to send none, remove llm.api_key_env from config.json",
		provider, addressOrigin(address), env)
}

func birthWritten(entry providerEntry, name, typedKey string) providerEntry {
	entry.Name, entry.Default = name, true
	yes := true
	entry.Chat = &yes
	if typedKey != "" {
		entry.APIKey = typedKey
	}
	entry.typedOnly = false
	return entry
}

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

	if name, found := nameAfterLead(first); found {
		return name
	}
	return "Unnamed"
}

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
	for _, lead := range leads {
		for idx := 0; idx+len(lead) <= len(line); idx++ {
			if !utf8.RuneStart(line[idx]) {
				continue
			}
			if at := line[idx : idx+len(lead)]; at != lead && (lead == "NAME:" || !strings.EqualFold(at, lead)) {
				continue
			}
			if before, _ := utf8.DecodeLastRuneInString(line[:idx]); idx > 0 && (unicode.IsLetter(before) || unicode.IsDigit(before)) {
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

func nameAt(line string, cut int) (string, bool) {
	rest := strings.TrimLeft(line[cut:], " \t.,;:!?'\u201c\u201d\"-")
	if rest == "" {
		return "", false
	}
	name := rest
	if i := strings.IndexAny(name, ".,;:!?\"'()[]-\u2014"); i > 0 {
		name = name[:i]
	}

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

	if runes := []rune(name); len(runes) > 32 {
		name = string(runes[:32])
	}
	if name == "" {
		return "", false
	}
	return name, true
}

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func foundingWhy(err error) string {
	if fault := (*tlsfault.Fault)(nil); errors.As(err, &fault) {
		return fault.Error()
	}
	return ""
}

func (a *App) foundingFault() string {
	for _, why := range []string{a.ring0Why, a.ring5Why, a.bootstrapWhy} {
		if why != "" {
			return why
		}
	}
	return ""
}

func foundingRefusalNotice(r genesis.Refusal) {
	if r.Fault == nil {
		logsink.Info("genesis.start", "%s, attempt %d of %d: answered under a certificate this machine accepts; the refusal before it no longer holds", r.URL, r.Attempt, r.Attempts)
		return
	}
	again := ""
	if r.Attempt < r.Attempts {
		again = "; it is asked for again"
	}
	logsink.Warn("genesis.error", "%s, attempt %d of %d: %s%s", r.URL, r.Attempt, r.Attempts, tlsfault.ForTheLog(r.Fault), again)
}

func orReachable(why, url string) string {
	if why != "" {
		return why
	}
	return "is " + url + " reachable?"
}
