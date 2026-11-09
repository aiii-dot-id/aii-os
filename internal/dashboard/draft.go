package dashboard

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const (
	draftEvery        = 100 * time.Millisecond
	draftWithdrawnMax = 16 << 10
	draftKeptAfterEnd = 10 * time.Second
)

const draftTextMax = llm.MaxResponseBytes

const WithdrawnReplaced = "replaced"

type DraftView struct {
	Ref *interaction.Location `json:"ref,omitempty"`
	Seq int                   `json:"seq"`

	Call int    `json:"call,omitempty"`
	Add  string `json:"add,omitempty"`

	Snapshot bool   `json:"snapshot,omitempty"`
	Kept     string `json:"kept,omitempty"`

	Withdrawals []DraftWithdrawal `json:"withdrawals,omitempty"`

	Hidden int `json:"hidden,omitempty"`

	End string `json:"end,omitempty"`
}

type DraftWithdrawal struct {
	Call  int    `json:"call"`
	Words string `json:"words"`
	Why   string `json:"why"`
}

type replyDraft struct {
	s   *Server
	ctx context.Context

	mu          sync.Mutex
	seq         int
	call        int
	kept        strings.Builder
	current     strings.Builder
	hidden      int
	withdrawals []DraftWithdrawal
	wbytes      int
	added       strings.Builder
	owed        bool
	sent        DraftView
	end         string
	endedAt     time.Time

	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	finished sync.Once
}

func (d *replyDraft) observer() conversation.ReplyEmitter {
	if d == nil {
		return nil
	}
	return d
}

var _ conversation.ReplyEmitter = (*replyDraft)(nil)

func (s *Server) startDraft(ctx context.Context) *replyDraft {
	d := &replyDraft{s: s, ctx: ctx, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	s.draftMu.Lock()
	s.draft = d
	s.draftMu.Unlock()
	go d.publish()
	return d
}

func (d *replyDraft) ReplyText(call int, piece string) {
	d.mu.Lock()
	if d.end != "" {
		d.mu.Unlock()
		return
	}
	if call != d.call {
		d.call, d.owed = call, true
		d.current.Reset()
		d.added.Reset()
	}
	room := draftTextMax - d.kept.Len() - d.current.Len()
	kept := fit(piece, room)
	d.current.WriteString(kept)
	d.added.WriteString(kept)
	d.hidden += len(piece) - len(kept)
	d.mu.Unlock()
	d.poke()
}

func (d *replyDraft) ReplyEnded(call int, taken, withdrawn string) {
	d.mu.Lock()
	if d.end != "" {
		d.mu.Unlock()
		return
	}
	shown := ""
	if call == d.call {
		shown = d.current.String()
	}
	switch {
	case withdrawn != "":
		d.withdraw(call, shown, withdrawn)
	default:
		if shown != "" && shown != taken {
			d.withdraw(call, shown, WithdrawnReplaced)
		}
		if taken != "" {
			if d.kept.Len() > 0 {
				taken = "\n\n" + taken
			}
			k := fit(taken, draftTextMax-d.kept.Len())
			d.kept.WriteString(k)
			d.hidden += len(taken) - len(k)
		}
	}
	d.current.Reset()
	d.added.Reset()
	d.owed = true
	d.mu.Unlock()
	d.poke()
}

func (d *replyDraft) withdraw(call int, words, why string) {
	if words == "" {
		return
	}
	k := fit(words, draftWithdrawnMax-d.wbytes)
	d.wbytes += len(k)
	d.hidden += len(words) - len(k)
	d.withdrawals = append(d.withdrawals, DraftWithdrawal{Call: call, Words: k, Why: why})
}

func (d *replyDraft) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *replyDraft) publish() {
	defer close(d.done)
	var last time.Time
	for {
		select {
		case <-d.stop:
			return
		case <-d.wake:
		}
		if wait := draftEvery - time.Since(last); wait > 0 {
			select {
			case <-d.stop:
				return
			case <-time.After(wait):
			}
		}
		if msg, ok := d.next(); ok {
			d.s.sendDraft(msg)
		}
		last = time.Now()
	}
}

func (d *replyDraft) next() (ServerMessage, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.owed {
		d.owed = false
		d.added.Reset()
		return d.snapshotLocked(), true
	}
	if d.added.Len() == 0 {
		return ServerMessage{}, false
	}
	d.seq++
	v := DraftView{Ref: turnRefOf(d.ctx), Seq: d.seq, Call: d.call, Add: d.added.String(), Hidden: d.hidden}
	d.sent.Seq, d.sent.Add, d.sent.Hidden = d.seq, d.sent.Add+v.Add, d.hidden
	d.added.Reset()
	return ServerMessage{Type: "draft", Draft: &v}, true
}

func (d *replyDraft) snapshotLocked() ServerMessage {
	d.seq++
	v := DraftView{Ref: turnRefOf(d.ctx), Seq: d.seq, Snapshot: true, Call: d.call, Kept: d.kept.String(), Add: d.current.String(),
		Withdrawals: append([]DraftWithdrawal(nil), d.withdrawals...), Hidden: d.hidden, End: d.end}
	d.sent = v
	return ServerMessage{Type: "draft", Draft: &v}
}

func (d *replyDraft) finish() {
	if d == nil {
		return
	}
	d.finished.Do(d.end1)
}

func (d *replyDraft) end1() {
	close(d.stop)
	select {
	case <-d.done:
	case <-time.After(writeWait):
	}
	d.mu.Lock()
	shown := d.seq > 0 || d.owed || d.added.Len() > 0
	d.end = "withdrawn"
	if d.kept.Len() > 0 {
		d.end = "kept"
	}
	d.endedAt = time.Now()
	msg := d.snapshotLocked()
	d.mu.Unlock()
	if shown {
		d.s.sendDraft(msg)
	}
}

func (s *Server) sendDraft(msg ServerMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), writeWait)
	defer cancel()
	var wg sync.WaitGroup
	for _, conn := range s.conns(nil) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.fanOut(ctx, []carrier{conn}, msg)
		}()
	}
	wg.Wait()
}

func (s *Server) draftForConnect() (ServerMessage, bool) {
	s.draftMu.Lock()
	d := s.draft
	s.draftMu.Unlock()
	if d == nil {
		return ServerMessage{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seq == 0 || (d.end != "" && time.Since(d.endedAt) > draftKeptAfterEnd) {
		return ServerMessage{}, false
	}
	v := d.sent
	v.Withdrawals = append([]DraftWithdrawal(nil), d.sent.Withdrawals...)
	return ServerMessage{Type: "draft", Draft: &v}, true
}

func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
