package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
type List struct {
	// .
	Path string `json:"path,omitempty"`
	// .
	// .
	// .
	// .
	// .
	Spec string `json:"spec,omitempty"`
	// .
	Query map[string]string `json:"query,omitempty"`
	// .
	Items string `json:"items"`
	// .
	// .
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// .
	// .
	// .
	Languages    string `json:"languages,omitempty"`
	LanguageID   string `json:"language_id,omitempty"`
	LanguageName string `json:"language_name,omitempty"`
	// .
	// .
	Accent string `json:"accent,omitempty"`
	Gender string `json:"gender,omitempty"`
	Age    string `json:"age,omitempty"`
	// .
	// .
	// .
	Search   string `json:"search,omitempty"`
	Language string `json:"language,omitempty"`
	// .
	// .
	Keep string `json:"keep,omitempty"`
	// .
	// .
	// .
	Match []string `json:"match,omitempty"`
	Skip  []string `json:"skip,omitempty"`
	// .
	Trim string `json:"trim,omitempty"`
	// .
	// .
	Next *ListPage `json:"next,omitempty"`
}

// .
// .
// .
// .
type ListPage struct {
	// .
	// .
	Kind string `json:"kind"`
	// .
	Param string `json:"param"`
	// .
	At string `json:"at,omitempty"`
	// .
	More string `json:"more,omitempty"`
	// .
	// .
	Total string `json:"total,omitempty"`
	// .
	// .
	Size int `json:"size,omitempty"`
	From int `json:"from,omitempty"`
}

// .
// .
type Item struct {
	ID        string
	Name      string
	Languages []Language
	// .
	// .
	Detail string
}

// .
type Language struct {
	ID   string
	Name string
}

const (
	// .
	maxListBytes = 8 << 20
	// .
	// .
	maxListItems = 5000
	maxListPages = 60
)

// .
// .
func (l *List) Validate() error {
	if l.Spec != "" {
		u, err := url.Parse(l.Spec)
		// .
		// .
		local := err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost")
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && local)) {
			return fmt.Errorf("list spec %q must be an https URL, or http to this machine", l.Spec)
		}
		if l.Path != "" || len(l.Query) > 0 || l.Next != nil || l.Search != "" || l.Language != "" {
			return errors.New("a list read from a published spec has no path, query, paging or search of its own")
		}
	} else if !strings.HasPrefix(l.Path, "/") {
		return fmt.Errorf("list path %q must start with /", l.Path)
	}
	literal := map[string]string{"path": l.Path, "trim": l.Trim, "search": l.Search, "language": l.Language}
	for k, v := range l.Query {
		literal["query."+k] = v
	}
	for _, where := range sortedKeys(literal) {
		if m := placeholderRE.FindString(literal[where]); m != "" {
			return fmt.Errorf("list %s holds %s — a list is literal", where, m)
		}
	}
	for _, w := range append(append([]string(nil), l.Match...), l.Skip...) {
		if strings.TrimSpace(w) == "" {
			return errors.New("list match and skip words must not be blank")
		}
	}
	for _, p := range []struct {
		where, path string
		optional    bool
	}{{"items", l.Items, false}, {"id", l.ID, false}, {"name", l.Name, true}, {"keep", l.Keep, true},
		{"languages", l.Languages, true}, {"language_id", l.LanguageID, true}, {"language_name", l.LanguageName, true},
		{"accent", l.Accent, true}, {"gender", l.Gender, true}, {"age", l.Age, true}} {
		if p.optional && p.path == "" {
			continue
		}
		if _, err := parsePath(p.path); err != nil {
			return fmt.Errorf("list %s: %w", p.where, err)
		}
	}
	return l.Next.validate()
}

func (p *ListPage) validate() error {
	if p == nil {
		return nil
	}
	if p.Param == "" || strings.ContainsAny(p.Param, " \r\n") {
		return fmt.Errorf("next.param %q is not a query parameter", p.Param)
	}
	for _, at := range []struct{ where, path string }{{"next.at", p.At}, {"next.more", p.More}, {"next.total", p.Total}} {
		if at.path == "" {
			continue
		}
		if _, err := parsePath(at.path); err != nil {
			return fmt.Errorf("%s: %w", at.where, err)
		}
	}
	switch p.Kind {
	case "cursor":
		if p.At == "" {
			return errors.New(`next.kind "cursor" needs next.at, the path to the cursor`)
		}
	case "number", "offset":
		if p.Total == "" && p.More == "" {
			return fmt.Errorf("next.kind %q needs next.total or next.more to know where the list ends", p.Kind)
		}
		if p.Kind == "offset" && p.Size <= 0 {
			return errors.New(`next.kind "offset" needs next.size, the page size it asks for`)
		}
	default:
		return fmt.Errorf("next.kind %q is not cursor, number or offset", p.Kind)
	}
	return nil
}

// .
// .
// .
// .
// .
func (s *Service) ListItems(ctx context.Context, dir Direction, l *List, endpoint, key string, timeout time.Duration, ask Ask) ([]Item, bool, error) {
	if l == nil {
		return nil, false, errors.New("speech: the service lists nothing here")
	}
	svc := s.effective(dir)
	if svc.Base != "" {
		endpoint = svc.Base
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := newHTTPClient(timeout)
	if l.Spec != "" {
		// .
		// .
		u, err := url.Parse(l.Spec)
		if err != nil {
			return nil, false, fmt.Errorf("speech: build request: %w", err)
		}
		doc, err := listPage(ctx, client, u, Service{}, "")
		if err != nil {
			return nil, false, err
		}
		items, err := l.read(doc, u, map[string]bool{})
		return items, err == nil, err
	}
	narrowed := map[string]string{}
	if l.Search != "" && strings.TrimSpace(ask.Search) != "" {
		narrowed[l.Search] = strings.TrimSpace(ask.Search)
	}
	if l.Language != "" && strings.TrimSpace(ask.Language) != "" {
		narrowed[l.Language] = strings.TrimSpace(ask.Language)
	}
	var out []Item
	seen := map[string]bool{}
	received := 0
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		query := make(map[string]string, len(l.Query)+len(narrowed)+1)
		for k, v := range l.Query {
			query[k] = v
		}
		for k, v := range narrowed {
			query[k] = v
		}
		if p := l.Next; p != nil && page > 0 {
			switch p.Kind {
			case "cursor":
				query[p.Param] = cursor
			case "number":
				query[p.Param] = strconv.Itoa(p.From + page)
			case "offset":
				query[p.Param] = strconv.Itoa(page * p.Size)
			}
		}
		u, err := requestURL(endpoint, l.Path, query, values{})
		if err != nil {
			return nil, false, fmt.Errorf("speech: build request: %w", err)
		}
		doc, err := listPage(ctx, client, u, svc, key)
		if err != nil {
			return nil, false, err
		}
		found, err := l.read(doc, u, seen)
		if err != nil {
			return nil, false, err
		}
		out = append(out, found...)
		if len(out) >= maxListItems {
			// .
			// .
			// .
			return out[:maxListItems], false, nil
		}
		// .
		// .
		// .
		// .
		// .
		raw := countAt(doc, l.Items)
		received += raw
		if raw == 0 || l.Next == nil || !l.Next.more(doc, &cursor, page+1, received) {
			return out, true, nil
		}
	}
	return out, false, nil
}

// .
// .
// .
type Ask struct {
	Search   string
	Language string
}

// .
// .
func (l *List) detail(item any) string {
	var said []string
	for _, path := range []string{l.Accent, l.Gender, l.Age} {
		if path == "" {
			continue
		}
		if word := stringAt(item, path); word != "" {
			said = append(said, word)
		}
	}
	return strings.Join(said, " · ")
}

// .
func (l *List) read(doc any, u *url.URL, seen map[string]bool) ([]Item, error) {
	found, _ := lookup(doc, l.Items)
	arr, ok := found.([]any)
	if !ok {
		return nil, fmt.Errorf("speech: %s answered with no list at %s", shown(u), l.Items)
	}
	var out []Item
	for _, it := range arr {
		id := strings.TrimPrefix(stringAt(it, l.ID), l.Trim)
		if id == "" || seen[id] || !l.keeps(it, id) {
			continue
		}
		seen[id] = true
		item := Item{ID: id}
		if l.Name != "" {
			item.Name = stringAt(it, l.Name)
		}
		item.Languages = l.languages(it)
		item.Detail = l.detail(it)
		out = append(out, item)
	}
	return out, nil
}

// .
// .
func listPage(ctx context.Context, client *http.Client, u *url.URL, svc Service, key string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("speech: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	raw, _, err := do(client, req, u, svc, values{}, key, maxListBytes)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err == nil {
		return doc, nil
	}
	// .
	// .
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("speech: %s answered with something that is neither JSON nor YAML: %w", shown(u), err)
	}
	return yamlPlain(doc), nil
}

// .
// .
func yamlPlain(node any) any {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			out[k] = yamlPlain(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			out[fmt.Sprint(k)] = yamlPlain(v)
		}
		return out
	case []any:
		out := make([]any, 0, len(n))
		for _, v := range n {
			out = append(out, yamlPlain(v))
		}
		return out
	case int:
		return float64(n)
	}
	return node
}

// .
// .
// .
func (p *ListPage) more(doc any, cursor *string, pagesRead, itemsRead int) bool {
	if p.More != "" {
		if flag, _ := lookup(doc, p.More); flag != true {
			return false
		}
	}
	switch p.Kind {
	case "cursor":
		at, _ := lookup(doc, p.At)
		s, _ := at.(string)
		if strings.TrimSpace(s) == "" {
			return false
		}
		*cursor = s
		return true
	case "number":
		if p.Total != "" {
			total, ok := whole(doc, p.Total)
			return ok && pagesRead < total
		}
	case "offset":
		if p.Total != "" {
			total, ok := whole(doc, p.Total)
			return ok && itemsRead < total
		}
	}
	// .
	// .
	return p.More != ""
}

// .
func countAt(doc any, path string) int {
	v, ok := lookup(doc, path)
	if !ok {
		return 0
	}
	if items, ok := v.([]any); ok {
		return len(items)
	}
	return 0
}

// .
func whole(doc any, path string) (int, bool) {
	v, ok := lookup(doc, path)
	if !ok {
		return 0, false
	}
	n, isNumber := v.(float64)
	return int(n), isNumber
}

// .
// .
func (l *List) languages(item any) []Language {
	if l.Languages == "" {
		return nil
	}
	found, _ := lookup(item, l.Languages)
	// .
	// .
	if code, isString := found.(string); isString {
		if code = strings.TrimSpace(code); code != "" {
			return []Language{{ID: code}}
		}
		return nil
	}
	arr, ok := found.([]any)
	if !ok {
		return nil
	}
	var out []Language
	for _, one := range arr {
		if code, isString := one.(string); isString {
			if code = strings.TrimSpace(code); code != "" {
				out = append(out, Language{ID: code})
			}
			continue
		}
		lang := Language{ID: stringAt(one, l.LanguageID), Name: stringAt(one, l.LanguageName)}
		if lang.ID != "" {
			out = append(out, lang)
		}
	}
	return out
}

// .
func (l *List) keeps(item any, id string) bool {
	if l.Keep != "" {
		if flag, _ := lookup(item, l.Keep); flag != true {
			return false
		}
	}
	if len(l.Match) > 0 && !containsWord(id, l.Match) {
		return false
	}
	return !containsWord(id, l.Skip)
}

func containsWord(id string, words []string) bool {
	id = strings.ToLower(id)
	for _, w := range words {
		if strings.Contains(id, strings.ToLower(w)) {
			return true
		}
	}
	return false
}

// .
func stringAt(doc any, path string) string {
	v, _ := lookup(doc, path)
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
