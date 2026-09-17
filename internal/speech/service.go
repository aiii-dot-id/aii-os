package speech

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type Service struct {
	// .
	// .
	Path string `json:"path,omitempty"`
	// .
	// .
	// .
	Base string `json:"base,omitempty"`
	// .
	// .
	Auth *Auth `json:"auth,omitempty"`
	// .
	// .
	Encoding string `json:"encoding,omitempty"`
	// .
	AudioField string `json:"audio_field,omitempty"`
	// .
	Fields map[string]string `json:"fields,omitempty"`
	// .
	// .
	Parts map[string]json.RawMessage `json:"parts,omitempty"`
	// .
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// .
	// .
	Body json.RawMessage `json:"body,omitempty"`
	// .
	Escape string `json:"escape,omitempty"`
	// .
	// .
	ContentType string `json:"content_type,omitempty"`
	// .
	Response Response `json:"response,omitzero"`
}

// .
// .
type Auth struct {
	// .
	In string `json:"in"`
	// .
	Name string `json:"name"`
	// .
	Scheme string `json:"scheme,omitempty"`
}

// .
type Response struct {
	// .
	// .
	Kind string `json:"kind,omitempty"`
	// .
	Text string `json:"text,omitempty"`
	// .
	Audio string `json:"audio,omitempty"`
	// .
	Format string `json:"format,omitempty"`
	// .
	Rate int `json:"rate,omitempty"`
}

// .
type Direction int

const (
	// .
	STT Direction = iota + 1
	// .
	TTS
)

func (d Direction) String() string {
	if d == TTS {
		return "tts"
	}
	return "stt"
}

func dialect(dir Direction) Service {
	bearer := &Auth{In: "header", Name: "Authorization", Scheme: "Bearer"}
	if dir == TTS {
		return Service{
			Path:     "/audio/speech",
			Auth:     bearer,
			Encoding: "json",
			// .
			// .
			Body:     json.RawMessage(`{"model":"{model}","input":"{text}","voice":"{voice}","response_format":"pcm"}`),
			Response: Response{Kind: "audio", Format: "pcm_s16le", Rate: 24000},
		}
	}
	return Service{
		Path:       "/audio/transcriptions",
		Auth:       bearer,
		Encoding:   "multipart",
		AudioField: "file",
		Fields:     map[string]string{"model": "{model}", "language": "{language}"},
		Response:   Response{Kind: "json", Text: "$.text"},
	}
}

// .
// .
func (s *Service) effective(dir Direction) Service {
	out := dialect(dir)
	if s == nil {
		return out
	}
	if s.Path != "" {
		out.Path = s.Path
	}
	if s.Base != "" {
		out.Base = s.Base
	}
	if s.Auth != nil {
		a := *s.Auth
		out.Auth = &a
	}
	if s.Encoding != "" {
		out.Encoding = s.Encoding
	}
	if s.AudioField != "" {
		out.AudioField = s.AudioField
	}
	if s.Fields != nil {
		out.Fields = s.Fields
	}
	if s.Parts != nil {
		out.Parts = s.Parts
	}
	if s.Query != nil {
		out.Query = s.Query
	}
	if s.Headers != nil {
		out.Headers = s.Headers
	}
	if len(s.Body) > 0 {
		out.Body = s.Body
	}
	if s.Escape != "" {
		out.Escape = s.Escape
	}
	if s.ContentType != "" {
		out.ContentType = s.ContentType
	}
	r := s.Response
	if r.Kind != "" {
		out.Response.Kind = r.Kind
	}
	if r.Text != "" {
		out.Response.Text = r.Text
	}
	if r.Audio != "" {
		out.Response.Audio = r.Audio
	}
	if r.Format != "" {
		out.Response.Format = r.Format
		if r.Format == "wav" {
			out.Response.Rate = 0
		}
	}
	if r.Rate != 0 {
		out.Response.Rate = r.Rate
	}
	return out
}

// .

var (
	encodings = map[Direction]map[string]bool{
		STT: {"multipart": true, "raw": true, "json": true},
		TTS: {"json": true, "template": true},
	}
	responseKinds = map[Direction]map[string]bool{
		STT: {"json": true, "text": true},
		TTS: {"audio": true, "json_base64": true},
	}
	schemes      = map[string]bool{"": true, "Bearer": true, "Token": true, "Basic": true}
	audioFormats = map[string]bool{"pcm_s16le": true, "wav": true}
)

// .
// .
var placeholderRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// .
type placement int

const (
	inPath placement = iota
	inQuery
	inHeader
	inField
	inBody
)

// .
func allowed(dir Direction, at placement, name string) error {
	switch name {
	case "model", "language":
		return nil
	case "voice", "rate":
		if dir == TTS {
			return nil
		}
		return fmt.Errorf("{%s} is for text-to-speech", name)
	case "text":
		if dir == TTS && at == inBody {
			return nil
		}
		return fmt.Errorf("{text} belongs in a text-to-speech body")
	case "audio_base64":
		if dir == STT && at == inBody {
			return nil
		}
		return fmt.Errorf("{audio_base64} belongs in a speech-to-text json body")
	case "key":
		return fmt.Errorf("the key is placed by auth, never by a placeholder")
	}
	return fmt.Errorf("unknown placeholder {%s}", name)
}

func checkPlaceholders(dir Direction, at placement, where, s string) error {
	for _, m := range placeholderRE.FindAllStringSubmatch(s, -1) {
		if err := allowed(dir, at, m[1]); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}
	return nil
}

// .
// .
// .
func (s *Service) Validate(dir Direction) error {
	e := s.effective(dir)
	if !strings.HasPrefix(e.Path, "/") {
		return fmt.Errorf("path %q must start with /", e.Path)
	}
	if err := checkPlaceholders(dir, inPath, "path", e.Path); err != nil {
		return err
	}
	if e.Base != "" {
		b, err := url.Parse(e.Base)
		if err != nil || (b.Scheme != "https" && b.Scheme != "http") || b.Host == "" || b.RawQuery != "" || b.Fragment != "" {
			return fmt.Errorf("base %q must be an http(s) URL with a host and no query", e.Base)
		}
	}
	if e.Auth == nil || (e.Auth.In != "header" && e.Auth.In != "query") {
		return errors.New(`auth.in must be "header" or "query"`)
	}
	if e.Auth.Name == "" || strings.ContainsAny(e.Auth.Name, " \r\n:") {
		return fmt.Errorf("auth.name %q is not a header or parameter name", e.Auth.Name)
	}
	if !schemes[e.Auth.Scheme] {
		return fmt.Errorf("auth.scheme %q is not one of Bearer, Token, Basic or none", e.Auth.Scheme)
	}
	if !encodings[dir][e.Encoding] {
		return fmt.Errorf("encoding %q is not a %s encoding", e.Encoding, dir)
	}
	for _, k := range sortedKeys(e.Query) {
		if err := checkPlaceholders(dir, inQuery, "query."+k, e.Query[k]); err != nil {
			return err
		}
	}
	for _, k := range sortedKeys(e.Headers) {
		if strings.ContainsAny(k, " \r\n:") || strings.ContainsAny(e.Headers[k], "\r\n") {
			return fmt.Errorf("headers.%s is not a header", k)
		}
		if err := checkPlaceholders(dir, inHeader, "headers."+k, e.Headers[k]); err != nil {
			return err
		}
	}
	switch e.Encoding {
	case "multipart":
		if e.AudioField == "" {
			return errors.New("audio_field is empty")
		}
		for _, k := range sortedKeys(e.Fields) {
			if err := checkPlaceholders(dir, inField, "fields."+k, e.Fields[k]); err != nil {
				return err
			}
		}
		for _, k := range sortedRawKeys(e.Parts) {
			if err := checkJSONTemplate(dir, "parts."+k, e.Parts[k]); err != nil {
				return err
			}
		}
	case "json":
		if err := checkJSONTemplate(dir, "body", e.Body); err != nil {
			return err
		}
	case "template":
		var tmpl string
		if err := json.Unmarshal(e.Body, &tmpl); err != nil || tmpl == "" {
			return errors.New("a template body must be a non-empty JSON string")
		}
		if e.Escape != "xml" {
			return errors.New(`a template body must name escape "xml"`)
		}
		if err := checkPlaceholders(dir, inBody, "body", tmpl); err != nil {
			return err
		}
	}
	if e.Encoding != "multipart" && (s != nil && (s.Fields != nil || s.Parts != nil)) {
		return fmt.Errorf("fields and parts are multipart only, and this encoding is %s", e.Encoding)
	}
	r := e.Response
	if !responseKinds[dir][r.Kind] {
		return fmt.Errorf("response.kind %q is not a %s answer", r.Kind, dir)
	}
	switch r.Kind {
	case "json":
		if _, err := parsePath(r.Text); err != nil {
			return fmt.Errorf("response.text: %w", err)
		}
	case "json_base64":
		if _, err := parsePath(r.Audio); err != nil {
			return fmt.Errorf("response.audio: %w", err)
		}
	}
	if dir == TTS {
		if !audioFormats[r.Format] {
			return fmt.Errorf("response.format %q is not pcm_s16le or wav", r.Format)
		}
		if r.Format == "pcm_s16le" && r.Rate <= 0 {
			return errors.New("response.rate is required for pcm_s16le")
		}
	}
	return nil
}

func checkJSONTemplate(dir Direction, where string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s is empty", where)
	}
	node, err := decodeTemplate(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if _, ok := node.(map[string]any); !ok {
		return fmt.Errorf("%s must be a JSON object", where)
	}
	var walk func(any) error
	walk = func(n any) error {
		switch v := n.(type) {
		case string:
			return checkPlaceholders(dir, inBody, where, v)
		case map[string]any:
			for _, k := range sortedAnyKeys(v) {
				if err := checkPlaceholders(dir, inBody, where+" key", k); err != nil {
					return err
				}
				if err := walk(v[k]); err != nil {
					return err
				}
			}
		case []any:
			for _, x := range v {
				if err := walk(x); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(node)
}

func decodeTemplate(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var node any
	if err := dec.Decode(&node); err != nil {
		return nil, err
	}
	return node, nil
}

// .

// .
type values map[string]string

// .
func expand(s string, v values, esc func(string) string) string {
	return placeholderRE.ReplaceAllStringFunc(s, func(m string) string {
		return esc(v[m[1:len(m)-1]])
	})
}

func verbatim(s string) string { return s }

// .
// .
// .
func blankOnly(s string, v values) bool {
	m := placeholderRE.FindStringSubmatch(s)
	return m != nil && m[0] == s && v[m[1]] == ""
}

// .
// .
// .
// .
// .
func expandJSON(node any, v values) (any, bool) {
	switch n := node.(type) {
	case string:
		if blankOnly(n, v) {
			return nil, false
		}
		return expand(n, v, verbatim), true
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, x := range n {
			if y, keep := expandJSON(x, v); keep {
				out[k] = y
			}
		}
		if len(out) == 0 && len(n) > 0 {
			return nil, false
		}
		return out, true
	case []any:
		out := make([]any, 0, len(n))
		for _, x := range n {
			if y, keep := expandJSON(x, v); keep {
				out = append(out, y)
			}
		}
		if len(out) == 0 && len(n) > 0 {
			return nil, false
		}
		return out, true
	}
	return node, true
}

func renderJSON(raw json.RawMessage, v values) ([]byte, error) {
	node, err := decodeTemplate(raw)
	if err != nil {
		return nil, err
	}
	filled, keep := expandJSON(node, v)
	if !keep {
		// .
		filled = map[string]any{}
	}
	return json.Marshal(filled)
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

// .

type pathStep struct {
	key   string
	index int
	isIdx bool
}

func parsePath(p string) ([]pathStep, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("path %q must start with $", p)
	}
	rest := p[1:]
	var steps []pathStep
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			j := 0
			for j < len(rest) && isNameByte(rest[j]) {
				j++
			}
			if j == 0 {
				return nil, fmt.Errorf("path %q: a member name must follow '.'", p)
			}
			steps = append(steps, pathStep{key: rest[:j]})
			rest = rest[j:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q: unclosed '['", p)
			}
			n, err := strconv.Atoi(rest[1:end])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("path %q: brackets hold a non-negative index and nothing else", p)
			}
			steps = append(steps, pathStep{index: n, isIdx: true})
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("path %q: members and indices only — no filters or wildcards", p)
		}
	}
	return steps, nil
}

func isNameByte(b byte) bool {
	return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// .
func lookup(doc any, path string) (any, bool) {
	steps, err := parsePath(path)
	if err != nil {
		return nil, false
	}
	cur := doc
	for _, s := range steps {
		if s.isIdx {
			arr, ok := cur.([]any)
			if !ok || s.index >= len(arr) {
				return nil, false
			}
			cur = arr[s.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[s.key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// .

// .
// .
// .
// .
// .
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// .
func requestURL(base, path string, query map[string]string, v values) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + expand(path, v, url.PathEscape))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for _, k := range sortedKeys(query) {
		if blankOnly(query[k], v) {
			continue
		}
		q.Set(k, expand(query[k], v, verbatim))
	}
	u.RawQuery = q.Encode()
	return u, nil
}

// .
// .
func applyAuth(req *http.Request, a *Auth, key string) {
	if key == "" || a == nil {
		return
	}
	val := key
	if a.Scheme != "" {
		val = a.Scheme + " " + key
	}
	if a.In == "query" {
		q := req.URL.Query()
		q.Set(a.Name, val)
		req.URL.RawQuery = q.Encode()
		return
	}
	req.Header.Set(a.Name, val)
}

func applyHeaders(req *http.Request, headers map[string]string, v values) {
	for _, k := range sortedKeys(headers) {
		if blankOnly(headers[k], v) {
			continue
		}
		req.Header.Set(k, expand(headers[k], v, verbatim))
	}
}

// .
// .
func shown(u *url.URL) string {
	c := *u
	c.RawQuery = ""
	c.User = nil
	return c.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// .
// .
// .
func (s *Service) Uses(dir Direction, name string) bool {
	e := s.effective(dir)
	token := "{" + name + "}"
	if strings.Contains(e.Path, token) || strings.Contains(string(e.Body), token) {
		return true
	}
	for _, m := range []map[string]string{e.Query, e.Headers} {
		for _, v := range m {
			if strings.Contains(v, token) {
				return true
			}
		}
	}
	if e.Encoding == "multipart" {
		for _, v := range e.Fields {
			if strings.Contains(v, token) {
				return true
			}
		}
		for _, v := range e.Parts {
			if strings.Contains(string(v), token) {
				return true
			}
		}
	}
	return false
}
