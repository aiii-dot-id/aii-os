package app

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

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
const (
	gradeServed   = "served"
	gradePartial  = "partial"
	gradeUnserved = "unserved"
)

// .
// .
const gradeCommentMax = 500

// .
func gradeWords(g string) bool {
	return g == gradeServed || g == gradePartial || g == gradeUnserved
}

// .
// .
func (a *App) gradeResult(req dashboard.GradeRequest) (uint64, error) {
	grade := strings.TrimSpace(req.Grade)
	if !gradeWords(grade) {
		return 0, fmt.Errorf("a grade is served, partial or unserved, got %q", req.Grade)
	}
	comment := strings.TrimSpace(req.Comment)
	if len(comment) > gradeCommentMax {
		return 0, fmt.Errorf("the comment is %d characters; %d is the line's length — a longer thought is an ordinary message", len(comment), gradeCommentMax)
	}
	session := strings.TrimSpace(req.Session)
	if session == "" {
		return 0, fmt.Errorf("a grade names the result it grades")
	}
	if a.store == nil || a.engine == nil {
		return 0, fmt.Errorf("this identity cannot record a grade yet")
	}
	ws, err := a.store.WorkSessionByID(session)
	if err != nil || ws == nil {
		return 0, fmt.Errorf("no work session %q to grade", session)
	}
	if ws.Status != "delivered" {
		return 0, fmt.Errorf("work session %q has not delivered yet; a grade is the word on a result", session)
	}
	// .
	// .
	// .
	// .
	if reason, safe := a.SafeMode(); safe {
		return 0, fmt.Errorf("this identity is in SAFE (%s); a grade is recorded when the record is writable again", reason)
	}
	text := fmt.Sprintf("[grade %s] %s", session, grade)
	if item := strings.TrimSpace(req.Item); item != "" {
		// .
		// .
		text = fmt.Sprintf("[grade %s %s/%s] %s", session, ws.Project, item, grade)
	}
	if comment != "" {
		text += " — " + comment
	}
	seq, err := a.engine.RecordConversationTurnSeq(roleOperator, text)
	if err != nil {
		return 0, fmt.Errorf("the grade could not be recorded: %w", err)
	}
	logsink.Info("grade.end", "the operator graded %s %s (turn %d)%s", session, grade, seq, gradeLogTail(comment))
	// .
	// .
	// .
	a.emitPluginEvent(pluginhost.TopicWorkGraded, map[string]interface{}{
		"session": session, "project": ws.Project, "grade": grade, "turn": seq,
	})
	if a.dashboard != nil {
		a.dashboard.BroadcastWork()
	}
	return seq, nil
}

func gradeLogTail(comment string) string {
	if comment == "" {
		return ""
	}
	return fmt.Sprintf(" with a line of %d characters", len(comment))
}

// .
// .
// .
func (a *App) gradesOf(sessions []string) map[string]dashboard.GradeView {
	if a.store == nil || len(sessions) == 0 {
		return nil
	}
	want := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		want[s] = true
	}
	turns, err := a.store.RecentTurns(gradeScanTurns)
	if err != nil {
		return nil
	}
	out := map[string]dashboard.GradeView{}
	// .
	// .
	// .
	// .
	for _, t := range turns {
		if t.Role != roleOperator || !strings.HasPrefix(t.Content, "[grade ") {
			continue
		}
		session, grade, comment, ok := parseGradeTurn(t.Content)
		if !ok || !want[session] {
			continue
		}
		if prev, seen := out[session]; seen && prev.Turn > t.TurnSeq {
			continue
		}
		out[session] = dashboard.GradeView{Grade: grade, Comment: comment, Turn: t.TurnSeq}
	}
	return out
}

// .
const gradeScanTurns = 200

// .
func parseGradeTurn(content string) (session, grade, comment string, ok bool) {
	rest, found := strings.CutPrefix(content, "[grade ")
	if !found {
		return "", "", "", false
	}
	head, tail, found := strings.Cut(rest, "] ")
	if !found {
		return "", "", "", false
	}
	session = head
	if i := strings.IndexByte(head, ' '); i > 0 {
		session = head[:i]
	}
	grade, comment, _ = strings.Cut(tail, " — ")
	grade = strings.TrimSpace(grade)
	if !gradeWords(grade) {
		return "", "", "", false
	}
	return session, grade, strings.TrimSpace(comment), true
}
