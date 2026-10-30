package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

type transcript struct {
	rows   []interaction.Record
	events []dashboard.ServerMessage
	turns  []*interaction.Location
	said   []string
}

// subagentArgs is the mark a sub-agent's tool event carries before its
// arguments: the actor in brackets (internal/app/adapters.go).
var subagentArgs = regexp.MustCompile(`^\[[^\]]*\] `)

// state assembles the graded state from the transcript, the ledger the
// operator's reader printed, and the home's files.
func (tr *transcript) state(ledgerJSON []byte, slot string, setup map[string]string) (*promptsuite.State, error) {
	s := &promptsuite.State{Files: map[string]string{}, SetupFiles: setup}
	turnOf := map[string]int{}    // turn id -> task turn
	opRow := map[string]int{}     // operator row id -> task turn
	replyTurn := map[string]int{} // resident row id -> task turn
	callAt := map[string]int{}    // tool execution id -> index in s.Calls
	for i, ref := range tr.turns {
		opRow[ref.ID] = i + 1
		s.Turns = append(s.Turns, promptsuite.Turn{Said: tr.said[i]})
	}
	events := map[string][]string{} // tool name -> its events' arguments, in order
	for _, e := range tr.events {
		events[e.Name] = append(events[e.Name], subagentArgs.ReplaceAllString(e.Args, ""))
	}
	current := 0 // the task turn the operator last spoke, 0 before the first
	for _, r := range tr.rows {
		if n, ok := opRow[r.ID]; ok {
			current = n
			if r.TurnID != "" {
				turnOf[r.TurnID] = n
			}
			continue
		}
		if current == 0 {
			continue // birth and setup: not graded, apart from what they left behind
		}
		turn, direct := current, false
		if n, ok := turnOf[r.TurnID]; ok && r.TurnID != "" {
			turn, direct = n, true
		}
		switch r.Kind {
		case interaction.Message:
			if r.Role == interaction.Resident && direct {
				t := &s.Turns[turn-1]
				if t.Reply != "" {
					t.Reply += "\n\n"
				}
				t.Reply += r.Content
				replyTurn[r.ID] = turn
			}
		case interaction.ToolCall:
			c := promptsuite.Call{Turn: turn, Direct: direct, Actor: actorOf(r.Details.Actor), Tool: r.Details.Tool, Outcome: "unknown"}
			args := ""
			if q := events[c.Tool]; len(q) > 0 {
				args, events[c.Tool] = q[0], q[1:]
			} else {
				// No live event was heard for it: the transcript's own
				// excerpt, which is the arguments whole when they are short.
				args = strings.TrimSuffix(strings.TrimPrefix(r.Content, "→ "+c.Tool+"("), ")")
			}
			c.ArgsText = args
			var decoded map[string]any
			if json.Unmarshal([]byte(args), &decoded) == nil {
				c.Args = decoded
			}
			// A recorded call and its result share their execution's
			// source; a SAFE identity's, held in the process, carry none,
			// and its result names the call's row instead.
			if r.Source != nil {
				callAt[r.Source.ID] = len(s.Calls)
			}
			callAt[r.ID] = len(s.Calls)
			s.Calls = append(s.Calls, c)
		case interaction.ToolResult:
			i, ok := -1, false
			if r.Source != nil {
				i, ok = callAt[r.Source.ID]
			}
			if !ok {
				i, ok = callAt[r.RelatedID]
			}
			if !ok {
				continue
			}
			c := &s.Calls[i]
			c.Outcome, c.Reason = string(r.Outcome), r.Details.ReasonCode
			if _, result, found := strings.Cut(r.Content, "\n← "); found {
				c.Result = result
			}
		case interaction.Notice:
			if r.Details.ReasonCode == "loop_note" {
				if n, ok := replyTurn[r.RelatedID]; ok {
					s.Turns[n-1].Notes = append(s.Turns[n-1].Notes, r.Content)
				}
			}
			if w := r.Details.Work; w != nil && w.Result != nil {
				s.Deliveries = append(s.Deliveries, promptsuite.Delivery{Turn: turn, Actor: deliveryActor(r.Details.Actor), Session: w.Session, Result: *w.Result})
			}
		}
	}
	if err := readLedger(s, ledgerJSON); err != nil {
		return nil, err
	}
	if err := readHome(s, slot); err != nil {
		return nil, err
	}
	return s, nil
}

func actorOf(a string) string {
	switch a {
	case "main", "safe", "":
		return promptsuite.ActorResident
	case "operator":
		return "operator"
	}
	return promptsuite.ActorSubagent
}

func deliveryActor(a string) string {
	if a == "subagent" {
		return promptsuite.ActorSubagent
	}
	return promptsuite.ActorResident
}

// readLedger reads the records `aii ledger show -json` printed, one per
// line, as the ledger file holds them. A line that is not a record (the
// one a SAFE task appends) is skipped: it is no record of the identity's.
func readLedger(s *promptsuite.State, out []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e ledger.Event
		if json.Unmarshal(line, &e) != nil || e.Seq == 0 {
			continue
		}
		s.Ledger = append(s.Ledger, promptsuite.Record{Seq: e.Seq, Type: string(e.Type), Payload: string(e.Payload)})
		if e.Type == ledger.EventRing0Genesis && s.BirthName == "" {
			var birth struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(e.Payload, &birth) == nil {
				s.BirthName = birth.Name
			}
		}
	}
	return sc.Err()
}

var hostDirs = map[string]bool{"data": true, "config": true, "log": true, "plugins": true}

// maxHomeFile bounds one file a grader reads: the tasks' files are a few
// lines, and a large file the identity wrote is read to this bound.
const maxHomeFile = 1 << 20

func readHome(s *promptsuite.State, slot string) error {
	err := filepath.WalkDir(slot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(slot, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && hostDirs[strings.SplitN(rel, "/", 2)[0]] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		blob, err := io.ReadAll(io.LimitReader(f, maxHomeFile))
		if err != nil {
			return err
		}
		s.Files[rel] = string(blob)
		return nil
	})
	if err != nil {
		return err
	}
	if blob, err := os.ReadFile(filepath.Join(slot, "data", "ui", "name")); err == nil {
		s.Name = strings.TrimSpace(strings.SplitN(string(blob), "\n", 2)[0])
	}
	return nil
}
