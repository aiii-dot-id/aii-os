package pluginhost

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/speecheval"
)

type SessionEvent struct {
	Type         string
	Text         string
	Withheld     bool
	SynthesisID  string
	Reason       string
	ReasonCode   string
	RetryMayPass bool
}

type SessionRefusal struct {
	Control string
	Words   string
}

type SessionAnswer struct {
	Events      []SessionEvent
	Complete    bool
	Refused     *SessionRefusal
	SynthesisID string
	Samples     int64
	Rate        int
	Level       float64
	Took        time.Duration
}

func judgeSession(e *packagefmt.EventsExpect, speaksText bool, a SessionAnswer) (pluginfacility.CheckOutcome, string) {
	if a.Refused != nil {
		return pluginfacility.CheckWrong, fmt.Sprintf("the engine refused %s: %s", a.Refused.Control, excerptText(a.Refused.Words))
	}
	for _, ev := range a.Events {
		cause := ""
		if ev.Reason != "" {
			cause = ": " + excerptText(ev.Reason)
		}
		if ev.ReasonCode != "" {
			cause += " (" + ev.ReasonCode + ")"
		}
		switch {
		case ev.Type == EventFailure && ev.RetryMayPass:
			return pluginfacility.CheckTimedOut, "the engine failed during the check and says another try may pass" + cause
		case ev.Type == EventFailure:
			return pluginfacility.CheckWrong, "the engine failed during the check" + cause
		case (ev.Type == EventSynthesisCancelled || ev.Type == EventCancellation) && ev.SynthesisID != "" && ev.SynthesisID == a.SynthesisID:
			return pluginfacility.CheckWrong, "the engine cancelled the check's own synthesis" + cause
		case terminal(ev.Type, ev.SynthesisID):
			return pluginfacility.CheckWrong, "the engine ended the session before its answer was complete" + cause
		}
	}
	if !a.Complete {
		if speaksText {
			return pluginfacility.CheckTimedOut, "no complete answer within its bound: its reply never ended (synthesis_end and its stream's end)"
		}
		return pluginfacility.CheckTimedOut, "no complete answer within its bound: the engine never said input_finished"
	}
	if e == nil {
		return pluginfacility.CheckWrong, "the check expects nothing a session answers"
	}
	if words := orderHolds(e.Order, a.Events); words != "" {
		return pluginfacility.CheckWrong, words
	}
	if words := transcriptHolds(e.TranscriptContains, a.Events); words != "" {
		return pluginfacility.CheckWrong, words
	}
	if f := e.AudioAtLeast; f != nil {
		switch {
		case a.Rate <= 0 || a.Samples*1000 < f.MS*int64(a.Rate):
			ms := int64(0)
			if a.Rate > 0 {
				ms = a.Samples * 1000 / int64(a.Rate)
			}
			return pluginfacility.CheckWrong, fmt.Sprintf("its reply was %d ms long, under the %d ms it asks", ms, f.MS)
		case math.IsInf(a.Level, -1):
			return pluginfacility.CheckWrong, fmt.Sprintf("its reply was silent, under the %g dBFS it asks", f.RMSDBFS)
		case !(a.Level >= f.RMSDBFS):
			return pluginfacility.CheckWrong, fmt.Sprintf("its reply's level was %.1f dBFS, under the %g dBFS it asks", a.Level, f.RMSDBFS)
		}
	}
	return pluginfacility.CheckPassed, ""
}

func orderHolds(order []string, events []SessionEvent) string {
	k := 0
	var said []string
	for _, ev := range events {
		if ev.Withheld {
			continue
		}
		said = append(said, ev.Type)
		if k < len(order) && ev.Type == order[k] {
			k++
		}
	}
	if k == len(order) {
		return ""
	}
	return fmt.Sprintf("its events lack %q in the order it asks (%s); the engine said: %s", order[k], strings.Join(order, ", "), excerptText(strings.Join(said, ", ")))
}

func transcriptHolds(phrases []string, events []SessionEvent) string {
	if len(phrases) == 0 {
		return ""
	}
	var texts []string
	withheld := 0
	for _, ev := range events {
		if ev.Type != EventTranscriptFinal {
			continue
		}
		if ev.Withheld {
			withheld++
			continue
		}
		texts = append(texts, ev.Text)
	}
	heard := speecheval.Normalize(strings.Join(texts, " "))
	for _, p := range phrases {
		if containsRun(heard, speecheval.Normalize(p)) {
			continue
		}
		words := fmt.Sprintf("its transcript lacks %q: %q", p, excerptText(strings.Join(heard, " ")))
		switch {
		case withheld == 1:
			words += "; 1 final transcript was withheld because it did not decode"
		case withheld > 1:
			words += fmt.Sprintf("; %d final transcripts were withheld because they did not decode", withheld)
		}
		return words
	}
	return ""
}

func containsRun(heard, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(heard); i++ {
		match := true
		for j := range want {
			if heard[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
