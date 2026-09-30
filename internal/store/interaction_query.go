package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
)

func (s *Store) InteractionIncarnation() string {
	s.interactionOnce.Do(func() { s.interactionInstance = uuid.NewString() })
	return s.interactionInstance
}

const interactionSelect = `c.id,c.session_id,c.role,c.content,c.turn_seq,c.created_at,c.project_id,c.kind,c.turn_id,COALESCE(c.related_id,''),COALESCE(c.source_kind,''),COALESCE(c.source_id,''),COALESCE(c.recorded_at,''),COALESCE(c.occurred_at,''),c.outcome,c.details`
const interactionAudience = `(c.source_kind IS NULL OR c.source_kind!='outbox' OR NOT EXISTS(SELECT 1 FROM outbox o WHERE o.id=c.source_id) OR EXISTS(SELECT 1 FROM outbox o WHERE o.id=c.source_id AND o.to_role='operator'))`

func scanInteraction(row interface{ Scan(...any) error }) (interaction.Record, error) {
	var r interaction.Record
	var sk, si, details string
	err := row.Scan(&r.ID, &r.SessionID, &r.Role, &r.Content, &r.Sequence, &r.CreatedAt, &r.ProjectID, &r.Kind, &r.TurnID, &r.RelatedID, &sk, &si, &r.RecordedAt, &r.OccurredAt, &r.Outcome, &details)
	if err != nil {
		return r, err
	}
	for _, text := range []string{r.ID, r.SessionID, r.ProjectID, r.TurnID, r.RelatedID, r.CreatedAt, r.RecordedAt, r.OccurredAt, sk, si} {
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return r, &interaction.Error{Code: "INTERACTION_CONTENT_UNAVAILABLE", Detail: "historical reference/date is outside the text domain; inspect the original read-only database"}
		}
	}
	if !utf8.ValidString(r.Content) {
		return r, &interaction.Error{Code: "INTERACTION_CONTENT_UNAVAILABLE", Detail: "historical content has invalid UTF-8; original bytes remain in the read-only database"}
	}
	if sk != "" {
		r.Source = &interaction.Source{Kind: sk, ID: si}
	}
	if err = interaction.Strict([]byte(details), &r.Details); err != nil {
		return r, err
	}
	return r, nil
}
func (s *Store) enrichInteraction(ctx context.Context, tx *readSnapshot, r *interaction.Record) error {
	if r.Source != nil && r.Source.Kind == "outbox" {
		var text string
		err := tx.QueryRowContext(ctx, `SELECT content FROM outbox WHERE id=? AND to_role='operator'`, r.Source.ID).Scan(&text)
		if err == sql.ErrNoRows {
			r.Details.Reason = "source body unavailable"
		} else if err != nil {
			return err
		} else {
			r.Content = text
		}
	}
	if !s.historicalInteractions {
		deliveries, err := tx.QueryContext(ctx, `SELECT id,delivered,COALESCE(delivered_via,''),delivered_at,attempts,parked,effect,last_error FROM outbox WHERE interaction_id=? AND to_role='operator' ORDER BY id`, r.ID)
		if err != nil {
			return err
		}
		for deliveries.Next() {
			var d interaction.Delivery
			var at sql.NullString
			if err = deliveries.Scan(&d.ID, &d.Delivered, &d.Via, &at, &d.Attempts, &d.Parked, &d.Effect, &d.Error); err != nil {
				deliveries.Close()
				return err
			}
			d.At = at.String
			r.Deliveries = append(r.Deliveries, d)
		}
		err = deliveries.Err()
		deliveries.Close()
		if err != nil {
			return err
		}
	}
	if s.historicalInteractions && !s.historicalAnnotations {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,payload FROM turn_annotations WHERE turn_seq=? ORDER BY kind`, r.Sequence)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, raw string
		if err = rows.Scan(&kind, &raw); err != nil {
			return err
		}
		if !json.Valid([]byte(raw)) {
			return fmt.Errorf("interaction annotation unavailable: malformed %s", kind)
		}
		if r.Annotations == nil {
			r.Annotations = map[string]json.RawMessage{}
		}
		r.Annotations[kind] = json.RawMessage(raw)
	}
	return rows.Err()
}

func (s *Store) QueryInteractions(q interaction.Query) (out *interaction.Page, retErr error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	if q.Source != "recorded" {
		return nil, interaction.Invalid("store only owns recorded history")
	}
	inc := s.InteractionIncarnation()
	before, err := q.Before(inc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Close(); retErr == nil && err != nil {
			out = nil
			retErr = err
		}
	}()
	where := []string{interactionAudience}
	args := []any{}
	for _, pair := range [][2]string{{"c.role", string(q.Filter.Role)}, {"c.turn_id", q.Filter.TurnID}, {"c.session_id", q.Filter.SessionID}, {"c.project_id", q.Filter.ProjectID}, {"c.related_id", q.Filter.RelatedID}, {"json_extract(c.details,'$.work_session_id')", q.Filter.WorkSession}} {
		if pair[1] != "" {
			where = append(where, pair[0]+" = ?")
			args = append(args, pair[1])
		}
	}
	if len(q.Filter.Kinds) > 0 {
		where = append(where, "c.kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(q.Filter.Kinds)), ",")+")")
		for _, k := range q.Filter.Kinds {
			args = append(args, string(k))
		}
	}
	if q.Filter.From != "" {
		where = append(where, "c.recorded_at >= ?")
		args = append(args, q.Filter.From)
	}
	if q.Filter.Until != "" {
		where = append(where, "c.recorded_at < ?")
		args = append(args, q.Filter.Until)
	}
	if q.Filter.Undated {
		where = append(where, "c.recorded_at IS NULL")
	}

	baseWhere := strings.Join(where, " AND ")
	baseArgs := append([]any(nil), args...)
	low, high, err := q.WindowBounds(inc, func(id string) (uint64, error) {
		var seq uint64
		a := append(append([]any(nil), baseArgs...), id)
		err := tx.QueryRowContext(ctx, "SELECT c.turn_seq FROM "+s.interactionTable()+" c WHERE "+baseWhere+" AND c.id = ?", a...).Scan(&seq)
		if err == sql.ErrNoRows {
			return 0, &interaction.Error{Code: "INTERACTION_ANCHOR_UNAVAILABLE", Detail: "history anchor is no longer available"}
		}
		return seq, err
	})
	if err != nil {
		return nil, err
	}
	if q.Window != nil {
		where = append(where, "c.turn_seq >= ?", "c.turn_seq <= ?")
		args = append(args, low, high)
	}
	if before != nil {
		where = append(where, "c.turn_seq < ?")
		args = append(args, *before)
	}
	order := "DESC"
	if q.Ascending() {
		order = "ASC"
	}
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, "SELECT "+interactionSelect+" FROM "+s.interactionTable()+" c WHERE "+strings.Join(where, " AND ")+" ORDER BY c.turn_seq "+order+" LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &interaction.Page{Version: interaction.Version, Identity: "", Incarnation: inc, Source: q.Source, Filter: q.Filter, Cursor: q.Cursor, Rows: []interaction.Record{}, Availability: "complete"}
	if s.historicalInteractions {
		page.Availability = "historical_schema"
	}
	var last uint64
	for rows.Next() {
		if len(page.Rows) == q.Limit {
			page.NextCursor = q.Next(inc, last)
			break
		}
		r, err := scanInteraction(rows)
		if err != nil {
			return nil, err
		}
		if err = s.enrichInteraction(ctx, tx, &r); err != nil {
			return nil, err
		}
		r = interaction.Compact(r, q.Source, inc)
		page.Rows = append(page.Rows, r)
		page.NextCursor = q.Next(inc, r.Sequence)
		raw, err := json.Marshal(page)
		if err != nil {
			return nil, err
		}
		if len(raw) > interaction.MaxPageBytes-8192 {
			page.Rows = page.Rows[:len(page.Rows)-1]
			if len(page.Rows) == 0 {
				return nil, &interaction.Error{Code: "INTERACTION_CONTENT_UNAVAILABLE", Detail: "record metadata exceeds frame budget"}
			}
			page.NextCursor = q.Next(inc, last)
			break
		}
		last = r.Sequence
		page.NextCursor = ""
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if !q.Ascending() {
		for i, j := 0, len(page.Rows)-1; i < j; i, j = i+1, j-1 {
			page.Rows[i], page.Rows[j] = page.Rows[j], page.Rows[i]
		}
	}
	if q.Window != nil {
		page.Window = &interaction.WindowState{}
		if len(page.Rows) > 0 {
			for _, check := range []struct {
				op  string
				seq uint64
				dst *bool
			}{
				{"<", page.Rows[0].Sequence, &page.Window.HasOlder},
				{">", page.Rows[len(page.Rows)-1].Sequence, &page.Window.HasNewer},
			} {
				a := append(append([]any(nil), baseArgs...), check.seq)
				err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.interactionTable()+" c WHERE "+baseWhere+" AND c.turn_seq "+check.op+" ?)", a...).Scan(check.dst)
				if err != nil {
					return nil, err
				}
			}
		}
		if q.Window.Mode != "range" {
			page.NextCursor = ""
		}
	}
	interaction.Seal(page)
	return page, nil
}
func (s *Store) ReadInteraction(req interaction.ReadRequest) (out *interaction.ReadResult, retErr error) {
	if err := interaction.ValidateRead(&req); err != nil {
		return nil, err
	}
	if req.Source != "recorded" {
		return nil, interaction.Invalid("store only owns recorded detail")
	}
	if req.Incarnation != s.InteractionIncarnation() {
		return nil, &interaction.Error{Code: "INTERACTION_SOURCE_CHANGED", Detail: "source retired"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Close(); retErr == nil && err != nil {
			out = nil
			retErr = err
		}
	}()
	r, err := scanInteraction(tx.QueryRowContext(ctx, "SELECT "+interactionSelect+" FROM conversations c WHERE c.id=? AND "+interactionAudience, req.ID))
	if err == sql.ErrNoRows {
		return nil, &interaction.Error{Code: "INTERACTION_CONTENT_UNAVAILABLE", Detail: "record unavailable to this audience"}
	}
	if err != nil {
		return nil, err
	}
	if err = s.enrichInteraction(ctx, tx, &r); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return interaction.DetailRead(req, raw)
}

func (s *Store) interactionTable() string {
	if !s.historicalInteractions {
		return "conversations"
	}
	project := "project_id"
	if !s.historicalProject {
		project = "'' AS project_id"
	}
	return `(SELECT id,session_id,role,content,turn_seq,created_at,` + project + `,
 'legacy' AS kind,'' AS turn_id,NULL AS related_id,NULL AS source_kind,
 NULL AS source_id,NULL AS recorded_at,NULL AS occurred_at,'' AS outcome,
 '{"legacy_source":"conversations","order_basis":"stored turn_seq"}' AS details FROM conversations)`
}
