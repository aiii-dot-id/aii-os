package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// embedReply asks a stand-in endpoint that answers every request with
// body for the embeddings of n inputs.
func embedReply(t *testing.T, body string, n int) ([][]float32, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer ts.Close()
	c := New(&ClientConfig{Endpoint: ts.URL, APIKey: "k", Model: "m", Retries: -1})
	inputs := make([]string, n)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("input %d", i)
	}
	return c.Embed(context.Background(), "e", inputs)
}

// A VECTOR IS ONLY AS GOOD AS THE TEXT IT IS FILED UNDER. The caller files
// each vector under the input at its position, and from then on nothing
// can tell that it was made from other words. The right number of rows is
// not enough: a reply whose rows do not name each input exactly once does
// not say which vector is whose, and the whole reply is refused before
// any of it can be stored.
func TestAnEmbeddingsReplyMustSayWhichVectorIsWhose(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		n                int
	}{
		{"one index twice", `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`, "two vectors name input 0", 2},
		{"an index past the inputs", `{"data":[{"index":0,"embedding":[1]},{"index":2,"embedding":[2]}]}`, "names input 2", 2},
		{"a negative index", `{"data":[{"index":-1,"embedding":[1]},{"index":0,"embedding":[2]}]}`, "names input -1", 2},
		{"one row with no index among rows that have one", `{"data":[{"embedding":[1]},{"index":1,"embedding":[2]}]}`, "1 of 2 vectors say which input", 2},
		{"an index that leaves an input with no vector", `{"data":[{"index":1,"embedding":[1]},{"index":2,"embedding":[2]},{"index":1,"embedding":[3]}]}`, "two vectors name input 1", 3},
		{"an empty vector", `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[]}]}`, "vector 1 is empty", 2},
		{"too few rows", `{"data":[{"index":0,"embedding":[1]}]}`, "1 vectors for 2 inputs", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vectors, err := embedReply(t, tc.body, tc.n)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the reply must be refused naming what is wrong (%q); got vectors %v, error %v", tc.want, vectors, err)
			}
			if vectors != nil {
				t.Fatalf("a refused reply hands back no vector: %v", vectors)
			}
		})
	}
}

// What a well-formed reply looks like is unchanged: indexes in any order
// are put in input order, and a reply that carries no index on any row is
// positional, as some OpenAI-compatible servers answer.
func TestAWellFormedEmbeddingsReplyIsTakenInInputOrder(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []float32 // the first component of each vector, in input order
	}{
		{"in order", `{"data":[{"index":0,"embedding":[10]},{"index":1,"embedding":[11]},{"index":2,"embedding":[12]}]}`, []float32{10, 11, 12}},
		{"out of order", `{"data":[{"index":2,"embedding":[12]},{"index":0,"embedding":[10]},{"index":1,"embedding":[11]}]}`, []float32{10, 11, 12}},
		{"no index on any row", `{"data":[{"embedding":[10]},{"embedding":[11]},{"embedding":[12]}]}`, []float32{10, 11, 12}},
		{"one input, index zero", `{"data":[{"index":0,"embedding":[10]}]}`, []float32{10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vectors, err := embedReply(t, tc.body, len(tc.want))
			if err != nil {
				t.Fatal(err)
			}
			for i, w := range tc.want {
				if len(vectors[i]) != 1 || vectors[i][0] != w {
					t.Fatalf("input %d was given %v, want [%v]: %v", i, vectors[i], w, vectors)
				}
			}
		})
	}
}
