package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const ledgerUsage = `usage: aii ledger show [-dir D] [-from N] [-to N] [-type T] [-grep WORDS] [-json]

Prints the identity's record from its ledger file, the closed segments
included, with the identity running or stopped, and verifies the chain from
record 1 as it reads, whatever the filters print. It writes nothing.

Every row is shown whole: anything typed into a chat or a note, a password
included, is here as it was recorded. In text, a character that is not
printable (a control character, or a format or direction character such as
U+202E) is shown as \u{<hex>}, its code point, so that no row can rewrite
the terminal. With -json each row is its line exactly as the file holds it,
proof and hashes included.

Where the chain stops verifying, the rows from there on are still printed,
marked UNVERIFIED (with -json, unchanged), standard error says from which
seq and why, and the exit status is 1. A last line still being written is
not a record yet and is not printed. Exit 0: every row verified; 1: not,
or the ledger could not be read; 2: usage.

`

const (
	ledgerStopsAt       = "ledger show: the record stops verifying at seq %d: %v. "
	ledgerDoesNotVerify = "ledger show: the record does not verify: %v. "
	ledgerVerifiedUpTo  = "The rows through seq %d were verified; the rows from seq %d on are shown unverified.\n"
	ledgerNoneVerified  = "No row was verified; every row is shown unverified.\n"
	ledgerCutShort      = "ledger show: the file was being written, or a stop cut its last line short; shown through seq %d, its last complete line.\n"
	ledgerUnreadable    = "ledger show: %s cannot be read past line %d: %v\n"
	ledgerConfigUnread  = "ledger show: the config cannot be read (%v); reading the ledger at its default place, %s\n"

	ledgerUnverifiedMark = "  UNVERIFIED"
	ledgerNotARecord     = "UNVERIFIED  %s line %d is not a record; its bytes, quoted:\n%s\n\n"
)

func runLedger(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" {
		fmt.Fprint(stderr, ledgerUsage)
		return 2
	}
	return ledgerShow(args[1:], stdout, stderr, besideLedger)
}

func ledgerShow(args []string, stdout, stderr io.Writer, load genesis.BesideLoader) int {
	fset := flag.NewFlagSet("ledger show", flag.ContinueOnError)
	fset.SetOutput(stderr)
	dir := fset.String("dir", "", "Identity install directory")
	from := fset.Uint64("from", 0, "print the records from this seq on")
	to := fset.Uint64("to", 0, "print the records up to this seq")
	typ := fset.String("type", "", "print only the records of this type, e.g. experience.create")
	grep := fset.String("grep", "", "print only the records whose type or payload holds these words, ignoring case")
	asJSON := fset.Bool("json", false, "print each record's line exactly as the file holds it")
	fset.Usage = func() {
		fmt.Fprint(stderr, ledgerUsage)
		fset.PrintDefaults()
	}
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fset.NArg() != 0 {
		fmt.Fprintf(stderr, "ledger show: unexpected argument %q\n", fset.Arg(0))
		return 2
	}
	if *to != 0 && *from > *to {
		fmt.Fprintln(stderr, "ledger show: -from is after -to")
		return 2
	}

	path := ledgerPathIn(*dir, stderr)
	beside, err := load(filepath.Dir(path))
	if err != nil {

		fmt.Fprintf(stderr, "ledger show: %v\n", err)
		beside = nil
	}

	out := bufio.NewWriter(stdout)
	p := &ledgerPrinter{out: out, stderr: stderr, json: *asJSON, from: *from, to: *to, typ: *typ, grep: asciiLower([]byte(*grep))}
	_, _, cut, err := genesis.ReadHeld(path, beside, p.verified, p.unverified)
	p.flush(err != nil)
	if ferr := out.Flush(); p.err == nil {
		p.err = ferr
	}

	code := 0
	var failure *ledger.VerifyFailure
	var held *witness.TailRefusal
	switch {
	case p.err != nil:
		fmt.Fprintf(stderr, "ledger show: %v\n", p.err)
		return 1
	case err == nil:
	case errors.As(err, &failure):
		fmt.Fprint(stderr, stopsVerifying(failure, p.proved))
		code = 1
	case errors.As(err, &held):
		fmt.Fprint(stderr, "ledger show: "+genesis.RefusedReport(err, beside.Summary()))
		code = 1
	default:
		fmt.Fprintf(stderr, "ledger show: %v\n", err)
		return 1
	}
	if cut > 0 {
		fmt.Fprintf(stderr, ledgerCutShort, p.last)
	}
	return code
}

func stopsVerifying(f *ledger.VerifyFailure, proved uint64) string {
	at := f.Seq
	if at == 0 {
		at = f.NextSeq
	}
	var head string
	if at == 0 {
		head = fmt.Sprintf(ledgerDoesNotVerify, f)
	} else {
		head = fmt.Sprintf(ledgerStopsAt, at, f)
	}
	if proved == 0 {
		return head + ledgerNoneVerified
	}
	return head + fmt.Sprintf(ledgerVerifiedUpTo, proved, proved+1)
}

func ledgerPathIn(dir string, stderr io.Writer) string {
	fallback := filepath.Join(dir, "data", "ledger.jsonl")
	cfg, err := app.ReadConfig(install.ConfigPathIn(dir))
	switch {
	case err == nil && cfg.Identity.LedgerPath != "":
		if filepath.IsAbs(cfg.Identity.LedgerPath) {
			return cfg.Identity.LedgerPath
		}
		return filepath.Join(dir, cfg.Identity.LedgerPath)
	case err == nil || errors.Is(err, fs.ErrNotExist):
		return fallback
	default:
		fmt.Fprintf(stderr, ledgerConfigUnread, err, fallback)
		return fallback
	}
}

type ledgerPrinter struct {
	out    *bufio.Writer
	stderr io.Writer
	json   bool
	from   uint64
	to     uint64
	typ    string
	grep   []byte

	pending []ledger.Event
	proved  uint64
	last    uint64
	err     error
}

func (p *ledgerPrinter) wanted(seq uint64, typ string, payload []byte) bool {
	if seq < p.from || (p.to != 0 && seq > p.to) {
		return false
	}
	if p.typ != "" && typ != p.typ {
		return false
	}
	return p.holds([]byte(typ)) || p.holds(payload)
}

func (p *ledgerPrinter) holds(b []byte) bool {
	return len(p.grep) == 0 || bytes.Contains(asciiLower(b), p.grep)
}

func (p *ledgerPrinter) verified(evt *ledger.Event, line []byte) error {
	p.last = evt.Seq

	if evt.Sig != "" {
		p.proved = evt.Seq
		p.flush(false)
	}
	if !p.wanted(evt.Seq, string(evt.Type), evt.Payload) {
		return p.err
	}
	switch {
	case p.json:
		p.write(line)
		p.write([]byte{'\n'})
	case evt.Sig == "":
		p.pending = append(p.pending, *evt)
	default:
		p.row(evt, false)
	}
	return p.err
}

func (p *ledgerPrinter) unverified(container string, at int, line []byte, err error) error {
	p.flush(true)
	if err != nil {
		if ferr := p.out.Flush(); p.err == nil {
			p.err = ferr
		}
		fmt.Fprintf(p.stderr, ledgerUnreadable, container, at, err)
		return p.err
	}
	var evt ledger.Event
	if json.Unmarshal(line, &evt) != nil {

		if !p.holds(line) {
			return p.err
		}
		if p.json {
			p.write(line)
			p.write([]byte{'\n'})
		} else {
			p.write(fmt.Appendf(nil, ledgerNotARecord, container, at, strconv.Quote(string(line))))
		}
		return p.err
	}
	p.last = evt.Seq
	if !p.wanted(evt.Seq, string(evt.Type), evt.Payload) {
		return p.err
	}
	if p.json {
		p.write(line)
		p.write([]byte{'\n'})
	} else {
		p.row(&evt, true)
	}
	return p.err
}

func (p *ledgerPrinter) flush(unverified bool) {
	for i := range p.pending {
		p.row(&p.pending[i], unverified)
	}
	p.pending = p.pending[:0]
}

func (p *ledgerPrinter) row(evt *ledger.Event, unverified bool) {
	head := fmt.Appendf(nil, "#%d  %s  %s  ring %d", evt.Seq, terminalSafe([]byte(evt.Timestamp)), terminalSafe([]byte(evt.Type)), evt.Ring)
	if unverified {
		head = append(head, ledgerUnverifiedMark...)
	}
	p.write(append(head, '\n'))
	p.write(terminalSafe(evt.Payload))
	p.write([]byte("\n\n"))
}

func (p *ledgerPrinter) write(b []byte) {
	if p.err != nil {
		return
	}
	_, p.err = p.out.Write(b)
}

func terminalSafe(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		switch {
		case r == utf8.RuneError && size <= 1:
			out = fmt.Appendf(out, "\\x{%02X}", b[0])
		case r == ' ' || unicode.IsPrint(r):
			out = append(out, b[:size]...)
		default:
			out = fmt.Appendf(out, "\\u{%X}", r)
		}
		b = b[size:]
	}
	return out
}

func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}
