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

// .
// .
// .
// .
// .
// .
func TestFirstbootProviderFormPopulates(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{}, nil
		},
		GetProviders: func() []ProviderInfo {
			return []ProviderInfo{
				{Name: "Acme", Endpoint: "https://api.acme.test/v1", SubscribeURL: "https://acme.test", Models: []string{"zai-org/glm-5.2"}},
				{Name: "zAI", Endpoint: "https://open.bigmodel.cn/api/paas/v4", SubscribeURL: "https://open.bigmodel.cn", Models: []string{"glm-5.3", "glm-5.2"}},
			}
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	// .
	// .
	// .
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn := dialWS(t, addr)

	// .
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

	// .
	// .
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

// .
// .
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

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "discover", Provider: "Acme", APIKey: "sk-x"})
	m := drainUntil(t, conn, "models")
	if len(m.ModelList) != 3 || m.ModelList[0] != "glm-5.1" {
		t.Fatalf("discovered models: %v", m.ModelList)
	}
}

// .
// .
// .
// .
// .
func TestFirstbootHandlerShapeContract(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		GetProviders: func() []ProviderInfo {
			return []ProviderInfo{{Name: "Acme", Endpoint: "https://api.acme.test/v1", Default: true}}
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "providers"})
	m := drainUntil(t, conn, "providers")
	if len(m.Providers) != 1 || m.Providers[0].Name != "Acme" || !m.Providers[0].Default {
		t.Fatalf("firstboot-mode providers query: %+v", m.Providers)
	}
}
