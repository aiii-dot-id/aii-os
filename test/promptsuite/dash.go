package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// frameLimit bounds one server frame: the interaction pages and the
// connect-time frames are the largest a dashboard sends, and the
// library's 32 KiB default would end the socket on the first long page.
const frameLimit = 64 << 20

// dash is one socket to a trial's dashboard, signed in as the page signs
// in. One goroutine reads every frame into log; the trial's goroutine
// sends and waits on what arrived. The log is the trial's record of
// what the dashboard said, tool events among it.
type dash struct {
	conn    *websocket.Conn
	origin  string
	client  *http.Client
	pageKey string

	mu      sync.Mutex
	log     []dashboard.ServerMessage
	arrived chan struct{} // closed and replaced at each frame
	readErr error
	done    chan struct{}
	next    int // request ids
}

// connect signs in with the slot's own token (POST /auth/token answers
// the page key and sets the cookie), opens the socket with the page's
// Origin and interaction protocol version, and sends the page key as its
// first message. The token is sent to the slot's own address in that one
// request body and nowhere else.
func connect(ctx context.Context, origin, token string) (*dash, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar, Timeout: 60 * time.Second}
	key := ""
	if token != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/auth/token", strings.NewReader(token))
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sign in to the trial's dashboard: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return nil, fmt.Errorf("the trial's dashboard refused its own token (HTTP %d)", resp.StatusCode)
		}
		key = resp.Header.Get("Aii-Page-Key")
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(origin, "http")+"/ws?interaction_version=1", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Origin": []string{origin}},
	})
	if err != nil {
		return nil, fmt.Errorf("open the dashboard socket: %w", err)
	}
	conn.SetReadLimit(frameLimit)
	d := &dash{conn: conn, origin: origin, client: client, pageKey: key, arrived: make(chan struct{}), done: make(chan struct{})}
	hello, _ := json.Marshal(map[string]string{"type": "hello", "page_key": key})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("send the dashboard its page key: %w", err)
	}
	go d.read()
	return d, nil
}

// read is the socket's one reader. It ends when the socket does; close
// ends it.
func (d *dash) read() {
	defer close(d.done)
	for {
		typ, b, err := d.conn.Read(context.Background())
		if err != nil {
			d.mu.Lock()
			d.readErr = err
			close(d.arrived)
			d.mu.Unlock()
			return
		}
		if typ != websocket.MessageText {
			continue // audio for a speaking page
		}
		var m dashboard.ServerMessage
		if err := json.Unmarshal(b, &m); err != nil {
			continue // a frame this runner cannot read is not one it waits for
		}
		d.mu.Lock()
		d.log = append(d.log, m)
		close(d.arrived)
		d.arrived = make(chan struct{})
		d.mu.Unlock()
	}
}

func (d *dash) close() {
	d.conn.Close(websocket.StatusNormalClosure, "")
	<-d.done
}

// mark is the position in the log a wait starts from.
func (d *dash) mark() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.log)
}

// frames is a copy of the log from position from.
func (d *dash) frames(from int) []dashboard.ServerMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]dashboard.ServerMessage(nil), d.log[from:]...)
}

// wait returns the first frame at or after from that match accepts.
func (d *dash) wait(ctx context.Context, from int, match func(dashboard.ServerMessage) bool) (dashboard.ServerMessage, int, error) {
	for {
		d.mu.Lock()
		for i := from; i < len(d.log); i++ {
			if match(d.log[i]) {
				m := d.log[i]
				d.mu.Unlock()
				return m, i, nil
			}
		}
		from = len(d.log)
		arrived, readErr := d.arrived, d.readErr
		d.mu.Unlock()
		if readErr != nil {
			return dashboard.ServerMessage{}, from, fmt.Errorf("the dashboard socket ended: %w", readErr)
		}
		select {
		case <-ctx.Done():
			return dashboard.ServerMessage{}, from, ctx.Err()
		case <-arrived:
		}
	}
}

func (d *dash) send(ctx context.Context, m dashboard.ClientMessage) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return d.conn.Write(ctx, websocket.MessageText, b)
}

func (d *dash) requestID(kind string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.next++
	return "promptsuite-" + kind + "-" + strconv.Itoa(d.next)
}

// query sends a named query and waits for the frame of the type it is
// answered with.
func (d *dash) query(ctx context.Context, name, answer string) (dashboard.ServerMessage, error) {
	from := d.mark()
	if err := d.send(ctx, dashboard.ClientMessage{Type: "query", Query: name}); err != nil {
		return dashboard.ServerMessage{}, err
	}
	m, _, err := d.wait(ctx, from, func(m dashboard.ServerMessage) bool { return m.Type == answer })
	return m, err
}

// discover asks for one registry entry's models, as the birth form does
// when the entry is chosen, and waits for the answer or the refusal
// (either is an answer: a refusal is the entry's, not the socket's).
// baseURL is the address typed for a local entry.
func (d *dash) discover(ctx context.Context, entry, baseURL string) error {
	id := d.requestID("discover")
	from := d.mark()
	if err := d.send(ctx, dashboard.ClientMessage{Type: "query", Query: "discover", RequestID: id, Provider: entry, BaseURL: baseURL}); err != nil {
		return err
	}
	_, _, err := d.wait(ctx, from, func(m dashboard.ServerMessage) bool {
		return m.RequestID == id && (m.Type == "models" || m.Type == "error")
	})
	return err
}

// birth sends the first-boot form and waits for the founding greeting or
// the refusal. No key travels on the form: the entry it names finds its
// own credential, as the operator's stored entry does.
func (d *dash) birth(ctx context.Context, entry, endpoint, model string) (string, error) {
	from := d.mark()
	if err := d.send(ctx, dashboard.ClientMessage{Type: "genesis", Genesis: &dashboard.GenesisRequest{Provider: entry, Endpoint: endpoint, Model: model}}); err != nil {
		return "", err
	}
	m, _, err := d.wait(ctx, from, func(m dashboard.ServerMessage) bool {
		return m.Type == "error" || (m.Type == "response" && m.Role == "identity" && m.Done)
	})
	if err != nil {
		return "", err
	}
	if m.Type == "error" {
		return "", fmt.Errorf("the birth was refused: %s", m.Message)
	}
	return m.Message, nil
}

// errRefused is a chat the dashboard refused: nothing was recorded.
var errRefused = errors.New("the dashboard refused the operator's words")

func (d *dash) chat(ctx context.Context, words string) (*interaction.Location, error) {
	id := d.requestID("chat")
	from := d.mark()
	if err := d.send(ctx, dashboard.ClientMessage{Type: "chat", Message: words, RequestID: id}); err != nil {
		return nil, err
	}
	m, at, err := d.wait(ctx, from, func(m dashboard.ServerMessage) bool {
		return m.RequestID == id && (m.Type == "chat_receipt" || m.Type == "error" || (m.Type == "chat_ack" && m.Message == "refused"))
	})
	if err != nil {
		return nil, fmt.Errorf("wait for the words to be recorded: %w", err)
	}
	if m.Type != "chat_receipt" || m.ChatRecording == nil || m.ChatRecording.Error != nil || m.ChatRecording.Ref == nil {
		return nil, fmt.Errorf("%w: %s", errRefused, describe(m))
	}
	ref := m.ChatRecording.Ref
	if _, _, err := d.wait(ctx, at, func(m dashboard.ServerMessage) bool {
		return m.Type == "response" && m.Role == "identity" && m.Done
	}); err != nil {
		return ref, fmt.Errorf("wait for the reply: %w", err)
	}
	return ref, nil
}

func describe(m dashboard.ServerMessage) string {
	if m.ChatRecording != nil && m.ChatRecording.Error != nil {
		return m.ChatRecording.Error.Code + ": " + m.ChatRecording.Error.Message
	}
	return m.Type + " " + m.Message
}

// probe asks whether a turn runs, as a page back from the background
// does. A probe is not an operator act.
func (d *dash) probe(ctx context.Context) (bool, error) {
	id := d.requestID("probe")
	from := d.mark()
	if err := d.send(ctx, dashboard.ClientMessage{Type: "probe", RequestID: id}); err != nil {
		return false, err
	}
	m, _, err := d.wait(ctx, from, func(m dashboard.ServerMessage) bool { return m.Type == "probe" && m.RequestID == id })
	return m.TurnActive, err
}

// work is the work frame: sessions live, queued and delivered.
func (d *dash) work(ctx context.Context) (*dashboard.WorkState, error) {
	m, err := d.query(ctx, "work", "work")
	if err != nil {
		return nil, err
	}
	if m.Work == nil {
		return &dashboard.WorkState{}, nil
	}
	return m.Work, nil
}

func (d *dash) activePlugins(ctx context.Context) (map[string]bool, error) {
	m, err := d.query(ctx, "config", "config")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	if m.Config != nil {
		for _, p := range m.Config.Plugins.Installed {
			out[p.ID] = true
		}
	}
	return out, nil
}

// rest waits until the identity is at rest on settle consecutive looks a
// poll apart: no turn running, no work session live and nothing queued.
// A sub-agent's run and the wake that harvests it are part of the turn
// that spawned it, so rest waits for both.
func (d *dash) rest(ctx context.Context, poll time.Duration, settle int) error {
	quiet := 0
	for {
		active, err := d.probe(ctx)
		if err != nil {
			return fmt.Errorf("ask whether a turn runs: %w", err)
		}
		busy := active
		if !busy {
			w, err := d.work(ctx)
			if err != nil {
				return fmt.Errorf("read the work frame: %w", err)
			}
			busy = len(w.Live) > 0 || w.Queued > 0
		}
		if busy {
			quiet = 0
		} else if quiet++; quiet >= settle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (d *dash) interactions(ctx context.Context, source string) ([]interaction.Record, error) {
	var rows []interaction.Record
	cursor := ""
	for page := 0; ; page++ {
		if page > 10000 {
			return nil, fmt.Errorf("the transcript did not end after %d pages", page)
		}
		id := d.requestID("interaction")
		q := interaction.Query{Version: interaction.Version, Source: source, Cursor: cursor, Limit: interaction.MaxPageRows}
		from := d.mark()
		if err := d.send(ctx, dashboard.ClientMessage{Type: "query", Query: "interaction", RequestID: id, InteractionQuery: &q}); err != nil {
			return nil, err
		}
		m, _, err := d.wait(ctx, from, func(m dashboard.ServerMessage) bool {
			return m.RequestID == id && (m.Type == "interaction_page" || m.Type == "interaction_error")
		})
		if err != nil {
			return nil, err
		}
		if m.Type == "interaction_error" || m.InteractionPage == nil {
			return nil, fmt.Errorf("the transcript (%s) could not be read: %s %s", source, m.InteractionReason, m.Message)
		}
		for _, r := range m.InteractionPage.Rows {
			if r.ContentRef != nil {
				full, err := d.detail(ctx, r.ContentRef)
				if err != nil {
					return nil, err
				}
				r.Content = full
			}
			rows = append(rows, r)
		}
		if m.InteractionPage.NextCursor == "" {
			return rows, nil
		}
		cursor = m.InteractionPage.NextCursor
	}
}

// detail reads a row too long for a page through the dashboard's detail
// route, a part at a time.
func (d *dash) detail(ctx context.Context, ref *interaction.ContentRef) (string, error) {
	var out []byte
	for offset := int64(0); ; {
		q := url.Values{"source": {ref.Source}, "id": {ref.ID}, "incarnation": {ref.Incarnation}, "sha256": {ref.SHA256}, "offset": {strconv.FormatInt(offset, 10)}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.origin+"/interaction/detail?"+q.Encode(), nil)
		if err != nil {
			return "", err
		}
		if d.pageKey != "" {
			req.Header.Set("Aii-Page-Key", d.pageKey)
		}
		resp, err := d.client.Do(req)
		if err != nil {
			return "", err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("the detail of %s: HTTP %d %s", ref.ID, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var part interaction.ReadResult
		if err := json.Unmarshal(body, &part); err != nil {
			return "", err
		}
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			return "", err
		}
		out = append(out, data...)
		offset += int64(len(data))
		if part.EOF || len(data) == 0 {
			return string(out), nil
		}
	}
}

// toolEvents are the live tool events the socket heard, in order: each
// call's name and its arguments whole, sent as the call starts.
func (d *dash) toolEvents(from int) []dashboard.ServerMessage {
	var out []dashboard.ServerMessage
	for _, m := range d.frames(from) {
		if m.Type == "event" && m.Kind == "tool_call" {
			out = append(out, m)
		}
	}
	return out
}
