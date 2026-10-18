package cursor

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var ErrCursorMoved = errors.New("the cursor moved since this pass read it")

type TurnCursor struct {
	Turn     string `json:"turn"`
	Position int    `json:"position"`
}

type TurnPart struct {
	ID    string
	Role  string
	Text  string
	From  int
	Whole bool

	TurnID string
}

type ConversationBatch struct {
	Start   string
	Through TurnCursor
	Parts   []TurnPart
}

func (b ConversationBatch) Moved() bool {
	v, _ := json.Marshal(b.Through)
	return b.Through.Turn != "" && string(v) != b.Start
}

func (b ConversationBatch) Keep(n, runes int) ConversationBatch {
	if n <= 0 || len(b.Parts) == 0 {
		return ConversationBatch{Start: b.Start}
	}
	if n > len(b.Parts) {
		n = len(b.Parts)
	}
	lastLen := TurnLength(b.Parts[n-1].Text)
	if n == len(b.Parts) && (runes <= 0 || runes >= lastLen) {
		return b
	}
	kept := append([]TurnPart(nil), b.Parts[:n]...)
	last := &kept[n-1]
	if runes > 0 && runes < lastLen {
		last.Text, last.Whole, lastLen = RuneSlice(last.Text, 0, runes), false, runes
	}
	return ConversationBatch{Start: b.Start, Parts: kept, Through: TurnCursor{Turn: last.ID, Position: last.From + lastLen}}
}

func TurnLength(content string) int { return utf8.RuneCountInString(content) }

func RuneSlice(s string, from, to int) string {
	if from <= 0 && to >= TurnLength(s) {
		return s
	}
	r := []rune(s)
	if from < 0 {
		from = 0
	}
	if to > len(r) {
		to = len(r)
	}
	if from >= to {
		return ""
	}
	return string(r[from:to])
}
