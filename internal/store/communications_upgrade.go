package store

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const communicationsBeforeUpgrade = "communications_before_upgrade"

func historicalCommunicationTables() []string {
	return []string{"correspondents", "reach", "outbox_route"}
}

type communicationValue struct {
	JSON      json.RawMessage `json:"value"`
	Blob      bool            `json:"blob,omitempty"`
	Real      bool            `json:"real,omitempty"`
	TextBytes bool            `json:"text_bytes,omitempty"`
}
type communicationRow map[string]communicationValue
type communicationSource map[string][]communicationRow

func (r communicationRow) text(name string) string {
	var value string
	if r[name].Blob || r[name].TextBytes || json.Unmarshal(r[name].JSON, &value) != nil {
		return ""
	}
	return value
}

func (r communicationRow) integer(name string) int64 {
	var value int64
	_ = json.Unmarshal(r[name].JSON, &value)
	return value
}

func (s *Store) walkCommunicationRows(table string, keep func(string) bool, visit func(int64, communicationRow) error) error {
	rows, err := s.h().Query("SELECT rowid,* FROM " + quoteIdentifier(table) + " ORDER BY rowid")
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	values, into := make([]any, len(columns)), make([]any, len(columns))
	for i := range into {
		into[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(into...); err != nil {
			return err
		}
		id, ok := values[0].(int64)
		if !ok {
			return fmt.Errorf("historical %s has no integer rowid", table)
		}
		row := communicationRow{}
		for i, name := range columns {
			if i == 0 || !keep(name) {
				continue
			}
			value := values[i]
			_, blob := value.([]byte)
			_, real := value.(float64)
			textBytes := false
			if text, ok := value.(string); ok && !utf8.ValidString(text) {
				value = []byte(text)
				textBytes = true
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			row[name] = communicationValue{JSON: raw, Blob: blob, Real: real, TextBytes: textBytes}
		}
		if err := visit(id, row); err != nil {
			return err
		}
	}
	return rows.Err()
}

func communicationNameKey(name string) string {
	return strings.Map(func(r rune) rune {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		return smallest
	}, strings.TrimSpace(name))
}
func communicationPrefix(table string) string { return communicationsBeforeUpgrade + "/" + table + "/" }
func communicationNamePrefix(name string) string {
	return communicationPrefix("correspondents") + hex.EncodeToString([]byte(communicationNameKey(name))) + "/"
}

func (s *Store) retainCommunicationRow(table string, id int64, row communicationRow) error {
	prefix := communicationPrefix(table)
	if table == "correspondents" {
		prefix = communicationNamePrefix(row.text("label"))
	}
	key := prefix + fmt.Sprintf("%016x", uint64(id)^(uint64(1)<<63))
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if _, err := s.h().Exec("INSERT INTO runtime_meta(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING", key, string(raw), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	var got string
	if err := s.h().QueryRow("SELECT value FROM runtime_meta WHERE key=?", key).Scan(&got); err != nil {
		return err
	}
	if got != string(raw) {
		return &SchemaError{Phase: "communications source", Cause: fmt.Errorf("retained row conflict in %s; neither source overwritten", table)}
	}
	return nil
}

func (s *Store) visitCommunicationPrefix(prefix string, visit func(communicationRow) error) error {
	rows, err := s.h().Query("SELECT value FROM runtime_meta WHERE key>=? AND key<? ORDER BY key", prefix, prefix+"~")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var row communicationRow
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

type communicationImageReader struct {
	q      dbi
	offset int64
}

func (r *communicationImageReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	size := len(p)
	if size > 64*1024 {
		size = 64 * 1024
	}
	var data []byte
	err := r.q.QueryRow("SELECT substr(CAST(value AS BLOB),?,?) FROM runtime_meta WHERE key=?", r.offset+1, size, communicationsBeforeUpgrade).Scan(&data)
	if err == sql.ErrNoRows {
		return 0, io.EOF
	}
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, data)
	r.offset += int64(n)
	return n, nil
}
func (s *Store) visitCommunicationArchive(table string, visit func(communicationRow) error) error {
	var exists bool
	if err := s.h().QueryRow("SELECT EXISTS(SELECT 1 FROM runtime_meta WHERE key=?)", communicationsBeforeUpgrade).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return s.visitCommunicationPrefix(communicationPrefix(table), visit)
	}
	dec := json.NewDecoder(&communicationImageReader{q: s.h()})
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("historical communications image is not an object")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if token != json.Delim('[') {
			return fmt.Errorf("historical communications rows are not an array")
		}
		for dec.More() {
			if key == table {
				var row communicationRow
				if err := dec.Decode(&row); err != nil {
					return err
				}
				if err := visit(row); err != nil {
					return err
				}
			} else {
				var ignored struct{}
				if err := dec.Decode(&ignored); err != nil {
					return err
				}
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("historical communications image has trailing data: %v", err)
	}
	return nil
}

type LegacyContact struct {
	Name, Channel, Address string
	Wake                   bool
}

func (s *Store) LegacyContacts() ([]LegacyContact, []string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	source := communicationSource{}

	needed := map[string]bool{}
	if err := s.visitCommunicationArchive("reach", func(row communicationRow) error {
		selected := communicationRow{}
		for _, field := range []string{"label", "correspondent_id", "channel", "address", "rank", "created_ms", "wake"} {
			if value, ok := row[field]; ok {
				selected[field] = value
			}
		}
		source["reach"] = append(source["reach"], selected)
		if id := row.text("correspondent_id"); id != "" {
			needed[id] = true
		}
		return nil
	}); err != nil {
		return nil, nil, &SchemaError{Phase: "communications source", Cause: err}
	}
	if len(needed) > 0 {
		if err := s.visitCommunicationArchive("correspondents", func(row communicationRow) error {
			if needed[row.text("id")] {
				source["correspondents"] = append(source["correspondents"], communicationRow{"id": row["id"], "label": row["label"]})
			}
			return nil
		}); err != nil {
			return nil, nil, &SchemaError{Phase: "communications source", Cause: err}
		}
	}
	return source.contacts()
}

func (source communicationSource) contacts() ([]LegacyContact, []string, error) {
	labels := map[string]string{}
	for _, row := range source["correspondents"] {
		labels[row.text("id")] = row.text("label")
	}
	rows := append([]communicationRow(nil), source["reach"]...)
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := rows[i].integer("rank"), rows[j].integer("rank"); a != b {
			return a < b
		}
		return rows[i].integer("created_ms") < rows[j].integer("created_ms")
	})
	var contacts []LegacyContact
	var issues []string
	nameOf := func(row communicationRow) string {
		if _, old := row["correspondent_id"]; old {
			return labels[row.text("correspondent_id")]
		}
		return row.text("label")
	}
	spellings, ambiguousNames := map[string]string{}, map[string]bool{}
	for _, row := range rows {
		name := nameOf(row)
		key := communicationNameKey(name)
		if prior, found := spellings[key]; found && prior != name {
			ambiguousNames[key] = true
		}
		spellings[key] = name
	}
	for i, row := range rows {
		name := nameOf(row)
		channel, address := row.text("channel"), row.text("address")
		ambiguous := ambiguousNames[communicationNameKey(name)]
		for _, field := range []string{"rank", "created_ms", "wake"} {
			var value int64
			if row[field].Blob || json.Unmarshal(row[field].JSON, &value) != nil {
				ambiguous = true
			}
		}
		if strings.TrimSpace(name) == "" || channel == "" || address == "" || ambiguous {
			issues = append(issues, fmt.Sprintf("historical reach row %d has no unambiguous name/channel/address; retained in runtime_meta.%s", i+1, communicationsBeforeUpgrade))
			continue
		}
		contacts = append(contacts, LegacyContact{name, channel, address, row.integer("wake") == 1})
	}
	return contacts, issues, nil
}

func (s *Store) reconcileCommunications(target string, report *ReconcileReport) error {
	retired := map[string]bool{}
	for _, name := range declaredRetiredTables(target) {
		retired[name] = true
	}
	for _, name := range historicalCommunicationTables() {
		if !retired[name] {
			return nil
		}
	}
	ref, err := referenceSchema(target)
	if err != nil {
		return err
	}
	source := map[string]map[string]bool{}
	for _, name := range []string{"correspondents", "reach", "outbox_route", "inbound"} {
		cols, err := s.liveColumns(name)
		if err != nil {
			return err
		}
		if len(cols) == 0 {
			continue
		}
		if name == "inbound" && !cols["seen"] && !cols["standing"] && !cols["correspondent_id"] && !cols["content"] {
			continue
		}
		source[name] = cols
	}
	if len(source) == 0 {
		return nil
	}
	if source["correspondents"] != nil || source["outbox_route"] != nil {
		cols, err := s.liveColumns("outbox")
		if err != nil {
			return err
		}
		if len(cols) > 0 {
			source["outbox"] = cols
		}
	}
	if _, exists, err := s.liveObjectSQL("table", "runtime_meta"); err != nil {
		return err
	} else if !exists {
		if _, err := s.h().Exec(ref.Objects["table"]["runtime_meta"]); err != nil {
			return err
		}
	}
	var before int64
	if err := s.h().QueryRow("SELECT COUNT(*) FROM runtime_meta").Scan(&before); err != nil {
		return err
	}
	var imageExists bool
	if err := s.h().QueryRow("SELECT EXISTS(SELECT 1 FROM runtime_meta WHERE key=?)", communicationsBeforeUpgrade).Scan(&imageExists); err != nil {
		return err
	}
	if imageExists {
		return &SchemaError{Phase: "communications source", Cause: fmt.Errorf("a retained image and unconverted source tables coexist; neither source overwritten")}
	}
	counts := map[string]int64{}
	for _, table := range sortedKeys(source) {
		keep := func(name string) bool {
			if table == "outbox" {
				return name == "id" || name == "to_role" || name == "to_identity"
			}
			if table == "inbound" {
				if name == "id" || name == "seen" {
					return true
				}
				if _, copied := ref.Shapes[table][name]; copied {
					return false
				}
				for _, old := range declaredRenames(target)[table] {
					if old == name {
						return false
					}
				}
			}
			return true
		}
		if err := s.walkCommunicationRows(table, keep, func(id int64, row communicationRow) error {
			if err := s.retainCommunicationRow(table, id, row); err != nil {
				return err
			}
			counts[table]++
			return nil
		}); err != nil {
			return err
		}
	}
	var after int64
	if err := s.h().QueryRow("SELECT COUNT(*) FROM runtime_meta").Scan(&after); err != nil {
		return err
	}
	if s.schemaConversions == nil {
		s.schemaConversions = map[string]RuntimeConversion{}
	}
	s.schemaConversions["runtime_meta"] = RuntimeConversion{Before: before, After: after, Reason: "retained historical communications rows"}
	for _, name := range historicalCommunicationTables() {
		if source[name] != nil {
			s.schemaConversions[name] = RuntimeConversion{Before: counts[name], After: 0, Reason: "preserved in runtime_meta.communications_before_upgrade/"}
		}
	}
	if err := s.applyDeclaredRenames(target, report); err != nil {
		return err
	}
	for _, name := range []string{"inbound", "outbox"} {
		if source[name] == nil {
			continue
		}
		live, err := s.liveColumns(name)
		if err != nil {
			return err
		}
		want := columnNameSet(ref.Shapes[name])
		if name == "outbox" {
			lost, err := s.populatedDroppedColumns(name, live, want)
			if err != nil {
				return err
			}
			if len(lost) > 0 {
				return &SchemaError{Phase: "communications copy", Cause: fmt.Errorf("outbox fields without a mapping: %v", lost)}
			}
		}
		if _, err := s.rebuildTable(name, ref.Objects["table"][name], live, want, nil, nil); err != nil {
			return err
		}
		report.Rebuilt = append(report.Rebuilt, name+" (historical communications preserved and mapped)")
	}
	if source["correspondents"] != nil && source["outbox"] != nil {
		if err := s.walkCommunicationRows("outbox", func(name string) bool { return name == "id" || name == "to_role" || name == "to_identity" }, func(_ int64, row communicationRow) error {
			if row.text("to_role") != "peer" {
				return nil
			}
			id, old := row.text("id"), row.text("to_identity")
			label := ""
			if source["correspondents"]["id"] && source["correspondents"]["label"] {
				var value any
				err := s.h().QueryRow("SELECT label FROM correspondents WHERE id=?", old).Scan(&value)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				if text, ok := value.(string); ok && utf8.ValidString(text) {
					label = text
				}
			}
			ambiguous := false
			if label != "" {
				if err := s.visitCommunicationPrefix(communicationNamePrefix(label), func(other communicationRow) error {
					if other.text("label") != label {
						ambiguous = true
					}
					return nil
				}); err != nil {
					return err
				}
			}
			if label != old {
				if err := s.visitCommunicationPrefix(communicationNamePrefix(old), func(communicationRow) error { ambiguous = true; return nil }); err != nil {
					return err
				}
			}
			issue := ""
			if label == "" || ambiguous {
				label = old
				issue = "historical recipient is unresolved; original addressing retained in runtime_meta.communications_before_upgrade"
			}
			if _, err := s.h().Exec("UPDATE outbox SET to_identity=?,recipient_error=? WHERE id=?", label, issue, id); err != nil {
				return err
			}
			var got, reason string
			if err := s.h().QueryRow("SELECT to_identity,recipient_error FROM outbox WHERE id=?", id).Scan(&got, &reason); err != nil {
				return err
			}
			if got != label || reason != issue {
				return fmt.Errorf("historical recipient conversion failed its readback")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if source["outbox_route"] != nil {
		if err := s.walkCommunicationRows("outbox_route", func(string) bool { return true }, func(_ int64, row communicationRow) error {
			id, channel := row.text("outbox_id"), row.text("channel")
			if channel == "" {
				_, err := s.h().Exec("UPDATE outbox SET recipient_error=? WHERE id=?", "historical requested channel is unresolved; original routing retained in runtime_meta.communications_before_upgrade", id)
				return err
			}
			if _, err := s.h().Exec("UPDATE outbox SET requested_channel=? WHERE id=?", channel, id); err != nil {
				return err
			}
			var got string
			err := s.h().QueryRow("SELECT requested_channel FROM outbox WHERE id=?", id).Scan(&got)
			if err == sql.ErrNoRows {
				return nil
			}
			if err != nil {
				return err
			}
			if got != channel {
				return fmt.Errorf("historical channel restriction failed its readback")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
