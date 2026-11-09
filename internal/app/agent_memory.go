package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/agentmemory"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var otherAgentStores = agentmemory.DefaultStores

const (
	agentMemoryNotice     = "agent_memory_birth"
	agentNotesPressNotice = "agent_notes_copy"
)

type agentMemoryCopy struct {
	report agentmemory.Report
	err    error
	dst    string

	absent bool
}

func copyAgentMemory(ctx context.Context, all Config) *agentMemoryCopy {
	return copyAgentNotes(ctx, all, all.Tools.CWD, agentmemory.Copy)
}

func agentStores(all Config) []agentmemory.Store {
	if set := all.Genesis.AgentMemory; set != nil && len(set.Stores) > 0 {
		return set.Stores
	}
	return otherAgentStores()
}

func agentNotesFound(all Config) bool { return agentmemory.Present(agentStores(all)) }

func copyAgentNotes(ctx context.Context, all Config, home string, copyNotes func(context.Context, []agentmemory.Store, string, agentmemory.Limits, func(string) bool, time.Time) (agentmemory.Report, error)) *agentMemoryCopy {
	cfg := AgentMemoryConfig{}
	if set := all.Genesis.AgentMemory; set != nil {
		cfg = *set
	}
	if cfg.Off {
		logsink.Info("seed.decision", "other agents' notes: not copied (genesis.agent_memory.off)")
		return nil
	}
	stores := agentStores(all)
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	out := &agentMemoryCopy{dst: filepath.Join(home, agentmemory.Folder)}

	if !agentmemory.Present(stores) {
		out.absent = true
		logsink.Info("seed.decision", "other agents' notes: none found — no store's directory is on this machine")
		return out
	}
	kept, err := agentMemoryKept(all, home)
	if err != nil {
		out.err = fmt.Errorf(keptUnnamed, err)
		logsink.Warn("seed.error", "other agents' notes: not copied: %v", out.err)
		return out
	}
	out.report, out.err = copyNotes(ctx, stores, out.dst, agentmemory.Limits{
		Extensions: cfg.Extensions, MaxFileBytes: cfg.MaxFileBytes, MaxBytes: cfg.MaxBytes, MaxFiles: cfg.MaxFiles,
		MaxEntries: cfg.MaxEntries, MaxDepth: cfg.MaxDepth, Secrets: cfg.SecretPatterns,
	}, kept, time.Now())
	switch {
	case out.err != nil:
		logsink.Warn("seed.error", "other agents' notes: the copy into %s did not finish: %v — %s", out.dst, out.err, out.report.Summary())
	case out.report.Files() == 0:
		logsink.Info("seed.decision", "other agents' notes: nothing copied — %s", out.report.Summary())
	default:
		logsink.Info("seed.end", "other agents' notes: %d files copied into %s — %s", out.report.Files(), out.dst, out.report.Summary())
	}
	return out
}

func agentMemoryKept(cfg Config, home string) (func(string) bool, error) {
	places := substrateObjects(cfg, home)
	others, err := otherIdentitiesPlaces(cfg, ".")
	if err != nil {
		return nil, err
	}
	machinery, err := hostMachinery(cfg)
	if err != nil {
		return nil, err
	}
	places = append(append(places, others...), machinery...)
	places = append(places, &firewall.Rule{Path: realPath(home)})

	for i, r := range places {
		places[i] = r.Resolved()
	}
	return func(path string) bool { return firewall.AnyCoversResolved(places, path) }, nil
}

const (
	birthNotCopiedThere = "At this identity's birth, the notes other AI agents kept on this machine were not copied: %s was already there, and is left as it is."

	birthNotCopied  = "At this identity's birth, the notes other AI agents kept on this machine were not copied: %v."
	birthUnfinished = "At this identity's birth, the copy of the notes other AI agents kept on this machine did not finish: %v. %s"

	pressSafe = "the identity is in SAFE mode (%s), so nothing is written into their home; nothing was copied"
	pressOff  = "the copy is switched off in config.json (genesis.agent_memory.off); nothing was copied"

	pressThere = "%s is already there, and is left as it is; nothing was copied. To copy again, delete that folder first"

	pressNotCopied  = "nothing was copied: %v"
	pressUnfinished = "the copy did not finish: %v. %s"

	pressNoStore = "No store of other AI agents' notes is on this machine; nothing was copied."

	pressNoneFound = "No notes were found in the stores looked in; nothing was copied. %s."

	keptUnnamed = "the places the host keeps could not be named, so no store was read: %w"
)

func (a *App) tellOperatorWhatWasCopied(c *agentMemoryCopy) {
	if c == nil || a.store == nil {
		return
	}
	said := c.report.Told()
	switch {
	case errors.Is(c.err, agentmemory.ErrExists):
		said = fmt.Sprintf(birthNotCopiedThere, c.dst)
	case c.err != nil && c.report.Files() == 0:
		said = fmt.Sprintf(birthNotCopied, c.err)
	case c.err != nil:
		said = fmt.Sprintf(birthUnfinished, c.err, said)
	}
	if said == "" {
		return
	}
	a.noticeOperator(agentMemoryNotice, said)
}

func (a *App) noticeOperator(id, said string) {
	if a.store == nil {
		return
	}
	if _, err := a.store.AddOutboxNoticeOnce(id, "operator", "", said, nil); err != nil {
		logsink.Warn("seed.error", "other agents' notes: the operator's notice could not be written: %v", err)
	}
}

func (a *App) copyAgentNotesAsked(ctx context.Context) (string, error) {

	beforeNotesLock()
	a.birthMu.Lock()
	defer a.birthMu.Unlock()
	if reason, safe := a.SafeMode(); safe {
		return "", &dashboard.AgentNotesRefusal{Code: dashboard.AgentNotesSafe, Reason: fmt.Sprintf(pressSafe, reason)}
	}
	cfg := a.configSnapshot()

	home, _ := a.toolReg.Roots()
	if am := cfg.Genesis.AgentMemory; am != nil && am.Off {
		return "", &dashboard.AgentNotesRefusal{Code: dashboard.AgentNotesOff, Reason: pressOff}
	}
	c := copyAgentNotes(ctx, cfg, home, agentmemory.CopyAsked)
	switch {
	case errors.Is(c.err, agentmemory.ErrExists) && c.report.Files() == 0:
		return "", &dashboard.AgentNotesRefusal{Code: dashboard.AgentNotesExists, Reason: fmt.Sprintf(pressThere, c.dst)}
	case c.err != nil && c.report.Files() == 0:
		return "", &dashboard.AgentNotesRefusal{Code: dashboard.AgentNotesFailed, Reason: fmt.Sprintf(pressNotCopied, c.err)}
	case c.absent:
		return pressNoStore, nil
	case c.report.Files() == 0 && c.report.Told() != "":
		return c.report.Told(), nil
	case c.report.Files() == 0:
		return fmt.Sprintf(pressNoneFound, c.report.Summary()), nil
	}

	said := c.report.Told()
	if c.err != nil {
		said = fmt.Sprintf(pressUnfinished, c.err, said)
	}
	a.noticeOperator(fmt.Sprintf("%s_%d", agentNotesPressNotice, c.report.When.UnixNano()), said)
	if c.err != nil {
		return "", &dashboard.AgentNotesRefusal{Code: dashboard.AgentNotesFailed, Reason: said}
	}
	return said, nil
}

var beforeNotesLock = func() {}

func agentNotesState(home string) *dashboard.AgentNotesState {
	dir := filepath.Join(home, agentmemory.Folder)
	if _, err := os.Lstat(dir); err != nil {
		return &dashboard.AgentNotesState{}
	}
	return &dashboard.AgentNotesState{Copied: true, Folder: dir}
}
