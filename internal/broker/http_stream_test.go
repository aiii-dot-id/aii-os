package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type chunkReply struct {
	StreamID   string `json:"stream_id"`
	Seq        int    `json:"seq"`
	DataB64    string `json:"data_b64"`
	Bytes      int    `json:"bytes"`
	Done       bool   `json:"done"`
	TotalBytes int    `json:"total_bytes"`
}

func openStream(t *testing.T, b *Binding, op, rawURL, extra string) (string, map[string]json.RawMessage) {
	t.Helper()
	args := `{"stream":true`
	if extra != "" {
		args += "," + extra
	}
	args += "}"
	m := dispatch(t, b, verbParams(op, rawURL, args))
	var or struct {
		StreamID string `json:"stream_id"`
		Status   int    `json:"http_status"`
		ChunkMax int    `json:"chunk_max_bytes"`
	}
	_ = json.Unmarshal(m["operation_result"], &or)
	return or.StreamID, m
}

func readChunk(t *testing.T, b *Binding, id string, extra string) (chunkReply, map[string]json.RawMessage) {
	t.Helper()
	args := "{}"
	if extra != "" {
		args = extra
	}
	m := dispatch(t, b, fmt.Sprintf(`{"operation":"http.read","target":{"stream_id":%q},"arguments":%s}`, id, args))
	var c chunkReply
	_ = json.Unmarshal(m["operation_result"], &c)
	return c, m
}

// A streamed reply arrives as numbered chunks, in order, each what the
// connection had; the head and the chunks carry no receipt; the
// terminal chunk carries the exchange's one receipt with its totals.
func TestAStreamedReplyIsNumberedChunksWithOneReceipt(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "data: event %d\n\n", i)
			f.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})

	id, head := openStream(t, b, "http.get", ts.URL+"/events", "")
	if id == "" || string(head["external_receipt"]) != "null" || !strings.Contains(string(head["operation_result"]), `"content_type":"text/event-stream"`) {
		t.Fatalf("the head names the stream and carries no receipt: %s", head["operation_result"])
	}
	var text string
	var seqs []int
	for i := 0; i < 20; i++ {
		c, m := readChunk(t, b, id, "")
		data, _ := base64.StdEncoding.DecodeString(c.DataB64)
		text += string(data)
		if c.Bytes > 0 {
			seqs = append(seqs, c.Seq)
		}
		if c.Done {
			if string(m["external_receipt"]) == "null" {
				t.Fatal("the terminal chunk carries the receipt")
			}
			rec := wantResult(t, m, statusSucceeded, "")
			if receiptField(t, rec, "effect") != EffectPerformed || !strings.Contains(receiptField(t, rec, "detail"), "complete") {
				t.Fatalf("the receipt is the exchange's outcome: %s", rec["detail"])
			}
			if c.TotalBytes != len(text) {
				t.Fatalf("total %d vs read %d", c.TotalBytes, len(text))
			}
			break
		}
		if string(m["external_receipt"]) != "null" {
			t.Fatal("a chunk before the end carries no receipt")
		}
	}
	if text != "data: event 1\n\ndata: event 2\n\ndata: event 3\n\n" {
		t.Fatalf("the chunks reassemble the body: %q", text)
	}
	for i, s := range seqs {
		if s != i+1 {
			t.Fatalf("chunks are numbered in order: %v", seqs)
		}
	}
	recs, _ := st.PluginReceipts("p")
	if len(recs) != 1 {
		t.Fatalf("one receipt per exchange, got %d", len(recs))
	}
	if _, m := readChunk(t, b, id, ""); !strings.Contains(string(m["message"]), "no open stream") {
		t.Fatalf("a finished stream is gone: %s", m["message"])
	}
}

// The bounds hold: a chunk over the ceiling is refused; a stream over
// its total ceiling ends with NET_RESPONSE_TOO_LARGE and its receipt; a
// connection held open past the timeout ends the exchange, receipted;
// the plugin's close is receipted; a fifth open stream is refused; a
// stream id from another activation is unknown.
func TestStreamBoundsTimeoutsAndCloseAreTerminalAndReceipted(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		switch r.URL.Path {
		case "/big":
			for i := 0; i < 8; i++ {
				fmt.Fprint(w, strings.Repeat("x", 1024))
				f.Flush()
			}
		case "/slow":
			fmt.Fprint(w, "first")
			f.Flush()
			time.Sleep(1500 * time.Millisecond)
			fmt.Fprint(w, "late")
		default:
			fmt.Fprint(w, "hello")
			f.Flush()
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, " world")
		}
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}, "q": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport, MaxStreamBytes: 4096})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})

	id, _ := openStream(t, b, "http.get", ts.URL+"/big", "")
	if _, m := readChunk(t, b, id, `{"max_bytes":1000000}`); !strings.Contains(string(m["message"]), "chunk ceiling") {
		t.Fatalf("a chunk over the ceiling is refused: %s", m["message"])
	}
	var last chunkReply
	var lastReply map[string]json.RawMessage
	for i := 0; i < 20; i++ {
		last, lastReply = readChunk(t, b, id, `{"max_bytes":1024}`)
		if last.Done {
			break
		}
	}
	rec := wantResult(t, lastReply, statusFailed, reasonNetResponseTooBig)
	if !strings.Contains(receiptField(t, rec, "detail"), "exceeds the 4096-byte ceiling") || !last.Done {
		t.Fatalf("over the total ceiling is terminal and receipted: %+v %s", last, rec["detail"])
	}

	id, _ = openStream(t, b, "http.get", ts.URL+"/slow", `"timeout_ms":300`)
	c, _ := readChunk(t, b, id, "")
	if string(mustB64(t, c.DataB64)) != "first" {
		t.Fatalf("first chunk: %+v", c)
	}
	c, m := readChunk(t, b, id, "")
	rec = wantResult(t, m, statusFailed, reasonNetRemoteFailed)
	if !c.Done || !strings.Contains(receiptField(t, rec, "detail"), "ended after 5 bytes") {
		t.Fatalf("a connection held past the timeout ends the exchange with its receipt: %+v %s", c, rec["detail"])
	}

	id, _ = openStream(t, b, "http.get", ts.URL+"/hello", "")
	c, _ = readChunk(t, b, id, "")
	if string(mustB64(t, c.DataB64)) != "hello" {
		t.Fatalf("hello: %+v", c)
	}
	m = dispatch(t, b, fmt.Sprintf(`{"operation":"http.close","target":{"stream_id":%q}}`, id))
	rec = wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(receiptField(t, rec, "detail"), "closed by the plugin after 5 bytes in 1 chunks") {
		t.Fatalf("the plugin's close is the outcome: %s", rec["detail"])
	}
	if _, m := readChunk(t, b, id, ""); !strings.Contains(string(m["message"]), "no open stream") {
		t.Fatalf("closed is gone: %s", m["message"])
	}

	var ids []string
	for i := 0; i < MaxOpenStreams; i++ {
		id, _ := openStream(t, b, "http.get", ts.URL+"/hello", "")
		ids = append(ids, id)
	}
	_, m = openStream(t, b, "http.get", ts.URL+"/hello", "")
	wantResult(t, m, statusDenied, reasonNetStreamLimit)
	other := h.Bind("q", packagefmt.TierT1, []string{"net.outbound:" + hostPort})
	if _, m := readChunk(t, other, ids[0], ""); !strings.Contains(string(m["message"]), "no open stream") {
		t.Fatalf("a stream belongs to the activation that opened it: %s", m["message"])
	}
	b.EndOperation()
	if _, m := readChunk(t, b, ids[0], ""); !strings.Contains(string(m["message"]), "no open stream") {
		t.Fatal("the operation's end closes its streams")
	}
	recs, _ := st.PluginReceipts("p")
	var ended int
	for _, r := range recs {
		if strings.Contains(string(r.ReceiptJSON), "closed at the operation's end") {
			ended++
		}
	}
	if ended != MaxOpenStreams {
		t.Fatalf("each stream the operation's end closed is receipted: %d", ended)
	}
}

// A mutation streams too; an error response is answered whole, never
// as a stream; a stream is refused under SAFE like every effect.
func TestAMutationStreamsAndAnErrorIsNeverAStream(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nope" {
			http.Error(w, `{"error":"no"}`, http.StatusForbidden)
			return
		}
		fmt.Fprint(w, "posted:", r.Method)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	safe := false
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})
	id, _ := openStream(t, b, "http.post", ts.URL+"/x", `"body":"{}","content_type":"application/json"`)
	c, _ := readChunk(t, b, id, "")
	if string(mustB64(t, c.DataB64)) != "posted:POST" {
		t.Fatalf("a mutation streams: %+v", c)
	}
	m := dispatch(t, b, verbParams("http.get", ts.URL+"/nope", `{"stream":true}`))
	rec := wantResult(t, m, statusFailed, reasonNetRemoteFailed)
	if strings.Contains(string(m["operation_result"]), "stream_id") || receiptField(t, rec, "effect") != EffectPerformed {
		t.Fatalf("an error response is whole: %s", m["operation_result"])
	}
	safe = true
	reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(fmt.Sprintf(`{"operation":"http.read","target":{"stream_id":%q}}`, id)))
	if !strings.Contains(string(reply), "SAFE") {
		t.Fatalf("a chunk asks SAFE like every effect: %s", reply)
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A STREAM OVER THE CEILING IS REFUSED BEFORE ANYTHING IS SENT. The ceiling
// on open streams was looked at when the response had come back: the call
// was sent, its effect performed at the remote, and then refused. A plugin
// that did as the refusal said, closed one and asked again, made a
// mutation twice.
//
// Here every request the remote receives is counted. With the ceiling's
// worth of streams open, another streamed POST is denied, the refusal says
// nothing was sent, and the remote has received none. A request that is
// still on its way holds its place too: with one place left, a second
// request made while the first waits for its response is denied, and the
// first then opens. A place held by a request that fails, or that is
// answered with an error and so opens no stream, is given back.
func TestAStreamOverTheCeilingIsRefusedBeforeAnythingIsSent(t *testing.T) {
	var mu sync.Mutex
	received := map[string]int{}
	arrived := make(chan struct{}, 8)
	answer := make(chan struct{})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/held":
			arrived <- struct{}{}
			<-answer
		case "/nope":
			http.Error(w, `{"error":"no"}`, http.StatusForbidden)
			return
		case "/drop":
			// The connection ends with no response at all.
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer ts.Close()
	// The held request is let go however the test ends, or the server
	// would wait for it when it closes.
	var once sync.Once
	letGo := func() { once.Do(func() { close(answer) }) }
	defer letGo()
	got := func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return received[path]
	}
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})
	const post = `"body":"{}","content_type":"application/json"`

	// A place held by an exchange that opens no stream is given back: an
	// error response answered whole, and an exchange that fails on the
	// wire, each more times than there are places.
	for i := 0; i < MaxOpenStreams+2; i++ {
		m := dispatch(t, b, verbParams("http.get", ts.URL+"/nope", `{"stream":true}`))
		wantResult(t, m, statusFailed, reasonNetRemoteFailed)
		m = dispatch(t, b, verbParams("http.post", ts.URL+"/drop", `{"stream":true,`+post+`}`))
		var status string
		if _ = json.Unmarshal(m["status"], &status); status != statusFailed {
			t.Fatalf("rig: an exchange dropped on the wire fails, got status %q: %s", status, m["message"])
		}
	}
	var ids []string
	for i := 0; i < MaxOpenStreams-1; i++ {
		id, m := openStream(t, b, "http.post", ts.URL+"/open", post)
		if id == "" {
			t.Fatalf("stream %d of %d did not open after %d exchanges that opened none: %s", i+1, MaxOpenStreams, MaxOpenStreams+2, m["message"])
		}
		ids = append(ids, id)
	}

	// One place left. A request on its way holds it.
	first := make(chan map[string]json.RawMessage, 1)
	go func() {
		_, m := openStream(t, b, "http.post", ts.URL+"/held", post)
		first <- m
	}()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("rig: the held request never reached the remote")
	}
	_, m := openStream(t, b, "http.post", ts.URL+"/second", post)
	rec := wantResult(t, m, statusDenied, reasonNetStreamLimit)
	if detail := receiptField(t, rec, "detail"); !strings.Contains(detail, "Nothing was sent") || !strings.Contains(detail, fmt.Sprintf("%d streams are open or being opened", MaxOpenStreams)) {
		t.Fatalf("the refusal does not say how many places are taken and that nothing was sent: %s", detail)
	}
	if got("/second") != 0 {
		t.Fatalf("A STREAMED POST OVER THE CEILING WAS SENT %d TIME(S) AND THEN REFUSED", got("/second"))
	}
	letGo()
	var opened struct {
		StreamID string `json:"stream_id"`
	}
	select {
	case m := <-first:
		_ = json.Unmarshal(m["operation_result"], &opened)
	case <-time.After(10 * time.Second):
		t.Fatal("the held request never returned")
	}
	if opened.StreamID == "" {
		t.Fatal("the request that held the last place did not open its stream")
	}

	// Full. The next is denied unsent; with one closed, it opens.
	_, m = openStream(t, b, "http.post", ts.URL+"/third", post)
	wantResult(t, m, statusDenied, reasonNetStreamLimit)
	if got("/third") != 0 {
		t.Fatalf("a streamed POST over the ceiling reached the remote %d time(s)", got("/third"))
	}
	wantResult(t, dispatch(t, b, fmt.Sprintf(`{"operation":"http.close","target":{"stream_id":%q}}`, ids[0])), statusSucceeded, "")
	if id, m := openStream(t, b, "http.post", ts.URL+"/third", post); id == "" || got("/third") != 1 {
		t.Fatalf("with a stream closed the next must open, sent once: id %q, sent %d, %s", id, got("/third"), m["message"])
	}
}
