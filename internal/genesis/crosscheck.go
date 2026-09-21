package genesis

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

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
// .
// .
// .
// .
type Beside interface {
	ledger.HeadVerifier
	Visit(*ledger.Event) error
	Held() error
	Summary() string
}

// .
// .
// .
type BesideLoader func(ledgerDir string) (Beside, error)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func VerifyHeld(ledgerPath string, beside Beside, visitFor func(fingerprint string) func(*ledger.Event) error) (int, string, error) {
	n, fp, err := VerifySelfContainedVisiting(ledgerPath, beside, func(fingerprint string) func(*ledger.Event) error {
		var also func(*ledger.Event) error
		if visitFor != nil {
			also = visitFor(fingerprint)
		}
		return func(evt *ledger.Event) error {
			if err := beside.Visit(evt); err != nil {
				return err
			}
			if also != nil {
				return also(evt)
			}
			return nil
		}
	})
	if err != nil {
		return n, fp, err
	}
	if err := beside.Held(); err != nil {
		return n, fp, err
	}
	return n, fp, nil
}

// .
// .
const (
	ExitVerified   = 0
	ExitRefused    = 1
	ExitUsage      = 2
	ExitIncomplete = 3
)

// .
// .
// .
// .
// .
func VerifyCommand(ledgerPath string, cross []string, load BesideLoader) (stdout, stderr string, code int) {
	beside, err := load(filepath.Dir(ledgerPath))
	if err != nil {
		return "", RefusedReport(err, ""), ExitRefused
	}
	// .
	// .
	var own *citing
	var visitFor func(string) func(*ledger.Event) error
	if len(cross) > 0 {
		own = &citing{}
		visitFor = func(string) func(*ledger.Event) error { return own.collect }
	}
	n, fp, err := VerifyHeld(ledgerPath, beside, visitFor)
	if err != nil {
		return "", RefusedReport(err, beside.Summary()), ExitRefused
	}
	stdout = VerifiedReport(n, fp, beside.Summary())
	if len(cross) == 0 {
		return stdout, "", ExitVerified
	}
	report, err := crossCheck(ledgerPath, fp, own, cross, load)
	if err != nil {
		return stdout, "CROSS-CHECK REFUSED: " + err.Error() + "\n", ExitRefused
	}
	return stdout + report.String(), "", report.code()
}

// .
type crossReport struct {
	matched    int
	mismatched []string
	notChecked map[string]int
	// .
	// .
	// .
	// .
	unread []string
	// .
	consulted int
}

func (r crossReport) code() int {
	switch {
	case len(r.mismatched) > 0:
		return ExitRefused
	case len(r.notChecked) > 0 || len(r.unread) > 0:
		return ExitIncomplete
	default:
		return ExitVerified
	}
}

func (r crossReport) String() string {
	var b strings.Builder
	switch {
	case len(r.mismatched) > 0:
		fmt.Fprintf(&b, "CROSS-CHECK FAILED: %d citation(s) do not name the record they say they name; %d matched\n", len(r.mismatched), r.matched)
	case len(r.notChecked) > 0 || len(r.unread) > 0:
		fmt.Fprintf(&b, "CROSS-CHECK INCOMPLETE: %d citation(s) matched and none mismatched, but some could not be checked — this is not a full success\n", r.matched)
	default:
		fmt.Fprintf(&b, "CROSS-CHECKED: all %d citation(s) name the records they say they name, looked up in %d ledger(s). A match proves which record was cited — not its truth or its author's agreement.\n", r.matched, r.consulted)
	}
	for _, line := range r.mismatched {
		b.WriteString("  mismatch: " + line + "\n")
	}
	ids := make([]string, 0, len(r.notChecked))
	for id := range r.notChecked {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "  not checked: %d citation(s) of identity %s — no ledger of it was supplied (-cross)\n", r.notChecked[id], id)
	}
	for _, line := range r.unread {
		b.WriteString("  not checked: " + line + "\n")
	}
	return b.String()
}

type citedRecord struct {
	by uint64
	c  ledger.Citation
}

// .
// .
// .
type citing struct {
	cited    []citedRecord
	unread   []string
	lastSeq  uint64
	lastHash string
}

// .
// .
// .
// .
// .
// .
// .
func (c *citing) collect(evt *ledger.Event) error {
	c.lastSeq, c.lastHash = evt.Seq, evt.EntryHash()
	var probe struct {
		Cites *json.RawMessage `json:"cites"`
	}
	if json.Unmarshal(evt.Payload, &probe) != nil || probe.Cites == nil {
		return nil
	}
	if !ledger.CitesAllowed(evt.Type) {
		c.unread = append(c.unread, fmt.Sprintf("record %d (%s) carries a cites member, and this reader knows no citations on that type", evt.Seq, evt.Type))
		return nil
	}
	cites, err := ledger.ParseCitations(evt.Payload)
	if err != nil {
		c.unread = append(c.unread, fmt.Sprintf("record %d (%s): %v", evt.Seq, evt.Type, err))
		return nil
	}
	for _, one := range cites {
		c.cited = append(c.cited, citedRecord{by: evt.Seq, c: one})
	}
	return nil
}

// .
// .
// .
// .
// .
// .
type matching struct {
	fp         string
	wanted     map[uint64][]citedRecord
	found      map[uint64]bool
	matched    int
	mismatched []string
}

func (m *matching) visit(evt *ledger.Event) error {
	for _, cr := range m.wanted[evt.Seq] {
		m.found[evt.Seq] = true
		if evt.EntryHash() != cr.c.EntryHash {
			m.mismatched = append(m.mismatched, fmt.Sprintf("record %d cites record %d of %s as %s; that record is %s", cr.by, evt.Seq, shortFP(m.fp), shortFP(cr.c.EntryHash), shortFP(evt.EntryHash())))
		} else {
			m.matched++
		}
	}
	return nil
}

// .
// .
func (m *matching) into(report *crossReport) {
	report.matched += m.matched
	report.mismatched = append(report.mismatched, m.mismatched...)
	for seq, crs := range m.wanted {
		if !m.found[seq] {
			for _, cr := range crs {
				report.mismatched = append(report.mismatched, fmt.Sprintf("record %d cites record %d of %s, and that ledger holds no such record", cr.by, seq, shortFP(m.fp)))
			}
		}
	}
	report.consulted++
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
func crossCheck(ownPath, ownFP string, own *citing, cross []string, load BesideLoader) (crossReport, error) {
	report := crossReport{notChecked: map[string]int{}, unread: own.unread}
	wanted := map[string]map[uint64][]citedRecord{}
	for _, cr := range own.cited {
		if wanted[cr.c.Identity] == nil {
			wanted[cr.c.Identity] = map[uint64][]citedRecord{}
		}
		wanted[cr.c.Identity][cr.c.Seq] = append(wanted[cr.c.Identity][cr.c.Seq], cr)
	}

	// .
	// .
	// .
	// .
	sources := map[string]string{ownFP: ownPath}
	for _, path := range cross {
		beside, err := load(filepath.Dir(path))
		if err != nil {
			return crossReport{}, fmt.Errorf("%s: %w", path, err)
		}
		var m *matching
		_, fp, err := VerifyHeld(path, beside, func(fp string) func(*ledger.Event) error {
			if len(wanted[fp]) == 0 || fp == ownFP {
				return nil
			}
			m = &matching{fp: fp, wanted: wanted[fp], found: map[uint64]bool{}}
			return m.visit
		})
		if err != nil {
			return crossReport{}, fmt.Errorf("%s does not verify, so it is evidence of nothing: %w", path, err)
		}
		if prior, dup := sources[fp]; dup {
			return crossReport{}, fmt.Errorf("%s and %s are both ledgers of identity %s — one source per identity; which is right is not this command's to guess", prior, path, fp)
		}
		sources[fp] = path
		if m != nil {
			m.into(&report)
		}
	}

	// .
	// .
	// .
	// .
	// .
	if len(wanted[ownFP]) > 0 {
		beside, err := load(filepath.Dir(ownPath))
		if err != nil {
			return crossReport{}, fmt.Errorf("%s: %w", ownPath, err)
		}
		m := &matching{fp: ownFP, wanted: wanted[ownFP], found: map[uint64]bool{}}
		sameChain := false
		_, fp, err := VerifyHeld(ownPath, beside, func(string) func(*ledger.Event) error {
			return func(evt *ledger.Event) error {
				if evt.Seq == own.lastSeq {
					if evt.EntryHash() != own.lastHash {
						return fmt.Errorf("%s changed while it was being checked: record %d is not the record that was verified", ownPath, evt.Seq)
					}
					sameChain = true
				}
				return m.visit(evt)
			}
		})
		if err != nil {
			return crossReport{}, fmt.Errorf("%s: its self-citations could not be checked: %w", ownPath, err)
		}
		if fp != ownFP || !sameChain {
			return crossReport{}, fmt.Errorf("%s changed while it was being checked: it no longer holds the chain that was verified", ownPath)
		}
		m.into(&report)
	}

	for id, bySeq := range wanted {
		if _, ok := sources[id]; ok {
			continue
		}
		for _, crs := range bySeq {
			report.notChecked[id] += len(crs)
		}
	}
	sort.Strings(report.mismatched)
	return report, nil
}

func shortFP(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
