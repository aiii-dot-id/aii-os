package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

const (
	standingEdgesForSQL = `SELECT id, from_id, to_id, edge_type, created_seq
		 FROM edges WHERE (from_id = ? OR to_id = ?) AND archived = 0
		 ORDER BY created_seq, id`
	standingBeliefSQL = `SELECT id, statement, ring, COALESCE(node_type, ''), confidence, evidence_count, confirmed_at_ticks, first_seq, last_seq
		 FROM beliefs WHERE id = ?`
	standingRetiredSQL    = `SELECT archived, CASE WHEN superseded_by IS NOT NULL THEN 1 ELSE 0 END FROM beliefs WHERE id = ?`
	standingProvenanceSQL = `SELECT provenance FROM experiences WHERE id = ?`
	standingLifetimeSQL   = `SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`
	standingHeadSQL       = `SELECT COALESCE(MAX(seq), 0) FROM ledger`
)

type StandingCursor struct {
	Seq    uint64
	EdgeID string
}

const StandingDetailMax = 10

type standingReader interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

type standingSnapshot interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

type boundReader struct {
	ctx context.Context
	s   standingSnapshot
}

func (b boundReader) Query(q string, args ...interface{}) (*sql.Rows, error) {
	return b.s.QueryContext(b.ctx, q, args...)
}

func (b boundReader) QueryRow(q string, args ...interface{}) *sql.Row {
	return b.s.QueryRowContext(b.ctx, q, args...)
}

func (s *Store) StandingFor(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, err := s.standingReportRead(s.db, id, nil)
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
	r, err := s.standingReportRead(boundReader{ctx: ctx, s: tx}, id, cur)
	if err != nil {
		return nil, errors.Join(err, tx.Close())
	}
	return r, tx.Close()
}

func (s *Store) standingReportRead(q standingReader, id string, cur *StandingCursor) (*StandingReport, error) {
	edges, err := edgesForBeliefRead(q, id)
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

	type sourceInfo struct{ kind, provenance string }
	resolved := map[string]sourceInfo{}
	sources := map[string]bool{}
	classes := map[string]bool{}
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
				kind, err := evidenceOriginKindRead(q, e.FromID)
				if err != nil && !errors.Is(err, ErrNoEvidenceSource) {
					return nil, fmt.Errorf("standing of %s: cannot verify evidence %s exists: %w", id, e.FromID, err)
				}
				if err == nil {
					prov, err := provenanceRead(q, e.FromID)
					if err != nil {
						return nil, fmt.Errorf("standing of %s: provenance of %s: %w", id, e.FromID, err)
					}
					info = sourceInfo{kind: kind, provenance: prov}
				}
				resolved[e.FromID] = info
			}
			if info.kind == "" {
				re = ReportEdge{Edge: e, Reason: "ghost source"}
			} else {
				qual = true
				re = ReportEdge{Edge: e, SourceKind: info.kind, Provenance: info.provenance}
				sources[e.FromID] = true
				classes[authorshipClassOf(info.provenance)] = true
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
		for class := range classes {
			r.AuthorshipClasses = append(r.AuthorshipClasses, class)
		}
		sort.Strings(r.AuthorshipClasses)
		if len(classes) >= 2 {
			var ticks int64
			if err := q.QueryRow(standingLifetimeSQL).Scan(&ticks); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("standing of %s: lifetime ticks: %w", id, err)
			}
			r.Standing, r.Eligible = "confirmed", true
			if r.Belief.ConfirmedAtTicks > 0 && ticks-r.Belief.ConfirmedAtTicks >= 50 {
				r.Standing = "trusted"
			}
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
	rows, err := q.Query(standingEdgesForSQL, beliefID, beliefID)
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

func evidenceOriginKindRead(q standingReader, id string) (string, error) {
	var found string
	for _, candidate := range []struct{ kind, table string }{{"belief", "beliefs"}, {"experience", "experiences"}} {
		var one int
		err := q.QueryRow("SELECT 1 FROM "+candidate.table+" WHERE id = ?", id).Scan(&one)
		if err == nil {
			if found != "" {
				return "", fmt.Errorf("evidence source %q is ambiguous across belief and experience", id)
			}
			found = candidate.kind
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("resolve evidence source %q: %w", id, err)
		}
	}
	if found == "" {
		return "", fmt.Errorf("evidence source %q: %w", id, ErrNoEvidenceSource)
	}
	return found, nil
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

	AuthorshipClasses []string

	Contradicted bool

	ReadHeadSeq uint64
}

type ReportEdge struct {
	Edge       Edge
	SourceKind string
	Provenance string

	Reason string
}

func authorshipClassOf(provenance string) string {
	switch provenance {
	case "operator", "external":
		return provenance
	default:
		return "resident"
	}
}

type TensionPair struct {
	LeftID, RightID string
	EdgeID          string
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

type TensionEnd struct {
	ID string

	Kind string

	Text string

	Retired bool

	Sealed bool

	Provenance string
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

		var private int
		var provenance string
		var content sql.NullString
		err = s.db.QueryRow(
			`SELECT private, provenance, CASE WHEN private = 0 THEN content END FROM experiences WHERE id = ?`, id,
		).Scan(&private, &provenance, &content)
		switch {
		case err == nil:

			end.Kind, end.Provenance, end.Sealed, end.Text = "experience", provenance, private == 1, content.String
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
