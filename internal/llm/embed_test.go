package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// .
// .
// .
func TestEmbedPostsAndOrdersVectors(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		fmt.Fprint(w, `{"model":"embed-1","data":[{"index":1,"embedding":[0.5,0.25]},{"index":0,"embedding":[1,2]}]}`)
	}))
	defer ts.Close()
	c := New(&ClientConfig{Endpoint: ts.URL + "/v1", APIKey: "sk-test", Model: "chat-1", Retries: -1})
	vectors, err := c.Embed(context.Background(), "embed-1", []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/embeddings" || !strings.Contains(gotAuth, "sk-test") || gotBody["model"] != "embed-1" {
		t.Fatalf("request: path %s auth %q body %v", gotPath, gotAuth, gotBody)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][0] != 0.5 {
		t.Fatalf("vectors must come back in input order: %v", vectors)
	}
}

// .
// .
func TestEmbedRefusesWhatItCannotServe(t *testing.T) {
	a := New(&ClientConfig{Endpoint: "https://api.anthropic.test", APIKey: "k", Model: "m", Provider: "anthropic", Retries: -1})
	if _, err := a.Embed(context.Background(), "embed-1", []string{"x"}); err == nil || !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("anthropic must refuse by name, got %v", err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	}))
	defer ts.Close()
	c := New(&ClientConfig{Endpoint: ts.URL, APIKey: "k", Model: "m", Retries: -1})
	if _, err := c.Embed(context.Background(), "e", []string{"a", "b"}); err == nil || !strings.Contains(err.Error(), "vectors for") {
		t.Fatalf("a count mismatch is an error, got %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); fmt.Fprint(w, "no") }))
	defer bad.Close()
	c2 := New(&ClientConfig{Endpoint: bad.URL, APIKey: "k", Model: "m", Retries: -1})
	if _, err := c2.Embed(context.Background(), "e", []string{"a"}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a remote error is an error, got %v", err)
	}
}
