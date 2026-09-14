package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type reviewBlockedDispatcher struct {
	started chan struct{}
	release chan struct{}
}

func (d *reviewBlockedDispatcher) Dispatch(context.Context, string, []byte) ([]byte, error) {
	d.started <- struct{}{}
	<-d.release
	return []byte(`{}`), nil
}

func TestReviewUpstreamSaturationMustNotBlockReplies(t *testing.T) {
	frames := make(chan []byte, 32)
	d := &reviewBlockedDispatcher{make(chan struct{}, 16), make(chan struct{})}
	c := NewSessionClientFrames(frames, &bytes.Buffer{}, d, 4)
	reply := make(chan hostReply, 1)
	c.pending[999] = reply
	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background()) }()
	for i := 1; i <= 16; i++ {
		frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"invoke.call","params":{"operation":"kv.get","arguments":{}}}`, i))
	}
	for i := 0; i < 16; i++ {
		select {
		case <-d.started:
		case <-time.After(time.Second):
			t.Fatal("dispatch did not start")
		}
	}
	frames <- []byte(`{"jsonrpc":"2.0","id":17,"method":"invoke.call","params":{}}`)
	frames <- []byte(`{"jsonrpc":"2.0","id":999,"result":{"accepted":true}}`)
	blocked := false
	select {
	case <-reply:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	close(d.release)
	close(frames)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup failed")
	}
	if blocked {
		t.Fatal("17th upstream request blocked the control reader behind the 16 dispatch slots")
	}
}

func TestReviewControlDeadlineMustBoundBlockedWrite(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	c := NewSessionClient(nil, w, nil, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Control(ctx, "speech.session.synthesize", map[string]string{"text": strings.Repeat("x", 256*1024)})
		done <- err
	}()
	blocked := false
	select {
	case <-done:
	case <-time.After(180 * time.Millisecond):
		blocked = true
	}
	r.Close()
	if blocked {
		<-done
		t.Fatal("Control ignored its deadline while writing to an unread pipe")
	}
}

type reviewIdleReader struct {
	entered chan struct{}
	release chan struct{}
}

func (r *reviewIdleReader) Read([]byte) (int, error) { close(r.entered); <-r.release; return 0, io.EOF }

func TestReviewRunContextMustStopIdleReader(t *testing.T) {
	r := &reviewIdleReader{make(chan struct{}), make(chan struct{})}
	c := NewSessionClient(r, &bytes.Buffer{}, nil, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	<-r.entered
	cancel()
	blocked := false
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	close(r.release)
	if blocked {
		<-done
		t.Fatal("Run cancellation did not release an idle reader")
	}
}

func TestReviewFinalTranscriptMustNotBeShed(t *testing.T) {
	c := NewSessionClient(nil, &bytes.Buffer{}, nil, 1)
	c.observe([]byte(`{"method":"session.event","params":{"type":"vad_probability","sequence":1}}`))
	c.observe([]byte(`{"method":"session.event","params":{"type":"transcript_final","sequence":2,"text":"keep my opening words"}}`))
	var found bool
	for len(c.events) > 0 {
		var event struct {
			Type string `json:"type"`
		}
		json.Unmarshal(<-c.events, &event)
		found = found || event.Type == "transcript_final"
	}
	if !found {
		t.Fatalf("final transcript was lost; Dropped=%d does not reconstruct its words", c.Dropped())
	}
}
