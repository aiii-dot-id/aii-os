package store

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"sort"
	"strings"
)

type CarryReport struct {
	Ephemeral map[string]int64
	Derived   map[string][2]int64
	Converted map[string]RuntimeConversion
}

func (r CarryReport) String() string {
	var b strings.Builder
	names := make([]string, 0, len(r.Ephemeral))
	for n := range r.Ephemeral {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "carried %d ephemeral tables unchanged:", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, " %s=%d", n, r.Ephemeral[n])
	}
	names = names[:0]
	for n := range r.Derived {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "\nrebuilt %d derived tables from the record:", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, " %s=%d→%d", n, r.Derived[n][0], r.Derived[n][1])
	}
	for _, name := range sortedKeys(r.Converted) {
		c := r.Converted[name]
		fmt.Fprintf(&b, "\nconverted %s=%d→%d: %s", name, c.Before, c.After, c.Reason)
	}
	return b.String()
}

func CarryAcross(dbPath, ledgerPath string, verified MirrorHead) (CarryReport, error) {
	var report CarryReport
	st, err := openSchema(context.Background(), dbPath, &verified, func(yield func(*ledger.Event) error) error {
		return ledger.Stream(ledgerPath, yield)
	}, &report, false)
	if err != nil {
		return CarryReport{}, err
	}
	if err := st.Close(); err != nil {
		return report, &SchemaError{Phase: "close after committed carry", Cause: err}
	}
	return report, nil
}

func (s *Store) catalogCounts() (map[string]int64, error) {
	tables, err := s.liveObjects("table")
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for name := range Catalog {
		if _, exists := tables[name]; !exists {
			continue
		}
		var count int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	for _, name := range historicalCommunicationTables() {
		if _, exists := tables[name]; !exists {
			continue
		}
		var count int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	return out, nil
}

func (s *Store) checkCarry(before map[string]int64, report *CarryReport) error {
	after, err := s.catalogCounts()
	if err != nil {
		return err
	}
	*report = CarryReport{Ephemeral: map[string]int64{}, Derived: map[string][2]int64{}, Converted: map[string]RuntimeConversion{}}
	for name, e := range Catalog {
		switch e.Provenance {
		case Derived:
			report.Derived[name] = [2]int64{before[name], after[name]}
		case Ephemeral:
			if change, ok := s.schemaConversions[name]; ok {
				if change.Before != before[name] || change.After != after[name] {
					return fmt.Errorf("conversion verification changed during replay: %s", name)
				}
				report.Converted[name] = change
			} else {
				if before[name] != after[name] {
					return fmt.Errorf("runtime rows changed without a verified conversion: %s %d -> %d", name, before[name], after[name])
				}
				report.Ephemeral[name] = after[name]
			}
		}
	}
	for _, name := range historicalCommunicationTables() {
		if change, ok := s.schemaConversions[name]; ok {
			if change.Before != before[name] || change.After != after[name] {
				return fmt.Errorf("historical communication conversion changed: %s", name)
			}
			report.Converted[name] = change
		}
	}
	return nil
}
