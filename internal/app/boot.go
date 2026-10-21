package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func (a *App) ensureRing5Policy() *firewall.Policy {
	if a.ring5Policy == nil {
		a.ring5Policy = firewall.DefaultPolicy()
	}
	return a.ring5Policy
}

func (a *App) loadRing5() {
	if a.ring5Content == "" {
		if err := a.rings.Set(ring.Ring5, nil); err != nil {
			logsink.Error("ring.refusal", "Ring 5 not cleared: %v", err)
		}
		logsink.Warn("ring.refusal", "Ring 5 unavailable — no posture content loaded")
		return
	}
	policy := a.ensureRing5Policy()

	size := 0
	if err := a.rings.SetFrom(ring.Ring5, func() *ring.RingContent {
		content := a.ring5Content + "\n\n" + policy.LocalFloor()

		if a.toolReg != nil {
			if _, extra := a.toolReg.Roots(); len(extra) > 0 {
				content += "\n\nGranted roots — your operator has widened your reach. You may also read and work under these directories (use ABSOLUTE paths):\n"
				for _, root := range extra {
					content += "- " + root + "\n"
				}
			}
		}
		floorStep("read")
		size = len(content)
		return &ring.RingContent{Level: ring.Ring5, Content: content}
	}); err != nil {
		logsink.Error("ring.refusal", "Ring 5 not set: %v", err)
	}

	logsink.Info("ring.start", "Ring 5 loaded: platform bundle + local floor (%d bytes)", size)
}

func tlsDirFor(cfg Config) string {
	if !cfg.Dashboard.TLS {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "tls")
}

func schemaRefusal(err error, armed bool) (string, bool) {
	var shape *store.ShapeError
	var undeclared *store.UndeclaredObjectError
	var rows *store.RowsRefusedError
	var refusal error
	switch {
	case errors.As(err, &shape):
		refusal = shape
	case errors.As(err, &undeclared):
		refusal = undeclared
	case errors.As(err, &rows):
		refusal = rows
	default:
		return "", false
	}
	detail := refusal.Error()
	if armed {
		detail += ". An update installed this version just before this start and it has not completed a boot, so restarting rolls back to the previous version (if the rollback cannot be done, the log says why)"
	}
	return detail, true
}

type bootStopped struct {
	step string
	err  error
}

func (e *bootStopped) Error() string { return e.step + ": " + e.err.Error() }
func (e *bootStopped) Unwrap() error { return e.err }

func stopped(step string, err error) error { return &bootStopped{step: step, err: err} }

func (a *App) couldNotStart(cfg Config, failed *error) bool {
	var step *bootStopped
	if !errors.As(*failed, &step) || errors.Is(*failed, ledger.ErrLedgerInUse) ||
		errors.Is(*failed, context.Canceled) || errors.Is(*failed, context.DeadlineExceeded) {
		return false
	}
	if _, safe := a.SafeMode(); safe {
		return false
	}

	armed, moment := endsForRollback(cfg), mayPassByItself(step.err)

	if (armed || moment) && endedStartsRestart {
		return false
	}

	said := strings.ReplaceAll((*failed).Error(), "\n", "; ")
	logsink.Error("boot.refusal", "THIS START STOPPED AT A STEP IT COULD NOT COMPLETE — entering BOOT-SAFE (minimal, read-only): %s", said)

	var arm func()
	if armed || moment || passesWithTime(step.err) {
		var retry string
		retry, arm = a.planStartRetry(armed)
		said += retry
	}
	a.rings = nil
	if err := a.startSafeBoot(safeCouldNotStart, said); err != nil {
		*failed = errors.Join(*failed, fmt.Errorf("the page for a start that could not complete did not start either: %w", err))
		return false
	}
	if arm != nil {
		arm()
	}
	*failed = nil
	return true
}

func mayPassByItself(err error) bool {
	if store.MayPass(err) {
		return true
	}
	return anyErrno(err, func(errno syscall.Errno) bool {
		switch errno {
		case syscall.EAGAIN, syscall.EINTR, syscall.EBUSY, syscall.ETIMEDOUT:
			return true
		}
		return false
	})
}

func endsForRollback(cfg Config) bool {
	return updates.RollbackArmed(filepath.Dir(cfg.Identity.LedgerPath))
}

func witnessSafeTrigger(ce *witness.ConflictError) safeTrigger {
	if ce.Local {
		return safeWitnessConflict
	}
	return safeWitnessSays
}

func (a *App) newToolRegistry(cfg Config) (*tools.Registry, error) {
	reg := tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{
		ShellSeconds:    cfg.Tools.ShellTimeoutSeconds,
		WebFetchSeconds: cfg.Tools.WebFetchTimeoutSeconds,
	})
	if err := checkNameOwners(reg); err != nil {
		return nil, err
	}
	a.applyLocalFetch(cfg, reg)

	reg.SetSafeSource(a.SafeMode)

	reg.SetShellWallPlaces(func() ([]*firewall.Rule, error) {
		cfg := a.configSnapshot()
		kept, _, err := sandboxOffPlaces(a.activeConfig(cfg), a.grantedRoots())
		return kept, err
	})
	if err := applyDisabledTools(reg, cfg.Tools.Disabled); err != nil {
		return nil, err
	}
	return reg, nil
}

func (a *App) startLive() (retErr error) {
	cfg := a.configSnapshot()

	release, err := a.claimIdentity(filepath.Dir(cfg.Identity.LedgerPath))
	if err != nil {
		return err
	}
	defer func() {

		if retErr != nil && a.couldNotStart(cfg, &retErr) {
			return
		}
		if retErr != nil {
			release()
		}
	}()
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, a.closeLiveResources())
		}
	}()

	if sr := hostcap.Can(hostcap.SelfReplace); sr.Available {
		rolled := updates.CheckRollback(filepath.Dir(cfg.Identity.LedgerPath))

		if rolled == "" {
			keepConfigBeforeUpdate(cfg.SourcePath, updates.RequireSettledBootUpdate(filepath.Dir(cfg.Identity.LedgerPath)))
		}
		putBack := func() error { return restoreConfigBeforeUpdate(cfg.SourcePath) }
		if err := afterRollback(rolled, putBack, a.releaseIdentityClaim, reexecSelf); err != nil {
			return err
		}
		if rolled != "" {

			os.Exit(0)
		}
	} else {
		logsink.Info("updates.decision", "rollback machinery idle on this host — %s", sr.Reason)
	}

	a.seedHomeDocs()

	if n := takeStartAttempt(); n > 0 {
		a.startAttempt = n
	}

	a.continuity.SweepKeyCheckScratch(continuitySettingsOf(cfg))
	kp, err := loadKeyPair(cfg.Identity.KeyPath)
	if err != nil {

		artifact, aerr := recordPlace(cfg.Identity.LedgerPath)

		if aerr != nil {
			return stopped("load identity key", errors.Join(err, fmt.Errorf("the record's place at %s cannot be inspected: %w", cfg.Identity.LedgerPath, aerr)))
		}
		if artifact != "" && !endsForRollback(cfg) {
			logsink.Error("boot.refusal", "THE IDENTITY'S KEY DID NOT LOAD AND ITS RECORD IS HERE — entering BOOT-SAFE (minimal, read-only): %v", err)
			detail := fmt.Sprintf("the key at %s did not load: %v; the record is at %s", cfg.Identity.KeyPath, err, artifact)

			trigger, cause := safeKeyMissing, err
			if _, _, nerr := namedKey(cfg.Identity.LedgerPath); nerr != nil {
				if genesis.RecordUnread(nerr) {
					detail += fmt.Sprintf("; the record does not read either: %v", nerr)
					cause = errors.Join(err, nerr)
				} else {
					trigger = safeKeyUnnamed
					detail += fmt.Sprintf("; the record does not name the key that signs it: %v", nerr)
				}
			}

			var arm func()
			if passesWithTime(cause) {
				var retry string
				retry, arm = a.planStartRetry(false)
				detail += retry
			}
			if err := a.startSafeBoot(trigger, detail); err != nil {
				return err
			}
			if arm != nil {
				arm()
			}
			return nil
		}

		if artifact == "" && !endsForRollback(cfg) && a.continuity.HoldsRestoreSource(continuitySettingsOf(cfg)) {
			logsink.Error("boot.refusal", "NO RECORD AT %s AND THE IDENTITY'S KEY DID NOT LOAD — entering BOOT-SAFE (minimal, read-only): a snapshot or a set kept aside is here: %v", cfg.Identity.LedgerPath, err)
			return a.startSafeBoot(safeRecordAndKeyMissing, fmt.Sprintf("no record at %s, and the key at %s did not load: %v", cfg.Identity.LedgerPath, cfg.Identity.KeyPath, err))
		}

		if endsForRollback(cfg) {
			return stopped("load identity key", err)
		}
		return fmt.Errorf("load identity key: %w", err)
	}
	a.keyPair = kp
	keyOrigin := cfg.Identity.KeyPath
	a.keyOrigin.Store(&keyOrigin)

	if err := a.continuity.PerformPendingRestore(context.Background(), continuitySettingsOf(cfg), kp); err != nil {
		logsink.Error("boot.refusal", "A RESTORE DID NOT FINISH — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(safeRestore, err.Error())
	}

	settle, owed, err := continuity.ReadRestoreSettlement(continuitySettingsOf(cfg))
	if err != nil {
		logsink.Error("boot.refusal", "A RESTORE'S SETTLEMENT DOES NOT READ — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(safeRestore, err.Error())
	}

	if artifact, err := ledger.RecordArtifact(cfg.Identity.LedgerPath); err != nil {
		return stopped("inspect the identity record", err)
	} else if artifact == "" {
		if endsForRollback(cfg) {
			return stopped("inspect the identity record", fmt.Errorf("no record at %s, and an update installed this version just before this start: the next start restores the previous version", cfg.Identity.LedgerPath))
		}
		logsink.Error("boot.refusal", "NO RECORD AT %s — entering BOOT-SAFE (minimal, read-only): the key is here, and a snapshot or a set kept aside can be restored from the page", cfg.Identity.LedgerPath)
		return a.startSafeBoot(safeRecordMissing, fmt.Sprintf("no record at %s", cfg.Identity.LedgerPath))
	}

	lg, err := a.openVerifiedRecord(cfg, kp)
	if lg == nil {
		return err
	}

	if a.ring5Content == "" {
		if cfg.Genesis.FirewallURL == "" {
			return a.startSafeBoot(safeRing5, "security_posture.absent: no Ring 5 server is configured")
		}
		gc := cfg.Genesis.client(a.lifetime())
		r5, err := gc.FetchRing5()
		if err != nil && a.lifetime().Err() != nil {

			return fmt.Errorf("the start ended before Ring 5 was fetched: %w", errStopping)
		}
		if err != nil {

			detail := fmt.Sprintf("security_posture.verify_fail: required Ring 5 unavailable or unverifiable: %v", err)
			if fault := (*tlsfault.Fault)(nil); errors.As(err, &fault) {
				return a.startSafeBoot(safeRing5Refused, detail)
			}
			return a.startSafeBoot(safeRing5, detail)
		}
		a.ring5Content = r5.Content
		logsink.Info("ring.start", "Ring 5 fetched from %s (%d bytes)", cfg.Genesis.FirewallURL, len(a.ring5Content))
	}

	st, err := openForStartup(context.Background(), cfg.Identity.DBPath,
		store.MirrorHead{Seq: lg.LastSeq(), Hash: lg.LastHash()},
		func(yield func(*ledger.Event) error) error { return recordStream(cfg.Identity.LedgerPath, yield) })
	if err != nil {
		var ahead *store.MirrorAheadError
		var unreadable *store.MirrorReadError
		if errors.As(err, &ahead) {
			return a.startSafeBoot(safeRecord, ahead.Error())
		}
		if errors.As(err, &unreadable) {
			return a.startSafeBoot(safeRecord, fmt.Sprintf("projection mirror could not be read before ledger replay: %v", unreadable))
		}
		var missing *store.MissingEventTargetError
		if errors.As(err, &missing) {
			return a.startSafeBoot(safeRecord, fmt.Sprintf("ledger replay found an invalid event reference: %v", missing))
		}

		if reason, ok := schemaRefusal(err, updates.RollbackArmed(filepath.Dir(cfg.Identity.LedgerPath))); ok {
			logsink.Error("boot.refusal", "THE DATABASE HOLDS A SCHEMA THIS BINARY DOES NOT DECLARE — entering BOOT-SAFE (minimal, read-only): %v", err)
			return a.startSafeBoot(safeSchema, reason)
		}

		var refused *store.ReplayRefusedError
		if errors.As(err, &refused) && !endsForRollback(cfg) {
			logsink.Error("boot.refusal", "THE RECORD HOLDS AN ENTRY THIS BINARY DOES NOT TAKE — entering BOOT-SAFE (minimal, read-only): %v", err)
			return a.startSafeBoot(safeEntryRefused, fmt.Sprintf("record %d (%s): %v", refused.Seq, refused.Type, refused.Err))
		}

		var boundary *store.ReplayBoundaryError
		if errors.As(err, &boundary) && !endsForRollback(cfg) {
			logsink.Error("boot.refusal", "THE REPLAY DID NOT END AT THE VERIFIED RECORD — entering BOOT-SAFE (minimal, read-only): %v", err)
			return a.startSafeBoot(safeRecord, fmt.Sprintf("ledger replay did not end at the verified record: %v", boundary))
		}

		return stopped("prepare schema and replay", err)
	}
	a.store = st
	st.SetWorkObserver(a.workObserved)

	if err := broker.ClearOrphanTempFiles(filepath.Join(identityDataDir(cfg.Identity.LedgerPath), "plugins-data")); err != nil {
		logsink.Warn("plugins.error", "orphan temporary files retained: %v", err)
	}
	if err := a.importLegacyContacts(saveConfig); err != nil {
		logsink.Warn("config.error", "historical contacts were not fully imported: %v; retained source is available for retry; normal startup continues", err)
	}
	st.SetOperatorASCII(cfg.Dashboard.OperatorASCII != nil && *cfg.Dashboard.OperatorASCII)
	st.SetOperatorVoices(cfg.Speech.OperatorSpeakers)
	st.SetSelfModelMaxTokens(cfg.Prompt.SelfModelMaxTokens)

	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	rc, err := genesis.LoadRing0(lg)
	if err != nil {

		var pathErr *os.PathError
		var systemErr syscall.Errno
		if errors.As(err, &pathErr) || errors.As(err, &systemErr) || endsForRollback(cfg) {
			return stopped("load Ring 0", err)
		}
		return a.startSafeBoot(safeRecord, fmt.Sprintf("the record's birth could not be loaded: %v", err))
	}

	if a.keyPair == nil {
		return fmt.Errorf("load Ring 0: no identity key to verify the constitution against")
	}
	if err := a.rings.SealConstitution(rc, a.keyPair.PublicKeyBytes()); err != nil {
		return a.startSafeBoot(safeRecord, fmt.Sprintf("the Ring 0 constitution could not be installed: %v", err))
	}

	st, err = a.activateDatabaseFormat(context.Background(), cfg, st)
	if err != nil {
		return a.startSafeBoot(safeDatabaseFormat, err.Error())
	}
	a.store = st
	st.SetWorkObserver(a.workObserved)
	st.SetOperatorASCII(cfg.Dashboard.OperatorASCII != nil && *cfg.Dashboard.OperatorASCII)
	st.SetOperatorVoices(cfg.Speech.OperatorSpeakers)
	st.SetSelfModelMaxTokens(cfg.Prompt.SelfModelMaxTokens)

	if owed {
		if err := a.continuity.SettleRestore(continuitySettingsOf(cfg), settle); err != nil {
			return stopped("settle the restore", err)
		}
	}
	a.wakeMu.Lock()
	a.timeFac = cognitive.NewTIME(st, st)
	a.wakeMu.Unlock()
	a.timeFac.SetSafeSource(func() bool { _, s := a.SafeMode(); return s })
	if err := a.timeFac.DeleteLegacyAlarm("heartbeat", "retired mechanism: heartbeat is a goroutine ticker, not an alarm"); err != nil {
		return stopped("remove retired heartbeat alarm", err)
	}

	snaps, err := st.RingSnapshots()
	if err != nil {
		return stopped("load ring snapshots", err)
	}

	for _, sn := range snaps {
		if err := a.rings.SetSection(ring.RingLevel(sn.RingLevel), sn.Section, sn.Content); err != nil {

			logsink.Error("ring.refusal", "ring snapshot %q at ring %d skipped: %v", sn.Section, sn.RingLevel, err)
		}
	}
	if len(snaps) > 0 {
		logsink.Info("ring.start", "Ring snapshots restored: %d sections", len(snaps))
	}
	brief, written, err := st.Brief()
	if err != nil {
		return stopped("load morning brief", err)
	}
	switch {
	case brief == "":
	case written.IsZero():

		logsink.Error("ring.refusal", "the stored morning brief's date cannot be read — the brief is not restored to the prompt; the next brief written replaces it")
	default:

		a.rings.RestoreBrief(brief, written)
	}

	a.loadRing5()

	a.askWindowIfUndeclared(cfg.LLM)
	cc, llmEntry, err := a.resolveLLM()
	if err != nil {
		return stopped("LLM substrate", err)
	}

	a.sayContactLinesTheDoorRefuses(a.configSnapshot())
	if cc.APIKey == "" && cc.Credential == nil {

		logsink.Info("llm.refusal", "no API key on provider %q — expected for a local endpoint; if this provider requires one, chat will fail at call time with the provider's own error (set it in the dashboard, or %s)", llmEntry.Name, keyRemedy(llmEntry, cfg.LLM))
	}
	lg.SetModelID(cc.Model)

	a.adoptRuntime(llmEntry, nil)
	promptBudget := a.rememberPromptBudget(llmEntry, promptCeilingFor(cfg, llmEntry, cc.Model))
	_, budgetSrc := a.currentPromptBudget()

	a.llmClient = a.newLLMClient(cc, promptBudget)
	a.adoptRuntime(llmEntry, a.llmClient)
	a.llmSwap = newSwappableLLM(a.llmClient)

	toolReg, err := a.newToolRegistry(cfg)
	if err != nil {
		return err
	}
	a.toolReg = toolReg

	if names, aerr := st.AbandonUnfinishedToolCalls(); aerr != nil {
		logsink.Warn("boot.error", "could not reconcile in-flight tool record: %v", aerr)
	} else if len(names) > 0 {
		logsink.Warn("boot.refusal", "%d tool call(s) were in flight at last shutdown: %s — side effects unverified", len(names), strings.Join(names, ", "))
		a.bootInterrupted = names
	}

	a.safeTools = &safeToolRecord{}
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st, safe: a.safeTools}, appEmitter{a: a}, conversation.Config{

			MaxToolResultChars: toolResultCharsSetting.n(cfg),

			WrapUpAtCallLimit: true,

			ContextBudgetTokens: promptBudget,

			HistoryChunk: cfg.Prompt.RecentTurns / 2,

			ContextBudgetFallback: budgetSrc == budgetFallback,
			ThinkingBudget:        llmEntry.ThinkingBudget,

			PredictedThisTurn: a.predictedThisTurn,
			ReplaySafe:        replaySafeHook(toolReg),

			ThinkingKept: a.thinkingKept,

			ImagesRefused: a.imagesRefused,
		})

	a.conv.SetSteering(a)

	facilityBudget, facilitySource := a.currentFacilityBudget()
	a.promptGate = prompt.NewGate(appRingSource{rm: a.rings, priorities: a.store}, facilityBudget)
	a.promptGate.SetWindowKnown(facilitySource.window())
	a.composer = a.newComposer(promptBudget)
	a.composer.SetIdentitySource(st)

	a.composer.SetName(a.store.IdentityName())

	a.composer.SetPluginOperations(pluginOperations(toolReg))

	a.applySubstrate(cfg)
	toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	if len(cfg.Tools.ExtraRoots) > 0 {
		a.loadRing5()
	}
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st, onIntegrity: a.integrityLost, onAppend: a.ledgerAppended}
	door.recoverSeal = func() error {

		heads, err := a.bootHeadVerifier(cfg)
		if err != nil {
			return fmt.Errorf("seal recovery witness proof unavailable: %w", err)
		}
		return lg.RecoverSeal(kp.PublicKeyBytes(), heads)
	}
	a.door = door

	witnessClient := witness.New(cfg.Witness.URL, cfg.Witness.TLSSPKISHA256)
	a.witnessProbe = witnessClient

	witnessClient.SetGenesisURL(cfg.Genesis.ServerURL)
	anchorer := witness.NewAnchorer(witnessClient, lg, witness.AsIdentityKey(kp), a.store, a.store,
		witnessMinter{door: door},
		cfg.Witness.IntervalEvents, cfg.Witness.PlatformPubkeyPath)

	anchorer.SetSealer(lg)

	anchorer.SetAppendPause(door)
	anchorer.SetOnIntegrityConflict(func(ce *witness.ConflictError) {

		if ce.WitnessSide {
			logsink.Error("witness.refusal", "the witness's row does not continue this record — NOT a reason for SAFE; the identity runs unwitnessed from here until the witness's side is put right: %v", ce)
			id := fmt.Sprintf("witness_row_%d", anchorer.LastAnchoredSeq())
			said := "The witness answered with a receipt this record cannot carry: its row for this identity no longer continues the record's witness heads. Nothing in its answer contradicts the record, which is intact, and the identity keeps running. Until whoever runs the witness puts that row right, new records are not witnessed and no segment is sealed. The detail: " + ce.Error()
			if _, err := a.store.AddOutboxNoticeOnce(id, "operator", "", said, nil); err != nil {
				logsink.Warn("witness.error", "the operator's notice that anchoring is off could not be written: %v", err)
			}
			return
		}

		logsink.Error("witness.refusal", "WITNESS INTEGRITY CONFLICT — entering SAFE MODE: %v", ce)
		a.enterSafeFor(witnessSafeTrigger(ce), fmt.Sprintf("witness rollback/fork conflict: %v", ce))
	})

	anchorer.SetOnUnrecognisedConflict(func(ce *witness.ConflictError) {
		a.tellUnrecognisedWitnessReply(cfg.Witness.URL, ce, witnessNoticeNow())
	})
	if cfg.Witness.URL != "" {
		a.anchorer = anchorer
	}

	if _, safe := a.SafeMode(); !safe {
		if err := anchorer.SettlePending(); errors.Is(err, witness.ErrPendingReceiptRefused) {
			logsink.Error("witness.refusal", "the witness receipt pending beside the ledger was refused at boot — anchoring is refused until it is settled: %v", err)
		} else if err != nil {
			logsink.Error("witness.error", "the witness receipt pending beside the ledger was not settled at boot — the next anchor pass tries again first: %v", err)
		}
	}

	pluginOpts, err := a.buildPluginOptions(st, toolReg, door)
	if err != nil {

		logsink.Error("plugins.refusal", "broker/trust-root config REFUSED, all plugins run quarantined: %v", err)
		pluginOpts = nil
	}

	a.sections = sections.NewRegistry()
	a.sections.SetSafeSource(a.SafeMode)

	a.pluginToolReg = toolReg
	a.pluginOpts = pluginOpts
	a.rescanPlugins(a.bgCtx)

	if ds := cfg.Plugins.DevSection; ds != nil {
		if reason, safe := a.SafeMode(); safe {
			logsink.Warn("dev.refusal", "dev section %q: REFUSED — runtime is in SAFE mode (%s); unverified bytes stay off the screen", ds.ID, reason)
		} else if sec, derr := sections.ActivateDev(ds.ID, ds.Path); derr != nil {
			logsink.Warn("dev.refusal", "dev section %q: REFUSED, skipped: %v", ds.ID, derr)
		} else if rerr := a.sections.Register(sec); rerr != nil {
			logsink.Warn("dev.refusal", "dev section %q: registration REFUSED: %v", ds.ID, rerr)
		} else {
			a.pluginMu.Lock()
			a.sectionActs = append(a.sectionActs, sec)
			a.pluginMu.Unlock()
			logsink.Warn("dev.decision", "dev section %q serving UNVERIFIED from %s (banner on, cache off, SAFE refuses)", ds.ID, ds.Path)
		}
	}

	a.executor = cognitive.NewExecutor(st)
	a.executor.SetHolds(a.fg)
	a.executor.SetWorkers(cfg.Agency.QueueWorkers)
	a.executor.SetQuiesceGate(a.gate)
	a.executor.RegisterHandler(&alarmHandler{time: a.timeFac})
	a.executor.RegisterHandler(&timerWakeHandler{a: a})

	a.store.SetClaimLimit(identity.TimerWakeWorkKind, 1)
	a.executor.RegisterHandler(&subagentHandler{a: a})
	a.queueWake = a.executor.Wake

	a.wireProjects(cfg)
	a.adoptEngine(identity.NewEngine(st, door, a.rings, toolDiscovererAdapter{toolReg}, identity.Ports{
		Projects:     projectsAdapter{a},
		Voice:        voiceModeAdapter{a},
		HeardHistory: a.recallHeard,
		WorkState:    a.readWorkState,
		Continuity:   a.continuity.Port(),
		Timers:       appTimers{time: a.timeFac, read: identity.NewStoreTimers(st)},
		Embedder:     sourceEmbedder{a},

		ConversationWindow: cfg.Memory.ConversationVectorWindow,
		ToolResultChars:    cfg.Prompt.MaxToolResultChars,
		AlarmFirings:       a.alarmFirings,
		Reachable:          func(name string) bool { return len(a.reachFor(name)) > 0 },
		AskProposer:        a.proposeAsk,
		WorkStarted:        a.workStarted,
		WorkWake:           a.executor.Wake,
		YieldGate:          a.yieldGate,
		RouteLimits:        a.spawnLimitsFor,
	}))
	a.applyAgency(cfg)

	if p, why := a.activeOpenProject(); p == nil && why != "" {
		logsink.Info("project.decision", "restored project focus dropped — %s", why)
		if err := a.store.SetActiveProject(""); err != nil {
			logsink.Error("project.error", "the dropped focus could not be cleared, and the next start meets it again: %v", err)
		}
	}

	a.timeFac.SetFireObserver(func(owner, alarmID string, accepted bool) {
		a.emitPluginEvent(pluginhost.TopicAlarmFired, map[string]any{"owner": owner, "alarm_id": alarmID, "accepted": accepted})
	})
	a.timeFac.RegisterOwner(a.continuity.AlarmOwner())
	a.timeFac.RegisterOwner(a.pn.AlarmOwner())
	a.timeFac.RegisterOwner(memoryOwner{a})

	timerOwner := identity.NewTimerDeliveryOwner(a.engine)

	if r, ok, err := st.LastRestore(); err != nil {
		logsink.Warn("restore.error", "%v — an alarm due before it fires without saying it may already have been delivered", err)
	} else if ok {
		timerOwner.RestoredMs = r.At.UnixMilli()
	}
	timerOwner.OnSafeWake = a.drainSafeWakes
	a.timerOwner = timerOwner
	a.timeFac.RegisterOwner(timerOwner)

	toolReg.ObserveFetches(a.engine.NoteExternalFetch)

	if reason, safe := a.SafeMode(); safe {
		a.applySafeState(reason)
	}

	if swept, err := a.store.SweepOrphanWorkSessions(); err != nil {
		logsink.Warn("work.error", "orphan sweep failed: %v", err)
	} else if len(swept) > 0 {
		logsink.Info("work.decision", "closed %d work session(s) orphaned by a previous shutdown", len(swept))
		a.bootSwept = swept
	}

	a.updateChecker = updates.NewChecker(
		func() *sigenvelope.PublicKeyEnvelope {
			root, err := packagefmt.PinnedOrShipped(a.configSnapshot().Plugins.PlatformRoot, packagefmt.KeyTypePlatformRelease)
			if err != nil {
				return nil
			}
			return root
		},
		func() string { return Current() },
		func() bool { return a.configSnapshot().Updates.Automatic },
		func() string { return a.configSnapshot().Updates.Repo },
		a.gate,
		filepath.Dir(cfg.Identity.LedgerPath),
		func() bool { _, s := a.SafeMode(); return s },
		func() bool { return packagefmt.HostTopology() == "mobile_app_host" },
	)

	a.updateChecker.SetRunner(a.runBackground)

	startDashboard := a.dashboard == nil
	if startDashboard {
		d, derr := a.newDashboard(a.buildLiveHandler())
		if derr != nil {
			return fmt.Errorf("dashboard credentials: %w", derr)
		}
		a.dashboard = d
		a.dashboard.SetQuiesceGate(a.gate)
	}

	if cfg.Tools.SandboxOff {
		a.applySavedSandbox(cfg)
		a.loadRing5()
	}
	a.dashboard.SetWebhookHandler(a.handleWebhook)

	a.projectsPush = func(id string, ws *dashboard.WorkspaceState) {
		a.dashboard.PushWorkspace(id, ws)
	}
	a.projectsListPush = a.dashboard.BroadcastProjects
	a.voiceEventSink = a.dashboard.BroadcastVoiceEvent
	a.voiceControlSink = a.dashboard.DeliverVoiceControl
	a.voiceReplySink = a.dashboard.BroadcastVoiceReply
	a.voiceHushSink = a.dashboard.BroadcastVoiceHush
	if startDashboard {
		_, err := a.dashboard.Start(tlsDirFor(cfg))
		if err != nil {
			return fmt.Errorf("dashboard start: %w", err)
		}
		fmt.Printf("AII OS — %s\n", a.resolveDisplayName())
		a.printDashboardURLs()
		fmt.Printf("Ledger seq: %d\n", lg.LastSeq())
	}

	a.dashboard.SetSections(a.sections)
	a.dashboard.SetLayoutSource(a.currentUILayout)
	a.loadUILayout(false)
	a.dashboard.SetThemeSource(a.currentUITheme)
	a.loadUITheme(false)

	a.dashboard.SetUIOverlay(a.uiOverlayDir())

	a.dashboard.SetApprovedScripts(a.configSnapshot().Dashboard.ApprovedScripts)

	a.seedOverlayREADME()

	a.dashboard.SetBuildStamp(BuildIdentity())

	a.store.OnOutboxWrite(a.dashboard.PokeOutbox)

	a.store.OnOutboxWrite(a.pokeOutboxForNotices)
	a.wireInteractionView()

	bgCtx := a.bgCtx
	a.warnTempHome(cfg.Identity.LedgerPath)
	a.snapshotUILayoutPath(cfg.Identity.LedgerPath)

	a.timeFac.SetAlarmEnqueuer(alarmEnqueuerAdapter{ex: a.executor})

	a.installPlatformWake()
	if err := a.wireCognitive(bgCtx, anchorer, door, cfg); err != nil {
		return err
	}

	a.settleInterruptedSends()
	a.executor.Start(bgCtx)

	a.startMachineLink(bgCtx, cfg)
	a.startPluginSweep(bgCtx)
	a.runBackground(func() { a.catalog.Run(bgCtx, a.catalogChanged) })
	a.runBackground(func() { a.runOutbox(bgCtx) })
	a.startSelectedModalities(llmEntry, cc.Model)

	a.goLive(cfg)

	if err := a.pn.Wire(publicNameSettingsOf(cfg)); err != nil {
		logsink.Warn("route.refusal", "%v — the name is claimed and the dashboard serves the local certificate until this is fixed", err)
	}

	if a.dashboard != nil {
		bootCfg := cfg
		a.runBackground(func() {
			defer startNameStep()
			a.pn.LearnServiceZone()
			a.pn.AutoClaim(publicNameSettingsOf(bootCfg))

			a.pn.Signal()
			for _, line := range a.pn.Advice(publicNameSettingsOf(bootCfg), a.dashboard.TLSMaterial()) {

				fmt.Println(line)
			}
		})
	}
	a.runBackground(func() { a.watchConfig(cfg.SourcePath) })
	a.runBackground(a.watchUILayout)
	a.runBackground(a.watchUITheme)
	a.runBackground(a.watchUIOverlay)
	a.runBackground(a.watchProjects)
	a.installReloadSignal()

	updates.WriteBootMarker(filepath.Dir(cfg.Identity.LedgerPath))
	dropConfigBeforeUpdate(cfg.SourcePath, updates.RequireSettledBootUpdate(filepath.Dir(cfg.Identity.LedgerPath)))

	a.runBackground(func() {
		a.updateChecker.Run(bgCtx)
	})

	a.executor.Wake()
	return nil
}

func (a *App) openVerifiedRecord(cfg Config, kp *crypto.KeyPair) (*ledger.Ledger, error) {

	heads, err := a.bootHeadVerifier(cfg)
	if err != nil {
		logsink.Error("boot.refusal", "BESIDE THE LEDGER DO NOT VERIFY — entering BOOT-SAFE (minimal, read-only): %v", err)
		return nil, a.startSafeBoot(safeRecord, fmt.Sprintf("witness keys beside the ledger do not verify: %v", err))
	}

	var tail *witness.LocalTail
	var tailErr error
	var tailCheck *witness.TailCheck

	lg, verified, err := ledger.OpenVerified(cfg.Identity.LedgerPath, kp.PublicKeyBytes(), heads, func(evt *ledger.Event) error {
		if tailCheck == nil {
			tail, tailErr = witness.ReadLocalTail(filepath.Dir(cfg.Identity.LedgerPath))
			tailCheck = witness.NewTailCheck(tail)
		}
		return tailCheck.Visit(evt)
	}, func(through uint64) error {
		if tailErr == nil {
			tailErr = tailCheck.Held()
		}
		if tailErr != nil {
			return tailErr
		}
		return store.CheckAcknowledgedPrefix(cfg.Identity.DBPath, through)
	})
	if err != nil {

		var pathErr *os.PathError
		var systemErr syscall.Errno
		if errors.As(err, &pathErr) || errors.As(err, &systemErr) || errors.Is(err, ledger.ErrLedgerInUse) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, stopped("open verified ledger", err)
		}
		var ahead *store.MirrorAheadError
		var unreadable *store.MirrorReadError
		if errors.As(err, &ahead) {
			return nil, a.startSafeBoot(safeRecord, ahead.Error())
		}
		if errors.As(err, &unreadable) {
			return nil, a.startSafeBoot(safeRecord, fmt.Sprintf("projection mirror could not be read before ledger replay: %v", unreadable))
		}
		var verification *ledger.VerifyFailure
		if tailErr != nil && !errors.As(err, &verification) {
			return nil, a.startSafeBoot(safeRecord, fmt.Sprintf("witness-tail check failed at startup: %v", tailErr))
		}
		if errors.Is(err, ledger.ErrRecordUnreadable) {
			return nil, a.startSafeBoot(safeRecord, fmt.Sprintf("the ledger could not be read at startup: %v", err))
		}
		if !errors.As(err, &verification) && !errors.Is(err, ledger.ErrRecordUnreadable) && !errors.Is(err, ledger.ErrTailConflict) && !errors.Is(err, ledger.ErrSegmentSet) {
			return nil, stopped("open verified ledger", err)
		}

		logsink.Error("boot.refusal", "LEDGER CHAIN VERIFICATION FAILED — entering BOOT-SAFE (minimal, read-only): %v; witness: %s", err, heads.Summary())
		return nil, a.startSafeBoot(safeRecord, fmt.Sprintf("chain verification failed at startup: %v; witness: %s", err, heads.Summary()))
	}

	a.ledger = lg
	if heads.Unverified() > 0 {
		logsink.Warn("boot.refusal", "%d witness heads in the tail carry receipts under keys not persisted beside the ledger — accepted on the identity's proof; they will not seal", heads.Unverified())
	}

	a.bootChain = fmt.Sprintf("verified at boot through record %d", verified)
	a.bootAttested = -1
	if seq, hash, ok := heads.Attested(); ok {
		a.bootAttested, a.bootAttestedHash = int64(seq), hash
		a.bootChain += fmt.Sprintf("; witnessed through record %d", seq)
	} else {
		a.bootChain += "; no record witnessed"
	}
	if tail != nil {
		a.bootChain += fmt.Sprintf("; holds record %d, the last the witness's tail names", tail.LedgerOrdinal)
	}
	return lg, nil
}

func (a *App) warnTempHome(ledgerPath string) {
	home := filepath.Dir(ledgerPath)
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = home
	}
	slash := filepath.ToSlash(abs)
	tmp := filepath.ToSlash(os.TempDir())
	if strings.HasPrefix(slash, tmp+"/") || slash == tmp || strings.HasPrefix(slash, "/tmp/") {
		logsink.Warn("boot.refusal", "the identity's home (%s) is under a TEMPORARY directory — temp directories are cleaned by the OS and by habit, so move the data dir to durable storage unless this identity is deliberately disposable", abs)
	}
}

func replaySafeHook(reg *tools.Registry) func(llm.ToolCall) bool {
	if reg == nil {
		return nil
	}
	return func(tc llm.ToolCall) bool { return reg.ReplaySafe(routedCall(reg, tc).Function.Name) }
}

func (a *App) bootHeadVerifier(cfg Config) (*witness.HeadVerifier, error) {
	return a.headVerifierFor(cfg, filepath.Dir(cfg.Identity.LedgerPath))
}

func (a *App) headVerifierFor(cfg Config, ledgerDir string) (*witness.HeadVerifier, error) {
	platform, err := witnessPlatform(cfg)
	if err != nil {
		return nil, err
	}
	return witness.LoadHeadVerifier(ledgerDir, platform)
}

func witnessPlatform(cfg Config) (*witness.PublicKeyEnvelope, error) {
	return witness.LoadPlatformEnvelope(cfg.Witness.PlatformPubkeyPath)
}

func (a *App) newComposer(promptBudget int) *prompt.Composer {
	c := prompt.New(a.rings, promptBudget)
	c.SetClosing(conversation.SystemAdditions())
	return c
}
