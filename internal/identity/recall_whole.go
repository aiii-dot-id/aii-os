package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

const (
	ring3WholeRead   = "recall source=ring3"
	briefWholeRead   = "recall source=brief"
	arrivalWholeRead = "recall source=inbound id=%s"
	ring3WholeName   = "Ring 3"
	briefWholeName   = "your brief"
	arrivalWholeName = "arrival %s"
	ring3WholeTitle  = "What You Are Discovering (Ring 3), as your unconscious authored it"
	briefWholeTitle  = "Your brief, written %s (UTC)"
	arrivalTitle     = "Arrival %s on %s, received %s"
	wholeHead        = "%s — version %s, part %d of %d:"
	wholeEnd         = "[end of %s, version %s, part %d of %d"
	wholeNext        = "; continues: %s part=\"%d@%s\"]"
	wholeEmpty       = "%s: nothing is written yet."
	arrivalsHeading  = "Inbound — what arrived, newest first, %d a page:"
	arrivalsLine     = "  [seq %d, %s, %s] %s"
	arrivalsWhole    = " [shortened: " + arrivalWholeRead + " reads it whole]"
	arrivalsNone     = "Nothing has arrived."
	arrivalsNoOlder  = "No older arrival before after_seq=%d."
	ringStateWhole   = " [shortened: recall source=%s reads it whole]"
)

const (
	wholeChangedWhy  = "%s has changed since version %s (its text, or the size of its parts), and a part of an older version is not given; it is now version %s. Read it again from its start: %s"
	wholeNoPartWhy   = "%s, version %s, has %d part(s); part %d is not one of them. Read it from its start: %s"
	wholePartWhy     = "part is the one a part's last line names, \"P@version\" (as part=\"2@3f9a01c2\"); omit it for the first part"
	wholeAloneWhy    = "part continues a whole read: source=ring3, source=brief, or source=inbound with id"
	wholeCursorWhy   = "%s reads it whole; after_seq pages listings — drop it"
	wholeQueryWhy    = "%s reads it whole and takes no words (query=\"\"); omit source to search your memory by words"
	wholeBoundWhy    = "%s is read in parts of at least %d characters with their labels, %d in all; prompt.max_tool_result_chars allows %d. Ask your operator to raise it"
	arrivalUnknownBy = "No arrival has id %s in your record: an arrival's id is on its line in a turn, and in recall source=inbound query=\"\""
)

const (
	recallInboundSource = " (query=\"\" lists what arrived, newest first, paged; id= reads one whole)"
	recallInboundID     = "; source=inbound: an arrival's id"
	recallWholeSource   = "; ring3 (What You Are Discovering) and brief, in parts"
	recallPartParam     = "source=ring3, brief, or inbound with id: the part=\"P@version\" a part's last line names; omit for the first"
)

type WholeReadRefusal struct {
	Code string
	Why  string
}

func (e *WholeReadRefusal) Error() string { return e.Why }

func refuseWhole(code, format string, args ...any) error {
	return &WholeReadRefusal{Code: code, Why: fmt.Sprintf(format, args...)}
}

const (
	recallArrivalPage    = 10
	recallArrivalPreview = 200

	recallArrivalSender = 200

	wholeLabelDigits = 999999
)

var wholePartRef = regexp.MustCompile(`^\s*(\d+)\s*@\s*([0-9a-fA-F]{8})\s*$`)

func wholePart(args map[string]any) (part int, version string, given bool, err error) {
	raw, present := args["part"]
	if !present || raw == nil {
		return 0, "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return 0, "", true, refuseWhole("whole.part_form", wholePartWhy)
	}
	if strings.TrimSpace(s) == "" {
		return 0, "", false, nil
	}
	m := wholePartRef.FindStringSubmatch(s)
	if m == nil {
		return 0, "", true, refuseWhole("whole.part_form", wholePartWhy)
	}
	part, err = strconv.Atoi(m[1])
	if err != nil {
		return 0, "", true, refuseWhole("whole.part_form", wholePartWhy)
	}
	return part, strings.ToLower(m[2]), true, nil
}

func wholeVersion(text string, size int) string {
	sum := sha256.Sum256([]byte(text + "\x00" + strconv.Itoa(size)))
	return hex.EncodeToString(sum[:4])
}

func ArrivalRef(id string) string {
	id = untrusted.Scrub(id)
	if runes := []rune(id); len(runes) > store.MaxArrivalIDChars {
		id = string(runes[:store.MaxArrivalIDChars]) + "…"
	}
	return strconv.Quote(id)
}

func arrivalReadRef(id string) string {
	return strings.ReplaceAll(strconv.Quote(id), "[", `\u005b`)
}

type whole struct {
	name   string
	read   string
	title  string
	text   string
	source string
}

func (e *Engine) recallWhole(ctx context.Context, source, id string, part int, version string) (string, error) {
	var w whole
	switch source {
	case "ring3":
		w = whole{name: ring3WholeName, read: ring3WholeRead, title: ring3WholeTitle}
		if e.rings != nil {
			w.text = prompt.RenderRing3Body(e.rings.Sections(ring.Ring3))
		}
	case "brief":
		var text string
		var written time.Time
		if e.rings != nil {
			text, written = e.rings.Brief()
		}
		w = whole{name: briefWholeName, read: briefWholeRead, title: fmt.Sprintf(briefWholeTitle, written.UTC().Format("2006-01-02")), text: text}
	default:
		m, ok, err := e.store.InboundByID(id)
		if err != nil {
			return "", fmt.Errorf("recall inbound %s: %w", ArrivalRef(id), err)
		}
		if !ok {
			return "", refuseWhole("whole.arrival_unknown", arrivalUnknownBy, ArrivalRef(id))
		}
		ref := ArrivalRef(m.ID)
		sender := m.Address
		if runes := []rune(sender); len(runes) > recallArrivalSender {
			sender = string(runes[:recallArrivalSender]) + "…"
		}
		received := time.UnixMilli(m.ReceivedMs).UTC().Format(time.RFC3339)
		w = whole{name: fmt.Sprintf(arrivalWholeName, ref), read: fmt.Sprintf(arrivalWholeRead, arrivalReadRef(m.ID)),
			title: fmt.Sprintf(arrivalTitle, ref, m.Channel, received), text: untrusted.Scrub(m.Body), source: m.Channel + ":" + sender}
	}
	if w.text == "" && version == "" {
		return fmt.Sprintf(wholeEmpty, w.title), nil
	}

	bound := e.recallBound(ctx)
	labels := utf8.RuneCountInString(fmt.Sprintf(wholeHead, w.title, "00000000", wholeLabelDigits, wholeLabelDigits)) +
		utf8.RuneCountInString(w.close("00000000", wholeLabelDigits-1, wholeLabelDigits)) + 4
	if w.source != "" {
		labels += utf8.RuneCountInString(untrusted.Wrap(w.source, ""))
	}
	size := min(recallReasoningPart, bound-labels)
	if size < recallPartFloor {
		return "", refuseWhole("whole.bound", wholeBoundWhy, w.title, recallPartFloor, recallPartFloor+labels, bound)
	}
	now := wholeVersion(w.text, size)
	if version != "" && version != now {
		return "", refuseWhole("whole.changed", wholeChangedWhy, w.name, version, now, w.read)
	}
	var parts []string
	if w.source == "" {
		parts = framedParts(w.text, size)
	} else {
		runes := []rune(w.text)
		for i := 0; i < len(runes); i += size {
			parts = append(parts, untrusted.Wrap(w.source, string(runes[i:min(i+size, len(runes))])))
		}
	}
	if part == 0 {
		part = 1
	}
	if part > len(parts) {
		return "", refuseWhole("whole.no_part", wholeNoPartWhy, w.name, now, len(parts), part, w.read)
	}
	return fmt.Sprintf(wholeHead, w.title, now, part, len(parts)) + "\n\n" + parts[part-1] + "\n\n" + w.close(now, part, len(parts)), nil
}

func (w whole) close(version string, part, parts int) string {
	out := fmt.Sprintf(wholeEnd, w.name, version, part, parts)
	if part < parts {
		return out + fmt.Sprintf(wholeNext, w.read, part+1, version)
	}
	return out + "]"
}

func framedParts(text string, size int) []string {
	closing := utf8.RuneCountInString(untrusted.Close) + 1
	var parts []string
	var b strings.Builder
	n, used := 0, false
	inside, reopen := false, ""
	flush := func() {
		body := b.String()
		if inside {
			body += "\n" + untrusted.Close
		}
		parts = append(parts, body)
		b.Reset()
		n, used = 0, false
		if inside {
			b.WriteString(reopen + "\n")
			n = utf8.RuneCountInString(reopen) + 1
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		for line != "" {
			room := size - n - closing
			r := utf8.RuneCountInString(line)
			if r <= room {
				b.WriteString(line)
				n, used = n+r, true
				inside, reopen = frameState(inside, reopen, line, size)
				break
			}

			fresh := size - closing
			if inside {
				fresh -= utf8.RuneCountInString(reopen) + 1
			}
			if used && r <= fresh {
				flush()
				continue
			}

			_, k := untrusted.Head(line, room)
			if k == 0 && used {
				flush()
				continue
			}
			if k == 0 {
				k = max(1, room)
			}
			runes := []rune(line)
			piece := string(runes[:k])
			b.WriteString(piece)
			n, used = n+k, true
			inside, reopen = frameState(inside, reopen, piece, size)
			line = string(runes[k:])
			flush()
		}
	}
	if used || len(parts) == 0 {
		flush()
	}
	return parts
}

func frameState(inside bool, reopen, s string, size int) (bool, string) {
	for {
		o, c := strings.Index(s, untrusted.Open), strings.Index(s, untrusted.Close)
		switch {
		case o < 0 && c < 0:
			return inside, reopen
		case c < 0 || (o >= 0 && o < c):
			inside, reopen = true, untrusted.Open
			if line, _, _ := strings.Cut(s[o:], "\n"); openLine(line) && utf8.RuneCountInString(line) <= size/4 {
				reopen = line
			}
			s = s[o+len(untrusted.Open):]
		default:
			inside = false
			s = s[c+len(untrusted.Close):]
		}
	}
}

func (e *Engine) recallArrivals(ctx context.Context, cursor uint64, limit int, limitNote string) (string, error) {
	n := limit
	if n == 0 {
		n = recallArrivalPage
	}

	rows, err := e.store.ArrivalsBefore(cursor, n+1, recallArrivalPreview+1)
	if err != nil {
		return "", fmt.Errorf("recall inbound: %w", err)
	}
	if limitNote != "" {
		limitNote = "\n" + limitNote
	}
	if len(rows) == 0 {
		if cursor > 0 {
			return fmt.Sprintf(arrivalsNoOlder, cursor) + limitNote, nil
		}
		return arrivalsNone + limitNote, nil
	}
	p := &recallPart{heading: "Inbound", source: "inbound", covered: true, more: len(rows) > n}
	for _, a := range rows[:min(n, len(rows))] {
		shown := recallExcerpt(a.Body, recallArrivalPreview)
		line := fmt.Sprintf(arrivalsLine, a.Seq, ArrivalRef(a.ID), a.Channel, untrusted.Wrap(a.Channel+":"+a.Address, shown))
		if shown != a.Body {
			line += fmt.Sprintf(arrivalsWhole, arrivalReadRef(a.ID))
		}
		p.items = append(p.items, line)
		p.seqs = append(p.seqs, a.Seq)
	}
	bound := e.recallBound(ctx)
	return fitParts([]*recallPart{p}, bound, func(fits bool) string {
		out := []string{fmt.Sprintf(arrivalsHeading, n)}
		out = append(out, p.items...)
		return strings.Join(out, "\n") + recallCoverage([]*recallPart{p}) + recallCutLine([]*recallPart{p}, bound, fits) + limitNote
	}), nil
}
