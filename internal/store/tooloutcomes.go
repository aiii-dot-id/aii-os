package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

const ToolOutcomeCursorKey = "consolidate.tool_outcomes_read_through"

const toolOutcomeBoundaryKey = "consolidate.tool_outcomes_boundary"

var ErrToolOutcomeBoundary = errors.New("tool outcome cursor: invalid boundary")

type toolOutcomeBoundary struct {
	Through int64  `json:"through"`
	After   string `json:"after"`
}

const toolGateRunes = 2000

func (s *Store) ToolOutcomeCursor() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.toolCursorLocked()
}

func (s *Store) toolCursorLocked() (int64, error) {
	v, err := s.getRuntimeMeta(ToolOutcomeCursorKey)
	if err != nil {
		return 0, fmt.Errorf("tool outcome cursor: %w", err)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, nil
	}
	return n, nil
}

func (s *Store) toolCursorStateLocked() (was int64, after string, raw [2]string, err error) {
	if raw[0], err = s.getRuntimeMeta(ToolOutcomeCursorKey); err != nil {
		return
	}
	if raw[1], err = s.getRuntimeMeta(toolOutcomeBoundaryKey); err != nil {
		return
	}
	was, err = strconv.ParseInt(raw[0], 10, 64)
	if err != nil || was < 0 {
		was, err = 0, nil
	}
	if raw[1] != "" {
		var boundary toolOutcomeBoundary
		if json.Unmarshal([]byte(raw[1]), &boundary) != nil || boundary.Through < 0 || boundary.After == "" {
			err = ErrToolOutcomeBoundary
			return
		}
		if boundary.Through == was {
			after = boundary.After
		}
	}
	return
}

func (s *Store) PublishToolOutcomeCursor(batch rows.ToolOutcomeBatch) error {
	if !batch.MovesCursor() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw [2]string
	if err := tx.QueryRow(`SELECT
		COALESCE((SELECT value FROM runtime_meta WHERE key=?), ''),
		COALESCE((SELECT value FROM runtime_meta WHERE key=?), '')`,
		ToolOutcomeCursorKey, toolOutcomeBoundaryKey).Scan(&raw[0], &raw[1]); err != nil {
		return err
	}
	if raw != batch.CursorRaw {
		return ErrCursorMoved
	}
	if err := setRuntimeMeta(tx, ToolOutcomeCursorKey, strconv.FormatInt(batch.Through, 10)); err != nil {
		return fmt.Errorf("tool outcome cursor: %w", err)
	}
	if batch.ThroughAfter == "" {
		_, err = tx.Exec(`DELETE FROM runtime_meta WHERE key=?`, toolOutcomeBoundaryKey)
	} else {
		value, marshalErr := json.Marshal(toolOutcomeBoundary{Through: batch.Through, After: batch.ThroughAfter})
		if marshalErr != nil {
			return marshalErr
		}
		err = setRuntimeMeta(tx, toolOutcomeBoundaryKey, string(value))
	}
	if err != nil {
		return fmt.Errorf("tool outcome boundary: %w", err)
	}
	return tx.Commit()
}

func (s *Store) NewestToolObservation() (rows.ToolObservation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newestToolObservationLocked()
}

func (s *Store) newestToolObservationLocked() (rows.ToolObservation, bool, error) {
	upper := strings.TrimSuffix(rows.ToolObservationPrefix, "_") + "`"
	rs, err := s.h().Query(`SELECT id, created_seq FROM experiences WHERE id >= ? AND id < ?`, rows.ToolObservationPrefix, upper)
	if err != nil {
		return rows.ToolObservation{}, false, fmt.Errorf("newest tool observation: %w", err)
	}
	defer rs.Close()
	var best rows.ToolObservation
	var seq int64
	found := false
	for rs.Next() {
		var id string
		var nextSeq int64
		if err := rs.Scan(&id, &nextSeq); err != nil {
			return rows.ToolObservation{}, false, err
		}
		o, ok := rows.ParseToolObservationID(id)
		if !ok {
			return rows.ToolObservation{}, false, ErrToolOutcomeBoundary
		}
		if !found || o.Through > best.Through || o.Through == best.Through &&
			(o.ThroughAfter > best.ThroughAfter || o.ThroughAfter == best.ThroughAfter && nextSeq > seq) {
			best, seq, found = o, nextSeq, true
		}
	}
	return best, found, rs.Err()
}

func (s *Store) ToolOutcomesSince(now time.Time, maxSessions int, firstLook time.Duration) (rows.ToolOutcomeBatch, error) {
	if maxSessions <= 0 {
		return rows.ToolOutcomeBatch{}, fmt.Errorf("tool outcomes: the session bound must be positive")
	}

	nowMs := now.UnixMilli() - 1
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.historicalInteractions {
		return rows.ToolOutcomeBatch{}, errHistoricalWork
	}
	was, after, raw, err := s.toolCursorStateLocked()
	if err != nil {
		return rows.ToolOutcomeBatch{}, err
	}
	last, haveLast, err := s.newestToolObservationLocked()
	if err != nil {
		return rows.ToolOutcomeBatch{}, err
	}
	b := rows.ToolOutcomeBatch{CursorWas: was, CursorAfterWas: after, CursorRaw: raw}

	if was <= nowMs {
		b.From, b.FromAfter = was, after
	}
	if haveLast {
		b.Last = &last
		if last.Through > b.From || last.Through == b.From && last.ThroughAfter > b.FromAfter {
			b.From, b.FromAfter = last.Through, last.ThroughAfter
		}
	}
	if b.From == 0 && b.FromAfter == "" {
		b.From = max(nowMs-firstLook.Milliseconds(), 0)
	}
	if b.From >= nowMs {
		b.Through, b.ThroughAfter = b.From, b.FromAfter
		return b, nil
	}
	sessions, err := s.deliveredSessionsLocked(b.From, nowMs, b.FromAfter, "", maxSessions+1)
	if err != nil {
		return rows.ToolOutcomeBatch{}, err
	}
	b.Through = nowMs
	if len(sessions) > maxSessions {
		last := sessions[maxSessions-1]
		if sessions[maxSessions].DeliveredAt == last.DeliveredAt {
			b.Through, b.ThroughAfter = last.DeliveredAt-1, last.ID
		} else {
			b.Through = last.DeliveredAt
		}
		sessions = sessions[:maxSessions]
	}
	now1, err := s.toolGroupsLocked(b.From, b.Through, sessions)
	if err != nil {
		return rows.ToolOutcomeBatch{}, err
	}
	for i := range sessions {
		if sessions[i].Tools, err = s.usesLocked(now1.bySession[sessions[i].ID]); err != nil {
			return rows.ToolOutcomeBatch{}, err
		}
	}
	if b.Loose, err = s.usesLocked(now1.loose); err != nil {
		return rows.ToolOutcomeBatch{}, err
	}
	b.Sessions, b.Oldest = sessions, now1.oldest
	if haveLast {
		then, err := s.deliveredSessionsLocked(last.From, last.Through, last.FromAfter, last.ThroughAfter, 0)
		if err != nil {
			return rows.ToolOutcomeBatch{}, err
		}
		thenGroups, err := s.toolGroupsLocked(last.From, last.Through, then)
		if err != nil {
			return rows.ToolOutcomeBatch{}, err
		}
		b.Since = toolSince(thenGroups.total(), now1.total())
	}
	return b, nil
}

func (s *Store) deliveredSessionsLocked(from, through int64, fromAfter, throughAfter string, limit int) ([]rows.ToolSession, error) {
	end := through
	if throughAfter != "" {
		end++
	}
	q := `SELECT id, description, COALESCE(result,''), delivered_at, COALESCE(started_at,0) FROM work_sessions
		WHERE status = 'delivered' AND delivered_at > ? AND delivered_at <= ?
		AND (? = '' OR delivered_at != ? OR id > ?)
		AND (? = '' OR delivered_at != ? OR id <= ?) ORDER BY delivered_at, id`
	args := []any{from, end, fromAfter, from + 1, fromAfter, throughAfter, end, throughAfter}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rs, err := s.h().Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("tool outcomes: sessions: %w", err)
	}
	var out []rows.ToolSession
	for rs.Next() {
		var ts rows.ToolSession
		var result string
		if err := rs.Scan(&ts.ID, &ts.Description, &result, &ts.DeliveredAt, &ts.StartedAt); err != nil {
			rs.Close()
			return nil, fmt.Errorf("tool outcomes: sessions: %w", err)
		}
		ts.Description, ts.Outcome = SubagentGoal(ts.Description), outcomeClass(result)
		out = append(out, ts)
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		err := s.h().QueryRow(`SELECT COALESCE(json_extract(details,'$.grade.grade'),'') FROM conversations
			WHERE kind = 'message' AND role = 'operator' AND json_extract(details,'$.grade.session') = ? ORDER BY turn_seq DESC LIMIT 1`, out[i].ID).Scan(&out[i].Grade)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tool outcomes: grade of %s: %w", out[i].ID, err)
		}
	}
	return out, nil
}

type toolKey struct{ tool, outcome, reason string }

type toolTally struct {
	counts  map[toolKey]int
	example map[toolKey]string
	gate    map[toolKey]string

	recovered   map[toolKey]int
	recoveredBy map[toolKey]string
	waiting     map[string][]toolKey
}

func newTally() *toolTally {
	return &toolTally{counts: map[toolKey]int{}, example: map[toolKey]string{}, gate: map[toolKey]string{},
		recovered: map[toolKey]int{}, recoveredBy: map[toolKey]string{}, waiting: map[string][]toolKey{}}
}

type toolGroups struct {
	bySession map[string]*toolTally
	loose     *toolTally
	oldest    int64
}

func (g toolGroups) total() map[toolKey]int {
	out := map[toolKey]int{}
	for _, t := range append([]*toolTally{g.loose}, mapValues(g.bySession)...) {
		for k, n := range t.counts {
			out[k] += n
		}
	}
	return out
}

func mapValues(m map[string]*toolTally) []*toolTally {
	out := make([]*toolTally, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

const toolResultScanSQL = `SELECT recorded_at, outcome, details, COALESCE(related_id,''), turn_id,
		CASE WHEN outcome = 'refused' THEN substr(content, 1, ?) ELSE '' END
	FROM conversations WHERE kind = 'tool_result' AND turn_seq >= ? AND recorded_at >= ? ORDER BY turn_seq`

func (s *Store) toolGroupsLocked(from, through int64, sessions []rows.ToolSession) (toolGroups, error) {
	g := toolGroups{bySession: map[string]*toolTally{}, loose: newTally()}
	floor := from
	for _, ts := range sessions {
		g.bySession[ts.ID] = newTally()
		floor = min(floor, ts.StartedAt)
		if g.oldest == 0 || ts.DeliveredAt < g.oldest {
			g.oldest = ts.DeliveredAt
		}
	}
	fromStamp, err := interaction.NormalizeTime(time.UnixMilli(from))
	if err != nil {
		return g, err
	}
	throughStamp, err := interaction.NormalizeTime(time.UnixMilli(through))
	if err != nil {
		return g, err
	}
	seqFloor, stamp, found, err := windowSince(s.h(), time.UnixMilli(floor))
	if err != nil || !found {
		return g, err
	}
	rs, err := s.h().Query(toolResultScanSQL, toolGateRunes, seqFloor, stamp)
	if err != nil {
		return g, fmt.Errorf("tool outcomes: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var at, outcome, details, call, turn, gate string
		if err := rs.Scan(&at, &outcome, &details, &call, &turn, &gate); err != nil {
			return g, fmt.Errorf("tool outcomes: %w", err)
		}
		var d interaction.Details
		if err := json.Unmarshal([]byte(details), &d); err != nil {
			continue
		}
		if d.LegacySource != "" || d.Actor == "operator" || !reToolDigestName.MatchString(d.Tool) {
			continue
		}
		var t *toolTally
		switch {
		case d.WorkSession != "":
			t = g.bySession[d.WorkSession]
		case at > fromStamp && at <= throughStamp:
			t = g.loose
			if ms, err := time.Parse(interaction.TimeLayout, at); err == nil && (g.oldest == 0 || ms.UnixMilli() < g.oldest) {
				g.oldest = ms.UnixMilli()
			}
		}
		if t == nil {
			continue
		}
		k := toolKey{tool: d.Tool, outcome: outcome}
		scope := d.WorkSession + "\x00" + d.Tool
		if d.WorkSession == "" {
			scope = "turn " + turn + "\x00" + d.Tool
		}
		if interaction.Outcome(outcome) == interaction.Succeeded {
			for _, w := range t.waiting[scope] {
				t.recovered[w]++
				t.recoveredBy[w] = call
			}
			delete(t.waiting, scope)
		}
		if interaction.Outcome(outcome) != interaction.Succeeded {
			k.reason = d.ReasonCode
			if k.reason != "" {
				k.reason = toolReasonLabel(k.reason)
			}
			t.example[k] = call
			if interaction.Outcome(outcome) == interaction.Refused {
				t.gate[k] = gate
			}
			t.waiting[scope] = append(t.waiting[scope], toolKey{tool: d.Tool, outcome: outcome, reason: k.reason})
		}
		t.counts[k]++
	}
	return g, rs.Err()
}

func (s *Store) usesLocked(t *toolTally) ([]rows.ToolUse, error) {
	byTool := map[string]*rows.ToolUse{}
	var names []string
	for k, n := range t.counts {
		u := byTool[k.tool]
		if u == nil {
			u = &rows.ToolUse{Tool: k.tool}
			byTool[k.tool] = u
			names = append(names, k.tool)
		}
		if interaction.Outcome(k.outcome) == interaction.Succeeded {
			u.Succeeded += n
			continue
		}
		p := rows.ToolPair{Outcome: k.outcome, Reason: k.reason, Count: n, Gate: gateSentence(t.gate[k]), Recovered: t.recovered[k]}
		for _, c := range []struct {
			id   string
			into *string
		}{{t.example[k], &p.Args}, {t.recoveredBy[k], &p.RecoveredArgs}} {
			if c.id == "" {
				continue
			}
			var content string
			err := s.h().QueryRow(`SELECT content FROM conversations WHERE id = ?`, c.id).Scan(&content)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("tool outcomes: call %s: %w", c.id, err)
			}
			*c.into = callArgs(content, k.tool)
		}
		u.Pairs = append(u.Pairs, p)
	}
	out := make([]rows.ToolUse, 0, len(names))
	for _, name := range names {
		u := byTool[name]
		sort.Slice(u.Pairs, func(i, j int) bool {
			a, b := u.Pairs[i], u.Pairs[j]
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			return a.Outcome+"\x00"+a.Reason < b.Outcome+"\x00"+b.Reason
		})
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].NonSuccess(), out[j].NonSuccess(); a != b {
			return a > b
		}
		return out[i].Tool < out[j].Tool
	})
	return out, nil
}

func callArgs(content, tool string) string {
	rest, ok := strings.CutPrefix(content, "→ "+tool+"(")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(rest, ")")
}

func gateSentence(content string) string {
	_, said, ok := strings.Cut(content, "\n← ")
	if !ok {
		return ""
	}
	return strings.TrimPrefix(said, "Error: ")
}

func toolSince(then, now map[toolKey]int) []rows.ToolChange {
	succeeded := map[string]int{}
	for k, n := range now {
		if interaction.Outcome(k.outcome) == interaction.Succeeded {
			succeeded[k.tool] += n
		}
	}
	var out []rows.ToolChange
	for k, n := range then {
		if interaction.Outcome(k.outcome) == interaction.Succeeded {
			continue
		}
		out = append(out, rows.ToolChange{Tool: k.tool, Outcome: k.outcome, Reason: k.reason, Then: n, Now: now[k], NowSucceeded: succeeded[k.tool]})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Tool+"\x00"+a.Outcome+"\x00"+a.Reason < b.Tool+"\x00"+b.Outcome+"\x00"+b.Reason
	})
	return out
}
