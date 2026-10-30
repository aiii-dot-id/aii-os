package rows

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const ToolObservationPrefix = OutcomeObservationPrefix + "tool_"

const ToolObservationPairsMax = 8

const ToolObservationMany = "x"

func ToolPairKey(tool, outcome, reason string) string {
	sum := sha256.Sum256([]byte(tool + "\x00" + outcome + "\x00" + reason))
	return hex.EncodeToString(sum[:4])
}

func ToolObservationID(from, through int64, pairs []string, tail string) string {
	keys := map[string]bool{}
	for _, p := range pairs {
		keys[p] = true
	}
	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	sort.Strings(list)
	if len(list) > ToolObservationPairsMax {
		list = append(list[:ToolObservationPairsMax], ToolObservationMany)
	}
	named := strings.Join(list, "-")
	if named == "" {
		named = "0"
	}
	return fmt.Sprintf("%s%013d_%013d_%s_%s", ToolObservationPrefix, from, through, named, tail)
}

type ToolObservation struct {
	From, Through           int64
	FromAfter, ThroughAfter string
	Pairs                   []string
	Many                    bool
}

func ParseToolObservationID(id string) (ToolObservation, bool) {
	rest, ok := strings.CutPrefix(id, ToolObservationPrefix)
	if !ok {
		return ToolObservation{}, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 4 {
		return ToolObservation{}, false
	}
	from, err1 := strconv.ParseInt(parts[0], 10, 64)
	through, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || from < 0 || through < from {
		return ToolObservation{}, false
	}
	o := ToolObservation{From: from, Through: through}
	if strings.HasPrefix(parts[3], "b1-") {
		boundary := strings.Split(parts[3], "-")
		if len(boundary) != 4 {
			return ToolObservation{}, false
		}
		start, e1 := hex.DecodeString(boundary[1])
		end, e2 := hex.DecodeString(boundary[2])
		if e1 != nil || e2 != nil || (from == through && string(start) > string(end)) {
			return ToolObservation{}, false
		}
		o.FromAfter, o.ThroughAfter = string(start), string(end)
	}
	if parts[2] != "0" {
		for _, k := range strings.Split(parts[2], "-") {
			if k == ToolObservationMany {
				o.Many = true
				continue
			}
			o.Pairs = append(o.Pairs, k)
		}
	}
	return o, true
}

type ToolPair struct {
	Outcome, Reason string
	Count           int
	Args, Gate      string
	Recovered       int
	RecoveredArgs   string
}

type ToolUse struct {
	Tool      string
	Succeeded int
	Pairs     []ToolPair
}

func (u ToolUse) NonSuccess() int {
	n := 0
	for _, p := range u.Pairs {
		n += p.Count
	}
	return n
}

type ToolSession struct {
	ID, Description, Outcome, Grade string
	StartedAt, DeliveredAt          int64
	Tools                           []ToolUse
}

type ToolChange struct {
	Tool, Outcome, Reason string
	Then, Now             int
	NowSucceeded          int
}

type ToolOutcomeBatch struct {
	From, Through, CursorWas                int64
	FromAfter, ThroughAfter, CursorAfterWas string
	CursorRaw                               [2]string
	Sessions                                []ToolSession
	Loose                                   []ToolUse
	Last                                    *ToolObservation
	Since                                   []ToolChange
	Oldest                                  int64
}

func (b ToolOutcomeBatch) MovesCursor() bool {
	return b.Through > b.CursorWas || b.Through == b.CursorWas && b.ThroughAfter > b.CursorAfterWas
}

func (b ToolOutcomeBatch) ObservationID(hash string) string {
	tail := hash
	if b.FromAfter != "" || b.ThroughAfter != "" {
		tail = "b1-" + hex.EncodeToString([]byte(b.FromAfter)) + "-" + hex.EncodeToString([]byte(b.ThroughAfter)) + "-" + hash
	}
	return ToolObservationID(b.From, b.Through, b.Pairs(), tail)
}

func (b ToolOutcomeBatch) Pairs() []string {
	var keys []string
	add := func(uses []ToolUse) {
		for _, u := range uses {
			for _, p := range u.Pairs {
				keys = append(keys, ToolPairKey(u.Tool, p.Outcome, p.Reason))
			}
		}
	}
	for _, s := range b.Sessions {
		add(s.Tools)
	}
	add(b.Loose)
	for _, c := range b.Since {
		keys = append(keys, ToolPairKey(c.Tool, c.Outcome, c.Reason))
	}
	return keys
}

func (b ToolOutcomeBatch) Shown() int {
	n := len(b.Sessions) + len(b.Since)
	if len(b.Loose) > 0 {
		n++
	}
	return n
}
