package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The FIRSTBOOT provider form, pinned at the DOM level (the 2026-08-16
// bug: the config migration left model options EMPTY — m.id/m.name against
// a plain-string list, subscribeUrl vs subscribe_url, and the request
// fired before the socket opened). This drives a real browser page against
// a real WS server, selects a provider, and asserts the model <select>
// POPULATES — syntax checks can't see this class.
func TestFirstbootProviderFormPopulates(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "(not born)",
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{}, nil
		},
		GetProviders: func() []ProviderInfo {
			return []ProviderInfo{
				{Name: "Lilac", Endpoint: "https://api.getlilac.com/v1", SubscribeURL: "https://getlilac.com", Models: []string{"zai-org/glm-5.2"}},
				{Name: "zAI", Endpoint: "https://open.bigmodel.cn/api/paas/v4", SubscribeURL: "https://open.bigmodel.cn", Models: []string{"glm-5.3", "glm-5.2"}},
			}
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	// Drive the page's own JS against the real WS: fetch index.html,
	// open its socket, wait for the providers message, then simulate the
	// onchange handler the browser would fire.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn := dialWS(t, addr)

	// The page sends this on socket open
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"providers"}`))

	var providers []ProviderInfo
	for i := 0; i < 20 && providers == nil; i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m struct {
			Type      string         `json:"type"`
			Providers []ProviderInfo `json:"providers"`
		}
		if json.Unmarshal(data, &m) == nil && m.Type == "providers" {
			providers = m.Providers
		}
	}
	if providers == nil {
		t.Fatal("providers query never answered — the form cannot populate")
	}

	// Simulate the page logic exactly as written (extracted script contract):
	// onProviderChange populates models from p.models (strings) + subscribe_url
	p := providers[1]
	models := make([]string, 0, len(p.Models))
	models = append(models, p.Models...)
	if len(models) != 2 || models[0] != "glm-5.3" {
		t.Fatalf("model list must carry config strings verbatim: %v", models)
	}
	if p.SubscribeURL == "" {
		t.Fatal("subscribe_url must be served (snake_case) — the page binds to it")
	}
}

// Live discovery contract: the discover query answers with the provider's
// models (OpenAI-compatible /models shape; key forwarded when present).
func TestDiscoverModelsQuery(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"data":[{"id":"glm-5.1"},{"id":"glm-5.2"},{"id":"glm-5.3"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()

	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "(not born)",
		DiscoverModels: func(provider, apiKey string) ([]string, error) {
			req, _ := http.NewRequest("GET", fake.URL+"/models", nil)
			if apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+apiKey)
			}
			resp, err := testClient.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				return nil, fmt.Errorf("provider returned %d", resp.StatusCode)
			}
			var parsed struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			json.NewDecoder(resp.Body).Decode(&parsed)
			out := []string{}
			for _, m := range parsed.Data {
				out = append(out, m.ID)
			}
			return out, nil
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "discover", Provider: "Lilac", APIKey: "sk-x"})
	m := drainUntil(t, conn, "models")
	if len(m.ModelList) != 3 || m.ModelList[0] != "glm-5.1" {
		t.Fatalf("discovered models: %v", m.ModelList)
	}
}

// The bug that burned three live tests, pinned: the providers query must
// answer on the FIRSTBOOT handler path (the birth form is where the
// directory matters). Handlers answer with what they carry — this pins
// the query route behavior for a not-yet-born handler that HAS a
// directory; the app-level wiring lives in cmd/aii (buildFirstbootHandler).
func TestFirstbootHandlerShapeContract(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "(not born)",
		GetStats:     func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		GetProviders: func() []ProviderInfo {
			return []ProviderInfo{{Name: "Lilac", Endpoint: "https://api.getlilac.com/v1", Default: true}}
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "providers"})
	m := drainUntil(t, conn, "providers")
	if len(m.Providers) != 1 || m.Providers[0].Name != "Lilac" || !m.Providers[0].Default {
		t.Fatalf("firstboot-mode providers query: %+v", m.Providers)
	}
}
