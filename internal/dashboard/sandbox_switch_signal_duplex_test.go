package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"
)

type unbufferedPageSignal struct {
	ctx        context.Context
	fromPage   chan setupMessage
	toPage     chan setupMessage
	candidates atomic.Int32
}

func (s *unbufferedPageSignal) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case m := <-s.toPage:
		raw, err := json.Marshal(m)
		return websocket.MessageText, raw, err
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-s.ctx.Done():
		return 0, nil, s.ctx.Err()
	}
}

func (s *unbufferedPageSignal) Write(ctx context.Context, _ websocket.MessageType, raw []byte) error {
	var m setupMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.Type == "transport_candidate" {
		s.candidates.Add(1)
	}
	select {
	case s.fromPage <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func TestCarriedChannelReadsTheAnswerWhileWritingCandidates(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	signal := &unbufferedPageSignal{ctx: ctx, fromPage: make(chan setupMessage), toPage: make(chan setupMessage)}
	peer, err := newPeer(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	peer.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			_ = dc.Send(append([]byte{pieceLast}, []byte(`{"type":"transport_ready","carrier":"channel"}`)...))
		})
	})
	put := func(m setupMessage) bool {
		select {
		case signal.toPage <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}
	peer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		v := c.ToJSON()
		put(setupMessage{Type: "transport_candidate", Candidate: v.Candidate, SDPMid: v.SDPMid, SDPMLineIndex: v.SDPMLineIndex})
	})
	go func() {
		if !put(setupMessage{Type: "transport_setup", ReadyWithinMS: 8000}) {
			return
		}
		select {
		case offer := <-signal.fromPage:
			answer, err := answerOffer(peer, offer.SDP)
			if err != nil {
				put(setupMessage{Type: "transport_ready", Carrier: fmt.Sprint(err)})
				return
			}
			if !put(setupMessage{Type: "transport_answer", SDP: answer}) {
				return
			}
		case <-ctx.Done():
			return
		}
		for {
			select {
			case m := <-signal.fromPage:
				if m.Type == "transport_candidate" {
					_ = peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate, SDPMid: m.SDPMid, SDPMLineIndex: m.SDPMLineIndex})
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	channelPageSetup(t, signal, true)
	if signal.candidates.Load() == 0 {
		t.Fatal("the fixture did not send any gathered candidate and did not exercise the duplex boundary")
	}
}
