package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

func (s *Store) experienceMark() string {
	if s.historicalExperienceMark != "" {
		return s.historicalExperienceMark
	}
	return "unmetabolized"
}

func (s *Store) experienceColumns() string {
	return `id, content, category, raw, ` + s.experienceMark() + `, provenance, created_seq, created_at, ` + s.citedOperatorWords("experiences")
}

func (s *Store) citedOperatorWords(table string) string {
	return `CASE WHEN ` + table + `.provenance = 'operator' THEN (SELECT content FROM conversations
		 WHERE turn_seq = (SELECT json_extract(payload, '$.source_turn') FROM ledger WHERE seq = ` + table + `.created_seq)
		   AND ` + s.operatorWordSelection() + `) END`
}

func quotesOperator(turn, note string) bool {
	n := foldWords(note)
	if n == "" {
		return false
	}
	if n == foldWords(turn) {
		return true
	}
	n = closedSentence(n)
	for _, sentence := range sentencesOf(turn) {
		if n == closedSentence(foldWords(sentence)) {
			return true
		}
	}
	return false
}

func foldWords(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

const sentenceMarks = ".!?…"

func closedSentence(s string) string {
	if s != "" && strings.TrimRight(s, sentenceMarks) == s {
		return s + "."
	}
	return s
}

var notSentenceEnds = map[string]bool{"e.g": true, "i.e": true, "etc": true, "vs": true, "cf": true}

func sentencesOf(text string) []string {
	var out []string
	r := []rune(text)
	start := 0
	cut := func(end int) {
		if s := strings.TrimSpace(string(r[start:end])); s != "" {
			out = append(out, s)
		}
		start = end
	}
	for i := 0; i < len(r); i++ {
		if r[i] == '\n' {
			cut(i)
			continue
		}
		if !strings.ContainsRune(sentenceMarks, r[i]) {
			continue
		}
		j := i
		for j+1 < len(r) && strings.ContainsRune(sentenceMarks, r[j+1]) {
			j++
		}
		if endsSentence(r, i, j) {
			cut(j + 1)
		}
		i = j
	}
	cut(len(r))
	return out
}

func endsSentence(r []rune, i, j int) bool {
	run := string(r[i : j+1])
	if strings.ContainsRune(run, '…') || strings.Count(run, ".") > 1 {
		return false
	}

	if dot := strings.IndexRune(run, '.'); dot > 0 || (dot == 0 && (i == 0 || unicode.IsSpace(r[i-1]))) {
		return false
	}
	if r[i] == '.' {
		k := i
		for k > 0 && !unicode.IsSpace(r[k-1]) {
			k--
		}
		word := strings.TrimLeftFunc(string(r[k:i]), func(c rune) bool { return !unicode.IsLetter(c) })
		if notSentenceEnds[strings.ToLower(word)] {
			return false
		}
	}
	next := j + 1
	if next == len(r) {
		return true
	}
	if !unicode.IsSpace(r[next]) {
		return false
	}
	for ; next < len(r) && unicode.IsSpace(r[next]); next++ {
		if r[next] == '\n' {
			return true
		}
	}
	if next == len(r) {
		return true
	}
	c := r[next]
	return unicode.IsLetter(c) && !unicode.IsLower(c)
}

func (s *Store) ExperienceMarkColumn() string { return s.experienceMark() }

func (s *Store) ListExperiences(n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT `+s.experienceColumns()+`
		 FROM experiences ORDER BY created_seq DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanExperiences(rows)
}

func (s *Store) ListExperiencesForConsolidate(n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT `+s.experienceColumns()+`
		 FROM experiences WHERE `+s.experienceMark()+` = 0 ORDER BY created_seq DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanExperiences(rows)
}

func (s *Store) UnprocessedExperienceCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM experiences WHERE raw = 1`).Scan(&count)
	return count, err
}

func scanExperiences(rows *sql.Rows) ([]Experience, error) {
	var experiences []Experience
	for rows.Next() {
		var e Experience
		if err := scanExperience(rows, &e); err != nil {
			return nil, err
		}
		experiences = append(experiences, e)
	}
	return experiences, rows.Err()
}

func scanExperience(row rowScanner, e *Experience) error {
	var category, provenance, cited sql.NullString
	if err := row.Scan(&e.ID, &e.Content, &category, &e.Raw, &e.Unmetabolized, &provenance, &e.CreatedSeq, &e.CreatedAt, &cited); err != nil {
		return err
	}
	e.OperatorVerbatim = cited.Valid && quotesOperator(cited.String, e.Content)
	e.Category, e.Provenance = category.String, provenance.String
	return nil
}

func (s *Store) ProvenanceByIDs(ids []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	for _, id := range ids {
		var prov string
		err := s.db.QueryRow(`SELECT provenance FROM experiences WHERE id = ?`, id).Scan(&prov)
		if err == nil {
			out[id] = prov
			continue
		}

		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("provenance of %s: %w", id, err)
		}
	}
	return out, nil
}

func (s *Store) ListRawExperiences(n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT `+s.experienceColumns()+`
		 FROM experiences WHERE raw = 1 ORDER BY created_seq ASC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

func (s *Store) ListRawExperiencesExcept(n int, idPrefix string) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT `+s.experienceColumns()+`
		 FROM experiences WHERE raw = 1 AND substr(id, 1, ?) != ? ORDER BY created_seq ASC LIMIT ?`,
		len(idPrefix), idPrefix, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

func (s *Store) ListExperiencesSince(after time.Time, n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT ` + s.experienceColumns() + `
		 FROM experiences ORDER BY created_seq DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Experience
	for rows.Next() {
		var e Experience
		if err := scanExperience(rows, &e); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("experience %s: created_at %q: %w", e.ID, e.CreatedAt, err)
		}
		if at.Before(after) {
			break
		}
		if len(out) == n {
			break
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListExperiencesByProvenance(provenances []string, n int) ([]Experience, error) {
	if len(provenances) == 0 || n <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(provenances)), ",")
	args := make([]any, 0, len(provenances)+1)
	for _, p := range provenances {
		args = append(args, p)
	}
	args = append(args, n)
	rows, err := s.db.Query(
		`SELECT `+s.experienceColumns()+`
		 FROM experiences WHERE provenance IN (`+marks+`)
		 ORDER BY created_seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}
