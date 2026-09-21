package ledger

import (
	"fmt"
	"strings"
)

// .
// .
type Boundary struct {
	Seq  uint64
	Hash string
}

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
// .
// .
// .
// .
// .
// .
type VerifyFailure struct {
	// .
	Requirement string
	// .
	// .
	// .
	Seq uint64
	// .
	// .
	Container string
	// .
	// .
	Expected, Observed string
	// .
	NextSeq uint64

	Proved    Boundary
	Traversed Boundary

	// .
	Err error
}

func (f *VerifyFailure) Unwrap() error { return f.Err }

func (f *VerifyFailure) Error() string {
	var b strings.Builder
	switch {
	case f.Seq != 0 && f.Container != "":
		fmt.Fprintf(&b, "record %d (%s): ", f.Seq, f.Container)
	case f.Seq != 0:
		fmt.Fprintf(&b, "record %d: ", f.Seq)
	}
	b.WriteString(f.Requirement)
	if f.Expected != "" || f.Observed != "" {
		fmt.Fprintf(&b, ": expected %s, found %s", orNone(f.Expected), orNone(f.Observed))
	}
	if f.Seq == 0 && f.NextSeq != 0 {
		fmt.Fprintf(&b, " (the walk expected record %d next)", f.NextSeq)
	}
	b.WriteString(" — ")
	b.WriteString(f.established())
	return b.String()
}

// .
func (f *VerifyFailure) established() string {
	switch {
	case f.Proved.Seq == 0 && f.Traversed.Seq == 0:
		return "nothing was established"
	case f.Proved.Seq == 0:
		return fmt.Sprintf("nothing proved; links checked through record %d, which no verified proof covers", f.Traversed.Seq)
	case f.Traversed.Seq > f.Proved.Seq:
		return fmt.Sprintf("proved through record %d (%s); links checked through record %d, which no verified proof covers yet",
			f.Proved.Seq, shortEntryHash(f.Proved.Hash), f.Traversed.Seq)
	default:
		return fmt.Sprintf("proved through record %d (%s)", f.Proved.Seq, shortEntryHash(f.Proved.Hash))
	}
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

func shortEntryHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
