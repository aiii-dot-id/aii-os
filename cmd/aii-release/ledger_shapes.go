package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const ledgerShapesUsage = `usage: go run ./cmd/aii-release ledger-shapes -ledger PATH

Prints, as JSON, the payload shapes the ledger at PATH holds: for each event
type, each distinct set of members and the kinds of their values (string,
integer, number, bool, null, object, array), with how many records have it
and whether this program would admit a new record of it. No value is printed. Run it on a copy of a ledger. It verifies the chain from
record 1 as it reads and counts only verified records. It writes nothing.
Exit 0: every record verified; 1: not, or the ledger could not be read;
2: usage.

`

const (
	shapesStopsAt    = "ledger-shapes: the record stops verifying at seq %d: %v. The shapes of the %d record(s) before it are printed; nothing after it is counted.\n"
	shapesNoVerify   = "ledger-shapes: the record does not verify: %v. Nothing is counted.\n"
	shapesUnreadable = "ledger-shapes: %s cannot be read past line %d: %v\n"
)

func besideLedger(ledgerDir string) (genesis.Beside, error) {
	beside, err := witness.LoadBeside(ledgerDir, genesis.PinnedRoot())
	if err != nil {
		return nil, err
	}
	return beside, nil
}

type shapesReport struct {
	Records int                     `json:"records"`
	Types   map[string][]shapeCount `json:"types"`
}

type shapeCount struct {
	Shape    string   `json:"shape"`
	Records  int      `json:"records"`
	FirstSeq uint64   `json:"first_seq"`
	LastSeq  uint64   `json:"last_seq"`
	Members  []string `json:"members"`

	Door string `json:"door"`
}

func ledgerShapes(args []string, stdout, stderr io.Writer, load genesis.BesideLoader) int {
	fset := flag.NewFlagSet("ledger-shapes", flag.ContinueOnError)
	fset.SetOutput(stderr)
	path := fset.String("ledger", "", "the ledger file (a copy)")
	fset.Usage = func() {
		fmt.Fprint(stderr, ledgerShapesUsage)
		fset.PrintDefaults()
	}
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fset.NArg() != 0 || *path == "" {
		fset.Usage()
		return 2
	}
	beside, err := load(filepath.Dir(*path))
	if err != nil {
		fmt.Fprintf(stderr, "ledger-shapes: %v\n", err)
		beside = nil
	}

	type seen struct {
		seq         uint64
		typ, digest string
	}
	door := map[string]string{}
	var records []seen
	shapes := map[string]ledger.PayloadShape{}
	visit := func(evt *ledger.Event, _ []byte) error {
		shape, err := ledger.ShapeOf(evt.Payload)
		if err != nil {
			return fmt.Errorf("record %d: %w", evt.Seq, err)
		}
		digest := shape.Digest()
		shapes[digest] = shape
		if key := string(evt.Type) + " " + digest; door[key] == "" {
			door[key] = "admitted"
			if err := ledger.AdmitPayloadMembers(evt.Type, evt.Payload); err != nil {
				door[key] = err.Error()
			}
		}
		records = append(records, seen{seq: evt.Seq, typ: string(evt.Type), digest: digest})
		return nil
	}
	unread := false
	rest := func(container string, at int, _ []byte, err error) error {
		if err != nil && !unread {
			unread = true
			fmt.Fprintf(stderr, shapesUnreadable, container, at, err)
		}
		return nil
	}
	_, _, _, err = genesis.ReadHeld(*path, beside, visit, rest)

	code := 0
	limit := ^uint64(0)
	var failure *ledger.VerifyFailure
	switch {
	case err == nil:
	case errors.As(err, &failure):

		limit = failure.Proved.Seq
		fmt.Fprintf(stderr, shapesStopsAt, failure.Seq, err, limit)
		code = 1
	default:
		fmt.Fprintf(stderr, shapesNoVerify, err)
		return 1
	}
	if unread {
		code = 1
	}
	rep := shapesReport{Types: map[string][]shapeCount{}}
	at := map[string]int{}
	for _, r := range records {
		if r.seq > limit {
			break
		}
		key := r.typ + " " + r.digest
		i, ok := at[key]
		if !ok {
			i = len(rep.Types[r.typ])
			at[key] = i
			rep.Types[r.typ] = append(rep.Types[r.typ], shapeCount{Shape: r.digest, FirstSeq: r.seq, Members: shapes[r.digest], Door: door[key]})
		}
		c := &rep.Types[r.typ][i]
		c.Records++
		c.LastSeq = r.seq
		rep.Records++
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		fmt.Fprintf(stderr, "ledger-shapes: %v\n", err)
		return 1
	}
	return code
}
