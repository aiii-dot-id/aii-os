package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// heldSent is a ring as the page reads one: the requests it holds, oldest
// first, and its numbers.
type heldSent struct {
	mu       sync.Mutex
	requests []llm.SentRequest
	kept     int
	maxBytes int
	over     bool
}

func (h *heldSent) List() llm.SentList {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := llm.SentList{Kept: h.kept, MaxBytes: h.maxBytes, Over: h.over}
	for i := len(h.requests) - 1; i >= 0; i-- {
		r := h.requests[i]
		r.Body = nil
		out.Requests = append(out.Requests, r)
	}
	return out
}

func (h *heldSent) Get(n uint64) (llm.SentRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.requests {
		if r.N == n {
			return r, true
		}
	}
	return llm.SentRequest{}, false
}

func (h *heldSent) set(kept int, over bool, bodies ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept, h.maxBytes, h.over, h.requests = kept, DefaultSentMaxBytes, over, nil
	for i, b := range bodies {
		h.requests = append(h.requests, llm.SentRequest{N: uint64(i + 1), CallID: "c" + strconv.Itoa(i+1), At: time.Date(2026, 10, 15, 1, 2, i, 0, time.UTC),
			Entry: "an-entry", Model: "a-model", Facility: map[bool]string{true: "dream"}[i%2 == 1], Turn: strconv.Itoa(40 + i), Bytes: len(b), Body: []byte(b)})
	}
}

func sentServer(t testing.TB, held *heldSent, token string) *Server {
	t.Helper()
	s := New("127.0.0.1", 0, &WSHandler{Sent: held, RecordSettings: func() RecordSettings { return RecordSettings{FirstViewBytes: 512} }})
	s.SetAccessToken(token != "", token)
	s.AllowHost("127.0.0.1:8180")
	return s
}

// WHAT THE MODEL IS SENT IS THE OPERATOR'S, WITH THE TOKEN ON AND OFF. Both
// routes refuse a request without the token when it is asked for, refuse
// another site, and answer no-store, no-referrer and nosniff.
func TestTheSentRoutesAnswerTheOperatorWithTheTokenOnAndOff(t *testing.T) {
	held := &heldSent{}
	held.set(20, false, `{"system":"s","messages":[]}`)
	for _, token := range []string{"", "a-token-of-honest-length-for-this-test-0123456789"} {
		s := sentServer(t, held, token)
		for _, path := range []string{"/sent", "/sent/body?id=1&part=body&head=1", "/sent/body?id=1&part=system&offset=0&length=9"} {
			name := fmt.Sprintf("%s (token on: %v)", path, token != "")
			check := func(w *httptest.ResponseRecorder, want int, why string) {
				t.Helper()
				if w.Code != want {
					t.Errorf("%s %s: answered %d %q, want %d", name, why, w.Code, w.Body.String(), want)
				}
				if h := w.Header(); h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
					t.Errorf("%s %s: headers %v", name, why, h)
				}
			}
			if token != "" {
				check(askRecord(s, path, false), http.StatusUnauthorized, "without the token")
			}
			check(askRecord(s, path, true), http.StatusOK, "from the page")
			check(askRecord(s, path, true, "Origin", "https://elsewhere.example"), http.StatusForbidden, "from another site's script")
			check(askRecord(s, path, true, "Sec-Fetch-Site", "cross-site"), http.StatusForbidden, "from a link on another site")
		}
	}
}

// THE LIST AND THE BODIES SAY WHAT THEY HOLD. Newest first, with the ring's
// numbers; a body's first view ends on a whole character and says the whole's
// size; ranges join to the body as kept; the system part is the system text
// whole; a call no longer kept is not found.
func TestTheSentListAndBodiesSayWhatTheyHold(t *testing.T) {
	long := `{"system":[{"type":"text","text":"Ring 0 😀 ` + strings.Repeat("界", 400) + `"}],"messages":[{"role":"user","content":"30.0"}]}`
	held := &heldSent{}
	held.set(20, true, `{"messages":[]}`, long)
	s := sentServer(t, held, "")
	w := askRecord(s, "/sent", false)
	var a sentAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || w.Code != http.StatusOK {
		t.Fatalf("/sent answered %d %q", w.Code, w.Body.String())
	}
	if a.Kept != 20 || a.MaxBytes != DefaultSentMaxBytes || !a.Over || a.FirstViewBytes != 512 || len(a.Requests) != 2 ||
		a.Requests[0].ID != "2" || a.Requests[0].Bytes != len(long) || a.Requests[0].Facility != "dream" || a.Requests[1].Turn != "40" || a.Requests[0].Entry != "an-entry" {
		t.Fatalf("the list is %+v", a)
	}
	head := askRecord(s, "/sent/body?id=2&part=body&head=1", false)
	if head.Header().Get("X-Total-Bytes") != strconv.Itoa(len(long)) || head.Body.Len() > 512 || head.Body.Len() < 509 || !strings.HasPrefix(long, head.Body.String()) {
		t.Fatalf("the first view is %d bytes, total %s", head.Body.Len(), head.Header().Get("X-Total-Bytes"))
	}
	s.rangeCap = 7
	got := append([]byte{}, head.Body.Bytes()...)
	for off := len(got); off < len(long); {
		r := askRecord(s, "/sent/body?id=2&part=body&offset="+strconv.Itoa(off)+"&length=100", false)
		if r.Code != http.StatusOK || r.Body.Len() == 0 || r.Body.Len() > 7 {
			t.Fatalf("a range answered %d with %d bytes", r.Code, r.Body.Len())
		}
		got = append(got, r.Body.Bytes()...)
		off += r.Body.Len()
	}
	if !bytes.Equal(got, []byte(long)) {
		t.Fatal("the first view and the ranges are not the body as kept")
	}
	s.rangeCap = 0
	sys := askRecord(s, "/sent/body?id=2&part=system&offset=0&length=100000", false)
	if sys.Body.String() != llm.SystemText([]byte(long)) || !strings.HasPrefix(sys.Body.String(), "Ring 0 😀 ") {
		t.Fatalf("the system text is %q", sys.Body.String())
	}
	if none := askRecord(s, "/sent/body?id=1&part=system&head=1", false); none.Code != http.StatusOK || none.Body.Len() != 0 || none.Header().Get("X-Total-Bytes") != "0" {
		t.Fatalf("a call with no system text answered %d %q", none.Code, none.Body.String())
	}
	for path, want := range map[string]int{
		"/sent/body?id=9&part=body&head=1":              http.StatusNotFound,
		"/sent/body?id=1&part=headers&head=1":           http.StatusBadRequest,
		"/sent/body?id=x&part=body&head=1":              http.StatusBadRequest,
		"/sent/body?id=1&part=body&offset=0":            http.StatusBadRequest,
		"/sent/body?id=1&part=body&offset=900&length=4": http.StatusOK,
	} {
		if r := askRecord(s, path, false); r.Code != want {
			t.Errorf("%s answered %d %q, want %d", path, r.Code, r.Body.String(), want)
		}
	}
	none := New("127.0.0.1", 0, &WSHandler{})
	none.AllowHost("127.0.0.1:8180")
	if r := askRecord(none, "/sent", false); r.Code != http.StatusServiceUnavailable {
		t.Errorf("/sent with no ring answered %d", r.Code)
	}
}
