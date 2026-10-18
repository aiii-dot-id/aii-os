package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

const (
	standingEdgesForSQL = `SELECT id, from_id, to_id, edge_type, created_seq
		 FROM edges WHERE (from_id = ? OR to_id = ?) AND archived = 0
		 ORDER BY created_seq, id`

	standingIncomingEdgesSQL = `SELECT id, from_id, to_id, edge_type, created_seq
		 FROM edges WHERE to_id = ? AND archived = 0
		 ORDER BY created_seq, id`
	standingBeliefSQL = `SELECT id, statement, ring, COALESCE(node_type, ''), confidence, evidence_count, confirmed_at_ticks, first_seq, last_seq
		 FROM beliefs WHERE id = ?`
	standingRetiredSQL    = `SELECT archived, CASE WHEN superseded_by IS NOT NULL THEN 1 ELSE 0 END FROM beliefs WHERE id = ?`
	standingProvenanceSQL = `SELECT provenance FROM experiences WHERE id = ?`
	standingLifetimeSQL   = `SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`
	standingHeadSQL       = `SELECT COALESCE(MAX(seq), 0) FROM ledger`
)

type anchorsAtStart struct {
	clock int64
	ahead map[string]struct{}
}

func readAnchorsAtStart(q standingReader) (*anchorsAtStart, error) {
	found := &anchorsAtStart{ahead: map[string]struct{}{}}
	if err := q.QueryRow(standingLifetimeSQL).Scan(&found.clock); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := q.Query(`SELECT id FROM beliefs WHERE confirmed_at_ticks > ?`, found.clock)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		found.ahead[id] = struct{}{}
	}
	return found, rows.Err()
}

func (s *Store) takeAnchorsAtStart(q standingReader) {
	if s.anchorsAtStart.Load() != nil {
		return
	}
	if found, err := readAnchorsAtStart(q); err == nil {
		s.anchorsAtStart.CompareAndSwap(nil, found)
	}
}

func (s *Store) anchorsFoundAtStart(q standingReader) (*anchorsAtStart, error) {
	if found := s.anchorsAtStart.Load(); found != nil {
		return found, nil
	}
	found, err := readAnchorsAtStart(q)
	if err != nil {
		return nil, err
	}
	s.anchorsAtStart.CompareAndSwap(nil, found)
	return s.anchorsAtStart.Load(), nil
}

type StandingCursor struct {
	Seq    uint64
	EdgeID string
}

const StandingDetailMax = 10

type standingReader interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type standingSnapshot interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type boundReader struct {
	ctx context.Context
	s   standingSnapshot
}

func (b boundReader) Query(q string, args ...any) (*sql.Rows, error) {
	return b.s.QueryContext(b.ctx, q, args...)
}

func (b boundReader) QueryRow(q string, args ...any) *sql.Row {
	return b.s.QueryRowContext(b.ctx, q, args...)
}

func (s *Store) StandingFor(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, err := s.standingReportRead(s.db, id, nil, false)
	if err != nil {
		return "", err
	}
	return r.Standing, nil
}

func (s *Store) StandingReportFor(id string) (*StandingReport, error) {
	return s.StandingReportForContext(context.Background(), id)
}

func (s *Store) StandingReportForContext(ctx context.Context, id string) (*StandingReport, error) {
	return s.StandingDetail(ctx, id, nil)
}

func (s *Store) StandingDetail(ctx context.Context, id string, cur *StandingCursor) (*StandingReport, error) {
	tx, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("standing of %s: read snapshot: %w", id, err)
	}
	r, err := s.standingReportRead(boundReader{ctx: ctx, s: tx}, id, cur, true)
	if err != nil {
		return nil, errors.Join(err, tx.Close())
	}
	return r, tx.Close()
}

func (s *Store) standingReportRead(q standingReader, id string, cur *StandingCursor, every bool) (*StandingReport, error) {
	query, args := standingIncomingEdgesSQL, []any{id}
	if every {
		query, args = standingEdgesForSQL, []any{id, id}
	}
	edges, err := edgesRead(q, query, args...)
	if err != nil {
		return nil, fmt.Errorf("standing of %s: edges: %w", id, err)
	}
	var head uint64
	if err := q.QueryRow(standingHeadSQL).Scan(&head); err != nil {
		return nil, fmt.Errorf("standing of %s: ledger head: %w", id, err)
	}
	r := &StandingReport{Standing: "new", ReadHeadSeq: head}
	b, err := beliefRead(q, id)
	switch {
	case err == nil:
		r.Belief = *b
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("standing of %s: belief row: %w", id, err)
	}
	var archived, superseded sql.NullInt64
	if err := q.QueryRow(standingRetiredSQL, id).Scan(&archived, &superseded); err == nil {
		r.Retired = archived.Int64 == 1 || superseded.Int64 == 1
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("standing of %s: retired state: %w", id, err)
	}

	type sourceInfo struct{ kind, provenance, note string }
	resolved := map[string]sourceInfo{}
	sources := map[string]bool{}
	type classified struct {
		edge ReportEdge
		qual bool
	}

	var window []classified
	for _, e := range edges {
		var re ReportEdge
		qual := false
		switch {
		case e.ToID != id || e.FromID == id:
			re = ReportEdge{Edge: e, Reason: "outending direction"}
		case e.EdgeType == "CONTRADICTS":
			r.Contradicted = true
			re = ReportEdge{Edge: e, Reason: "contradiction"}
		case e.EdgeType == "SUPPORTS" || e.EdgeType == "REINFORCED_BY" || e.EdgeType == "DERIVED_FROM":
			info, known := resolved[e.FromID]
			if !known {
				src, err := evidenceSourceRead(q, e.FromID)
				if err != nil && !errors.Is(err, ErrNoEvidenceSource) {
					return nil, fmt.Errorf("standing of %s: cannot verify evidence %s exists: %w", id, e.FromID, err)
				}
				if err == nil {
					prov, err := provenanceRead(q, e.FromID)
					if err != nil {
						return nil, fmt.Errorf("standing of %s: provenance of %s: %w", id, e.FromID, err)
					}
					info = sourceInfo{kind: src.kind, provenance: prov, note: src.note()}
				}
				resolved[e.FromID] = info
			}
			if info.kind == "" {
				re = ReportEdge{Edge: e, Reason: "ghost source"}
			} else {
				qual = true
				re = ReportEdge{Edge: e, SourceKind: info.kind, Provenance: info.provenance, Note: info.note}
				sources[e.FromID] = true
			}
		default:
			re = ReportEdge{Edge: e, Reason: "edge type not counted"}
		}
		if qual {
			r.QualifyingTotal++
		} else {
			r.ExcludedTotal++
		}

		if cur != nil && len(window) < StandingDetailMax+1 &&
			(e.CreatedSeq > cur.Seq || (e.CreatedSeq == cur.Seq && e.ID > cur.EdgeID)) {
			window = append(window, classified{edge: re, qual: qual})
		}
	}

	r.DistinctSources = len(sources)

	if r.DistinctSources >= 3 {
		origins, unopened, err := originsOf(q, id, sources)
		if err != nil {
			return nil, fmt.Errorf("standing of %s: origins: %w", id, err)
		}
		r.DistinctOrigins, r.OriginsUnopened = origins, unopened
	}
	if r.DistinctSources >= 3 && r.DistinctOrigins >= 3 {
		var ticks int64
		if err := q.QueryRow(standingLifetimeSQL).Scan(&ticks); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("standing of %s: lifetime ticks: %w", id, err)
		}
		r.Standing, r.Eligible = "confirmed", true
		start, err := s.anchorsFoundAtStart(q)
		if err != nil {
			return nil, fmt.Errorf("standing of %s: the lived clock at this start: %w", id, err)
		}

		anchor := r.Belief.ConfirmedAtTicks
		if _, ahead := start.ahead[id]; ahead {
			anchor = start.clock
		}
		if r.Belief.ConfirmedAtTicks > 0 && ticks-anchor >= 50 {
			r.Standing = "trusted"
		}
	}
	if r.Contradicted {
		r.Standing, r.Eligible = "suspect", false
	}

	more := len(window) > StandingDetailMax
	if more {
		window = window[:StandingDetailMax]
	}
	for _, item := range window {
		if item.qual {
			r.Qualifying = append(r.Qualifying, item.edge)
		} else {
			r.Excluded = append(r.Excluded, item.edge)
		}
	}
	if more {
		last := window[len(window)-1].edge.Edge
		r.NextDetailSeq, r.NextDetailEdgeID = last.CreatedSeq, last.ID
	}
	return r, nil
}

func edgesForBeliefRead(q standingReader, beliefID string) ([]Edge, error) {
	return edgesRead(q, standingEdgesForSQL, beliefID, beliefID)
}

func edgesRead(q standingReader, query string, args ...any) ([]Edge, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.FromID, &e.ToID, &e.EdgeType, &e.CreatedSeq); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func beliefRead(q standingReader, id string) (*Belief, error) {
	var b Belief
	err := q.QueryRow(standingBeliefSQL, id).Scan(&b.ID, &b.Statement, &b.Ring, &b.NodeType, &b.Confidence, &b.EvidenceCount, &b.ConfirmedAtTicks, &b.FirstSeq, &b.LastSeq)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

const (
	standingExperienceOriginSQL = `SELECT provenance, created_seq FROM experiences WHERE id = ?`

	standingDreamRunAfterSQL = `SELECT payload FROM ledger WHERE seq > ? AND seq <= ? AND type = 'dream.run' ORDER BY seq LIMIT 1`
	standingDreamRunWindow   = 4096

	standingOriginBound = 512
)

func originsOf(q standingReader, claim string, sources map[string]bool) (origins int, unopened int, err error) {
	found := map[string]bool{}
	seen := map[string]bool{claim: true}
	turnedAway := map[string]bool{}
	queue := make([]string, 0, len(sources))
	for id := range sources {
		queue = append(queue, id)
	}
	sort.Strings(queue)
	opened := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		if opened >= standingOriginBound {
			turnedAway[id] = true
			continue
		}
		seen[id] = true
		opened++
		rests, origin, err := openSource(q, id)
		if err != nil {
			return 0, 0, err
		}
		if origin {
			found[id] = true
		}
		queue = append(queue, rests...)
	}
	return len(found), len(turnedAway), nil
}

func openSource(q standingReader, id string) (rests []string, origin bool, err error) {
	kind, err := evidenceOriginKindRead(q, id)
	if errors.Is(err, ErrNoEvidenceSource) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if kind == "experience" {
		var provenance string
		var created int64
		if err := q.QueryRow(standingExperienceOriginSQL, id).Scan(&provenance, &created); err != nil {
			return nil, false, fmt.Errorf("origin of %s: %w", id, err)
		}
		if provenance == "dream" {
			inputs, err := dreamInputsOf(q, created)
			if err != nil {
				return nil, false, fmt.Errorf("origin of %s: %w", id, err)
			}
			if len(inputs) > 0 {
				return inputs, false, nil
			}
		}
		return nil, true, nil
	}
	edges, err := edgesRead(q, standingIncomingEdgesSQL, id)
	if err != nil {
		return nil, false, fmt.Errorf("origin of %s: edges: %w", id, err)
	}
	for _, e := range edges {
		if e.FromID == id || (e.EdgeType != "SUPPORTS" && e.EdgeType != "REINFORCED_BY" && e.EdgeType != "DERIVED_FROM") {
			continue
		}
		if _, err := evidenceOriginKindRead(q, e.FromID); errors.Is(err, ErrNoEvidenceSource) {
			continue
		} else if err != nil {
			return nil, false, err
		}
		rests = append(rests, e.FromID)
	}
	return rests, len(rests) == 0, nil
}

func dreamInputsOf(q standingReader, seq int64) ([]string, error) {
	var payload string
	err := q.QueryRow(standingDreamRunAfterSQL, seq, seq+standingDreamRunWindow).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p FacilityRunPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return nil, fmt.Errorf("the dream's run marker after record %d: %w", seq, err)
	}
	for _, out := range p.Outputs {
		if out == uint64(seq) {
			return p.Inputs, nil
		}
	}
	return nil, nil
}

func evidenceOriginKindRead(q standingReader, id string) (string, error) {
	src, err := evidenceSourceRead(q, id)
	return src.kind, err
}

type evidenceSource struct {
	kind        string
	alsoKind    string
	seq, alsoAt int64
}

func (s evidenceSource) note() string {
	if s.alsoKind == "" {
		return ""
	}
	return fmt.Sprintf("this id also names %s recorded later (record %d); it is read as the %s recorded first (record %d)", article(s.alsoKind), s.alsoAt, s.kind, s.seq)
}

func article(kind string) string {
	if kind == "experience" {
		return "an experience"
	}
	return "a " + kind
}

func evidenceSourceRead(q standingReader, id string) (evidenceSource, error) {
	var found []evidenceSource
	for _, candidate := range []struct{ kind, query string }{
		{"belief", "SELECT first_seq FROM beliefs WHERE id = ?"},
		{"experience", "SELECT created_seq FROM experiences WHERE id = ?"},
	} {
		var seq int64
		err := q.QueryRow(candidate.query, id).Scan(&seq)
		if err == nil {
			found = append(found, evidenceSource{kind: candidate.kind, seq: seq})
		} else if !errors.Is(err, sql.ErrNoRows) {
			return evidenceSource{}, fmt.Errorf("resolve evidence source %q: %w", id, err)
		}
	}
	switch len(found) {
	case 0:
		return evidenceSource{}, fmt.Errorf("evidence source %q: %w", id, ErrNoEvidenceSource)
	case 1:
		return found[0], nil
	}

	for i := range found {
		if found[i].kind != "experience" {
			continue
		}
		var first sql.NullInt64
		if err := q.QueryRow(`SELECT MIN(seq) FROM ledger WHERE seq <= ? AND type = ? AND json_extract(payload, '$.id') = ?`, found[i].seq, string(ledger.EventExperienceCreate), id).Scan(&first); err != nil {
			return evidenceSource{}, fmt.Errorf("resolve evidence source %q: the experience's first record: %w", id, err)
		}
		if first.Valid {
			found[i].seq = first.Int64
		}
	}
	first, later := found[0], found[1]
	if later.seq < first.seq {
		first, later = later, first
	}
	first.alsoKind, first.alsoAt = later.kind, later.seq
	return first, nil
}

func provenanceRead(q standingReader, id string) (string, error) {
	var prov string
	err := q.QueryRow(standingProvenanceSQL, id).Scan(&prov)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return prov, nil
}

type StandingReport struct {
	Belief   Belief
	Retired  bool
	Standing string

	Eligible bool

	Qualifying []ReportEdge

	QualifyingTotal int

	Excluded []ReportEdge

	ExcludedTotal int

	NextDetailSeq    uint64
	NextDetailEdgeID string

	DistinctSources int

	DistinctOrigins int

	OriginsUnopened int

	Contradicted bool

	ReadHeadSeq uint64
}

type ReportEdge struct {
	Edge       Edge
	SourceKind string
	Provenance string

	Note string

	Reason string
}

func (s *Store) TensionsView() ([]TensionPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, from_id, to_id FROM edges
		 WHERE edge_type = 'CONTRADICTS' AND archived = 0
		 ORDER BY created_seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TensionPair
	for rows.Next() {
		var tp TensionPair
		if err := rows.Scan(&tp.EdgeID, &tp.LeftID, &tp.RightID); err != nil {
			return nil, err
		}
		out = append(out, tp)
	}
	return out, rows.Err()
}

func (s *Store) TensionEnds(ids []string) (map[string]TensionEnd, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]TensionEnd, len(ids))
	for _, id := range ids {
		if _, done := out[id]; done {
			continue
		}
		end, isBelief, err := s.beliefEndLocked(id)
		if err != nil {
			return nil, err
		}
		if isBelief {
			out[id] = end
			continue
		}

		var unmetabolized int
		var provenance, content string
		var cited sql.NullString
		err = s.db.QueryRow(
			`SELECT `+s.experienceMark()+`, provenance, content, `+s.citedOperatorWords("experiences")+` FROM experiences WHERE id = ?`, id,
		).Scan(&unmetabolized, &provenance, &content, &cited)
		switch {
		case err == nil:
			end.Kind, end.Provenance, end.Unmetabolized, end.Text = "experience", provenance, unmetabolized == 1, content
			end.OperatorVerbatim = cited.Valid && quotesOperator(cited.String, content)
		case !errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("tension end %s: %w", id, err)
		}
		out[id] = end
	}
	return out, nil
}

func (s *Store) beliefEndLocked(id string) (end TensionEnd, isBelief bool, err error) {
	var stmt string
	var archived, superseded int
	err = s.db.QueryRow(
		`SELECT statement, archived, superseded_by IS NOT NULL FROM beliefs WHERE id = ?`, id,
	).Scan(&stmt, &archived, &superseded)
	switch {
	case err == nil:
		return TensionEnd{ID: id, Kind: "belief", Text: stmt, Retired: archived == 1 || superseded == 1}, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return TensionEnd{ID: id}, false, nil
	}
	return TensionEnd{}, false, fmt.Errorf("tension end %s: %w", id, err)
}
