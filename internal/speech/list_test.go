package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func list(t *testing.T, doc string) *List {
	t.Helper()
	var l List
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		t.Fatalf("list %s: %v", doc, err)
	}
	if err := l.Validate(); err != nil {
		t.Fatalf("list %s: %v", doc, err)
	}
	return &l
}

// .
// .
// .
// .
func TestAListIsOneGetWhereTheServiceAnswers(t *testing.T) {
	var got *http.Request
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write([]byte(`{"voices":[
			{"voice_id":"v1","name":" Rachel ","labels":{"accent":"American","gender":"female","age":"young"},"verified_languages":[{"language":"en"},{"language":"de"}]},
			{"voice_id":"v2"},
			{"voice_id":"v1","name":"again"},
			{"name":"no id"},
			{"voice_id":7}]}`))
	}))
	defer vendor.Close()

	s := svc(t, `{"path":"/v1/text-to-speech/{voice}","auth":{"in":"header","name":"xi-api-key"},"headers":{"X-Version":"2026-08-14","X-Model":"{model}"}}`)
	l := list(t, `{"path":"/v2/voices","query":{"page_size":"100"},"items":"$.voices","id":"$.voice_id","name":"$.name",
		"accent":"$.labels.accent","gender":"$.labels.gender","age":"$.labels.age","languages":"$.verified_languages","language_id":"$.language"}`)
	items, whole, err := s.ListItems(context.Background(), TTS, l, vendor.URL, "el-key", 0, Ask{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet || got.URL.Path != "/v2/voices" || got.URL.Query().Get("page_size") != "100" {
		t.Errorf("asked %s %s", got.Method, got.URL)
	}
	if got.Header.Get("xi-api-key") != "el-key" || got.Header.Get("X-Version") != "2026-08-14" || got.Header.Get("Accept") != "application/json" {
		t.Errorf("headers %v", got.Header)
	}
	if _, sent := got.Header["X-Model"]; sent {
		t.Errorf("a header holding only a blank placeholder was sent: %v", got.Header)
	}
	if !whole {
		t.Errorf("a list with no paging came back as if something were left")
	}
	if len(items) != 2 || items[0].ID != "v1" || items[0].Name != "Rachel" || items[0].Detail != "American · female · young" ||
		len(items[0].Languages) != 2 || items[0].Languages[0].ID != "en" || items[1].ID != "v2" {
		t.Errorf("items %+v", items)
	}
}

// .
// .
// .
func TestAListIsFollowedToItsEnd(t *testing.T) {
	for _, tc := range []struct {
		why, list string
		answer    func(page int, q url.Values) string
		wantParam string
		wantItems int
	}{
		{"a cursor", `{"path":"/v","query":{"page_size":"2"},"items":"$.voices","id":"$.id","next":{"kind":"cursor","param":"next_page_token","at":"$.next_page_token","more":"$.has_more"}}`,
			func(page int, q url.Values) string {
				if page < 2 {
					return fmt.Sprintf(`{"voices":[{"id":"a%d"},{"id":"b%d"}],"has_more":true,"next_page_token":"tok%d"}`, page, page, page+1)
				}
				return `{"voices":[{"id":"last"}],"has_more":false,"next_page_token":null}`
			}, "next_page_token", 5},
		{"a page number", `{"path":"/v","query":{"page_size":"2"},"items":"$.voices_page","id":"$.id","next":{"kind":"number","param":"page_number","total":"$.total_pages"}}`,
			func(page int, q url.Values) string {
				return fmt.Sprintf(`{"total_pages":3,"voices_page":[{"id":"a%d"},{"id":"b%d"}]}`, page, page)
			}, "page_number", 6},
		{"an offset", `{"path":"/v","query":{"limit":"2"},"items":"$.items","id":"$.id","next":{"kind":"offset","param":"offset","size":2,"total":"$.total"}}`,
			func(page int, q url.Values) string {
				return fmt.Sprintf(`{"total":6,"items":[{"id":"a%d"},{"id":"b%d"}]}`, page, page)
			}, "offset", 6},
	} {
		t.Run(tc.why, func(t *testing.T) {
			var asked []string
			page := 0
			vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked = append(asked, r.URL.Query().Get(tc.wantParam))
				body := tc.answer(page, r.URL.Query())
				page++
				_, _ = w.Write([]byte(body))
			}))
			defer vendor.Close()
			var dialect *Service
			items, whole, err := dialect.ListItems(context.Background(), TTS, list(t, tc.list), vendor.URL, "k", 0, Ask{})
			if err != nil {
				t.Fatal(err)
			}
			if !whole || len(items) != tc.wantItems {
				t.Fatalf("%d items, want %d, whole=%v", len(items), tc.wantItems, whole)
			}
			if len(asked) != 3 || asked[0] != "" {
				t.Fatalf("asked %q", asked)
			}
			switch tc.wantParam {
			case "next_page_token":
				if asked[1] != "tok1" || asked[2] != "tok2" {
					t.Errorf("the cursor was not sent back as it came: %q", asked)
				}
			case "page_number":
				if asked[1] != "1" || asked[2] != "2" {
					t.Errorf("pages %q", asked)
				}
			case "offset":
				if asked[1] != "2" || asked[2] != "4" {
					t.Errorf("offsets %q", asked)
				}
			}
		})
	}
}

// .
// .
// .
func TestASearchAndALanguageGoToTheVendorThatTakesThem(t *testing.T) {
	var asked url.Values
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		_, _ = w.Write([]byte(`{"data":[{"id":"v1"}]}`))
	}))
	defer vendor.Close()
	var dialect *Service
	ask := Ask{Search: " barber ", Language: "en"}
	if _, _, err := dialect.ListItems(context.Background(), TTS,
		list(t, `{"path":"/voices","items":"$.data","id":"$.id","search":"q","language":"language"}`), vendor.URL, "k", 0, ask); err != nil {
		t.Fatal(err)
	}
	if asked.Get("q") != "barber" || asked.Get("language") != "en" {
		t.Errorf("the vendor was not asked to narrow: %v", asked)
	}
	if _, _, err := dialect.ListItems(context.Background(), TTS,
		list(t, `{"path":"/voices","items":"$.data","id":"$.id"}`), vendor.URL, "k", 0, ask); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 {
		t.Errorf("a vendor that names no search parameter was sent one: %v", asked)
	}
}

// .
// .
// .
func TestAListKeepsWhatItsFlagAndWordsSay(t *testing.T) {
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":"models/gpt-4o-transcribe","ok":true},
			{"id":"models/gpt-4o-realtime-transcribe","ok":true},
			{"id":"models/whisper-1","ok":true},
			{"id":"models/whisper-2","ok":"true"},
			{"id":"models/chat-model","ok":true},
			{"id":"models/WHISPER-3","ok":true}
		]`))
	}))
	defer vendor.Close()

	var dialect *Service
	items, _, err := dialect.ListItems(context.Background(), STT,
		list(t, `{"path":"/models","items":"$","id":"$.id","keep":"$.ok","match":["transcribe","whisper"],"skip":["realtime"],"trim":"models/"}`),
		vendor.URL, "k", 0, Ask{})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(items) != "[{gpt-4o-transcribe  [] } {whisper-1  [] } {WHISPER-3  [] }]" {
		t.Errorf("items %v", items)
	}
}

// .
// .
// .
func TestAListIsBoundedAndSaysWhenItIsCutShort(t *testing.T) {
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`{"data":[`)
		for i := 0; i < 400; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"id":"m%d-%s"}`, i, r.URL.Query().Get("offset"))
		}
		b.WriteString(`],"total":100000}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer vendor.Close()
	var dialect *Service
	items, whole, err := dialect.ListItems(context.Background(), TTS,
		list(t, `{"path":"/models","items":"$.data","id":"$.id","next":{"kind":"offset","param":"offset","size":400,"total":"$.total"}}`),
		vendor.URL, "k", 0, Ask{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != maxListItems || whole {
		t.Fatalf("%d items, whole=%v", len(items), whole)
	}
}

// .
// .
func TestAListRefusalIsTheServicesOwnWords(t *testing.T) {
	var leaked bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("xi-api-key") != ""
		_, _ = w.Write([]byte(`{"voices":[]}`))
	}))
	defer elsewhere.Close()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, elsewhere.URL+"/v2/voices", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":{"message":"invalid key el-secret-99 for voices_read"}}`))
	}))
	defer vendor.Close()

	s := svc(t, `{"auth":{"in":"header","name":"xi-api-key"}}`)
	_, _, err := s.ListItems(context.Background(), TTS, list(t, `{"path":"/v2/voices","items":"$.voices","id":"$.voice_id"}`), vendor.URL, "el-secret-99", 0, Ask{})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Status != http.StatusUnauthorized || !strings.Contains(err.Error(), "voices_read") || strings.Contains(err.Error(), "el-secret-99") {
		t.Fatalf("refusal %v", err)
	}

	_, _, err = s.ListItems(context.Background(), TTS, list(t, `{"path":"/moved","items":"$.voices","id":"$.voice_id"}`), vendor.URL, "el-secret-99", 0, Ask{})
	if leaked || err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("a redirected list: leaked %v, err %v", leaked, err)
	}
}

// .
// .
func TestAnAnswerWithNoListSaysSo(t *testing.T) {
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"voices":{"v1":"Rachel"}}`))
	}))
	defer vendor.Close()
	var dialect *Service
	_, _, err := dialect.ListItems(context.Background(), TTS, list(t, `{"path":"/voices","items":"$.voices","id":"$.id"}`), vendor.URL, "", 0, Ask{})
	if err == nil || !strings.Contains(err.Error(), "no list at $.voices") {
		t.Fatalf("err %v", err)
	}
}

// .
func TestAListFollowsTheServiceBase(t *testing.T) {
	var path, key string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("x-goog-api-key")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/m","displayName":"M"}]}`))
	}))
	defer vendor.Close()
	s := svc(t, `{"base":"`+vendor.URL+`/v1beta","path":"/models/{model}:generateContent","encoding":"json","auth":{"in":"header","name":"x-goog-api-key"},"body":{"a":"{audio_base64}"}}`)
	items, _, err := s.ListItems(context.Background(), STT,
		list(t, `{"path":"/models","items":"$.models","id":"$.name","name":"$.displayName","trim":"models/"}`),
		"http://127.0.0.1:1/v1beta/openai", "g-key", 0, Ask{})
	if err != nil || path != "/v1beta/models" || key != "g-key" || len(items) != 1 || items[0].ID != "m" || items[0].Name != "M" {
		t.Fatalf("path %q key %q items %v err %v", path, key, items, err)
	}
}

// .
func TestAListOutsideTheClosedSetIsRefused(t *testing.T) {
	for _, tc := range []struct{ why, doc, want string }{
		{"a relative path", `{"path":"v2/voices","items":"$.voices","id":"$.id"}`, "must start with /"},
		{"a placeholder in the path", `{"path":"/v1/{model}/voices","items":"$.voices","id":"$.id"}`, "path holds {model}"},
		{"a placeholder in the query", `{"path":"/v","query":{"lang":"{language}"},"items":"$.v","id":"$.id"}`, "query.lang holds {language}"},
		{"a wildcard for the items", `{"path":"/v","items":"$.data[*].voices","id":"$.id"}`, "list items"},
		{"a filter for the flag", `{"path":"/v","items":"$.v","id":"$.id","keep":"$[?(@.ok)]"}`, "list keep"},
		{"a filter for a language", `{"path":"/v","items":"$.v","id":"$.id","languages":"$.langs[*]"}`, "list languages"},
		{"no id", `{"path":"/v","items":"$.v"}`, "list id"},
		{"a blank word", `{"path":"/v","items":"$.v","id":"$.id","match":[" "]}`, "must not be blank"},
		{"paging nobody wrote", `{"path":"/v","items":"$.v","id":"$.id","next":{"kind":"scroll","param":"p"}}`, "not cursor, number or offset"},
		{"a cursor with nowhere to read it", `{"path":"/v","items":"$.v","id":"$.id","next":{"kind":"cursor","param":"p"}}`, "needs next.at"},
		{"counting with no end", `{"path":"/v","items":"$.v","id":"$.id","next":{"kind":"number","param":"p"}}`, "to know where the list ends"},
		{"an offset with no page size", `{"path":"/v","items":"$.v","id":"$.id","next":{"kind":"offset","param":"p","total":"$.t"}}`, "needs next.size"},
		{"a spec that is not a URL", `{"spec":"ftp://x/openapi.yaml","items":"$.v","id":"$"}`, "must be an https URL"},
		{"a spec with paging of its own", `{"spec":"https://x/openapi.json","items":"$.v","id":"$","next":{"kind":"cursor","param":"p","at":"$.c"}}`, "has no path, query, paging or search"},
		{"a spec with a path of its own", `{"spec":"https://x/openapi.json","path":"/v","items":"$.v","id":"$"}`, "has no path, query, paging or search"},
		{"a page parameter that is not one", `{"path":"/v","items":"$.v","id":"$.id","next":{"kind":"cursor","param":"p q","at":"$.c"}}`, "not a query parameter"},
	} {
		var l List
		if err := json.Unmarshal([]byte(tc.doc), &l); err != nil {
			t.Fatalf("%s: %v", tc.why, err)
		}
		err := l.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.why, err)
		}
	}
}

// .
// .
// .
// .
func TestAListReadFromAPublishedSpec(t *testing.T) {
	for _, tc := range []struct{ why, ctype, body, items, want string }{
		{"a JSON spec", "application/json",
			`{"components":{"schemas":{"Body":{"properties":{"model_id":{"examples":["scribe_v2"]}}}}}}`,
			"$.components.schemas.Body.properties.model_id.examples", "[{scribe_v2  [] }]"},
		{"a YAML spec", "text/yaml",
			"components:\n  schemas:\n    VoiceIdsShared:\n      anyOf:\n        - type: string\n        - type: string\n          enum:\n            - alloy\n            - ash\n",
			"$.components.schemas.VoiceIdsShared.anyOf[1].enum", "[{alloy  [] } {ash  [] }]"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			var carried http.Header
			spec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				carried = r.Header.Clone()
				w.Header().Set("Content-Type", tc.ctype)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer spec.Close()
			l := &List{Spec: spec.URL + "/openapi", Items: tc.items, ID: "$"}
			if err := l.Validate(); err != nil {
				t.Fatal(err)
			}
			s := svc(t, `{"auth":{"in":"header","name":"xi-api-key"}}`)
			items, whole, err := s.ListItems(context.Background(), TTS, l, "https://vendor.invalid", "el-key", 0, Ask{})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(items) != tc.want || !whole {
				t.Errorf("items %v whole %v", items, whole)
			}
			if carried.Get("xi-api-key") != "" || carried.Get("Authorization") != "" {
				t.Errorf("a key went to a public document: %v", carried)
			}
		})
	}
}

// .
// .
// .
func TestOneLanguageIsStillALanguage(t *testing.T) {
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"v1","name":"Skylar","language":"en","gender":"feminine"}]}`))
	}))
	defer vendor.Close()
	var dialect *Service
	items, _, err := dialect.ListItems(context.Background(), TTS,
		list(t, `{"path":"/voices","items":"$.data","id":"$.id","name":"$.name","languages":"$.language","gender":"$.gender"}`),
		vendor.URL, "k", 0, Ask{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Languages) != 1 || items[0].Languages[0].ID != "en" || items[0].Detail != "feminine" {
		t.Fatalf("items %+v", items)
	}
}

// .
// .
// .
// .
// .
// .
func TestAWalkMeasuresWhatItReceivedAndEndsAtAnEmptyPage(t *testing.T) {
	pages := 0
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		switch r.URL.Query().Get("offset") {
		case "", "0":
			_, _ = w.Write([]byte(`{"total":4,"items":[{"id":"a"},{"id":"a"}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"total":4,"items":[{"id":"b"},{"id":"c"}]}`))
		default:
			_, _ = w.Write([]byte(`{"total":4,"items":[]}`))
		}
	}))
	defer vendor.Close()
	var dialect *Service
	spec := list(t, `{"path":"/v","items":"$.items","id":"$.id","next":{"kind":"offset","param":"offset","size":2,"total":"$.total"}}`)
	items, whole, err := dialect.ListItems(context.Background(), TTS, spec, vendor.URL, "k", 0, Ask{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || !whole {
		t.Fatalf("%d items, whole=%v — the duplicate made the list incomplete", len(items), whole)
	}
	if pages != 2 {
		t.Fatalf("the walk asked %d pages for a total of 4 in pages of 2", pages)
	}

	// .
	pages = 0
	stingy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("offset") == "" || r.URL.Query().Get("offset") == "0" {
			_, _ = w.Write([]byte(`{"total":100,"items":[{"id":"only"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"total":100,"items":[]}`))
	}))
	defer stingy.Close()
	items, whole, err = dialect.ListItems(context.Background(), TTS, spec, stingy.URL, "k", 0, Ask{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !whole || pages != 2 {
		t.Fatalf("%d items, whole=%v, %d pages — an empty page did not end the walk", len(items), whole, pages)
	}
}
