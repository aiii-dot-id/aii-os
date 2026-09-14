package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// .
// .
// .
type CachePolicy struct {
	Mode        string `json:"mode,omitempty"`
	TTL         string `json:"ttl,omitempty"`
	TailTTL     string `json:"tail_ttl,omitempty"`
	Key         string `json:"key,omitempty"`
	Diagnostics bool   `json:"diagnostics,omitempty"`
}

type CachePolicyError struct{ Field, Reason string }

func (e *CachePolicyError) Error() string {
	return fmt.Sprintf("cache policy %s: %s", e.Field, e.Reason)
}

func ValidateCachePolicy(p CachePolicy, dialect Dialect, explicit bool) error {
	refuse := func(field, reason string) error { return &CachePolicyError{field, reason} }
	switch p.Mode {
	case "", "auto", "explicit", "off":
	default:
		return refuse("mode", "want auto, explicit, or off")
	}
	if len(p.Key) > 64 {
		return refuse("key", "maximum 64 bytes")
	}
	if dialect == DialectResponses {
		if p.TTL != "" || p.TailTTL != "" || p.Diagnostics || p.Mode == "explicit" || p.Mode == "off" {
			return refuse("policy", "ChatGPT subscription supports routing keys and automatic caching; API retention/breakpoint controls are not declared")
		}
		return nil
	}
	if dialect == DialectAnthropic {
		for _, ttl := range []string{p.TTL, p.TailTTL} {
			if ttl != "" && ttl != "5m" && ttl != "1h" {
				return refuse("ttl", "Anthropic accepts 5m or 1h")
			}
		}
		if p.TailTTL == "1h" && p.TTL != "1h" {
			return refuse("tail_ttl", "longer TTL must precede shorter TTL")
		}
		if p.Key != "" {
			return refuse("key", "Anthropic derives keys from the prefix")
		}
		return nil
	}
	if p.TailTTL != "" {
		return refuse("tail_ttl", "only Anthropic supports mixed TTLs")
	}
	if p.Diagnostics {
		return refuse("diagnostics", "native Anthropic diagnostics only")
	}
	if (p.Mode == "explicit" || p.Mode == "off") && !explicit {
		return refuse("mode", "model/endpoint has no declared explicit-cache support")
	}
	if explicit {
		if p.TTL != "" && p.TTL != "30m" {
			return refuse("ttl", "explicit-cache models accept 30m")
		}
	} else {
		switch p.TTL {
		case "", "in_memory", "24h":
		default:
			return refuse("ttl", "legacy OpenAI caching accepts in_memory or 24h")
		}
	}
	return nil
}

func (c *Client) cachePolicy() (CachePolicy, error) {
	return ResolveCachePolicy(c.cache, c.extra, DialectFor(c.provider), c.explicitCache, c.cacheRetentions)
}

// .
// .
func ResolveCachePolicy(config *CachePolicy, extra map[string]any, dialect Dialect, explicit bool, retentions []string) (CachePolicy, error) {
	p := CachePolicy{}
	if config != nil {
		p = *config
	} else if dialect == DialectOpenAI {
		field := func(key string) (string, error) {
			v, ok := extra[key]
			if !ok || v == nil {
				return "", nil
			}
			s, ok := v.(string)
			if !ok {
				return "", &CachePolicyError{key, "want a string"}
			}
			return s, nil
		}
		var err error
		p.Key, err = field("prompt_cache_key")
		if err != nil {
			return p, err
		}
		p.TTL, err = field("prompt_cache_retention")
		if err != nil {
			return p, err
		}
		if v, ok := extra["prompt_cache_options"]; ok && v != nil {
			b, err := json.Marshal(v)
			if err != nil {
				return p, err
			}
			var options struct {
				Mode string `json:"mode"`
				TTL  string `json:"ttl"`
			}
			if err := json.Unmarshal(b, &options); err != nil {
				return p, &CachePolicyError{"prompt_cache_options", "invalid object"}
			}
			p.Mode = options.Mode
			if p.Mode == "implicit" {
				p.Mode = "auto"
			}
			if options.TTL != "" {
				p.TTL = options.TTL
			}
		}
	}
	if err := ValidateCachePolicy(p, dialect, explicit); err != nil {
		return p, err
	}
	if dialect == DialectOpenAI && p.TTL != "" && retentions != nil {
		allowed := false
		for _, ttl := range retentions {
			allowed = allowed || p.TTL == ttl
		}
		if !allowed {
			return p, &CachePolicyError{"ttl", "not supported by this model's declared retention policies"}
		}
	}
	return p, nil
}

// .
// .
// .
func (c *Client) applyOpenAICache(body []byte, messages []Message) ([]byte, error) {
	p, err := c.cachePolicy()
	if err != nil {
		return nil, err
	}
	if c.cache == nil && p == (CachePolicy{}) && !c.explicitCache {
		return body, nil
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if c.cache != nil {
		delete(req, "prompt_cache_key")
		delete(req, "prompt_cache_retention")
		delete(req, "prompt_cache_options")
	}
	set := func(k string, v any) { req[k], _ = json.Marshal(v) }
	if p.Key == "" && c.explicitCache {
		for _, m := range messages {
			if m.Role == "system" && m.StableLen > 0 && m.StableLen <= len(m.Content) {
				p.Key = fmt.Sprintf("%x", sha256.Sum256([]byte(m.Content[:m.StableLen])))
				break
			}
		}
	}
	if p.Key != "" {
		set("prompt_cache_key", p.Key)
	}
	if c.explicitCache {
		delete(req, "prompt_cache_retention")
		mode := "implicit"
		if p.Mode == "explicit" || p.Mode == "off" {
			mode = "explicit"
		}
		set("prompt_cache_options", map[string]string{"mode": mode, "ttl": "30m"})
		if p.Mode != "off" {
			var wire []map[string]json.RawMessage
			if err := json.Unmarshal(req["messages"], &wire); err != nil {
				return nil, err
			}
			// .
			left := 3
			if mode == "explicit" {
				left = 4
			}
			for i, m := range messages {
				if left == 0 || m.Role != "system" || m.Content == "" {
					continue
				}
				seam := m.StableLen
				if seam == 0 {
					seam = len(m.Content)
				}
				if seam < 0 || seam > len(m.Content) {
					return nil, &CachePolicyError{"stable_len", "outside message"}
				}
				parts := []map[string]any{{"type": "text", "text": m.Content[:seam], "prompt_cache_breakpoint": map[string]string{"mode": "explicit"}}}
				if seam < len(m.Content) {
					parts = append(parts, map[string]any{"type": "text", "text": m.Content[seam:]})
				}
				wire[i]["content"], _ = json.Marshal(parts)
				left--
			}
			set("messages", wire)
		}
	} else if p.TTL != "" {
		set("prompt_cache_retention", p.TTL)
	}
	return json.Marshal(req)
}

func addBeta(current, flag string) string {
	for _, v := range strings.Split(current, ",") {
		if strings.TrimSpace(v) == flag {
			return current
		}
	}
	if current == "" {
		return flag
	}
	return current + "," + flag
}

// .
type stablePrefixKey struct{}

func WithStablePrefix(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, stablePrefixKey{}, n)
}
func StablePrefix(ctx context.Context) int { n, _ := ctx.Value(stablePrefixKey{}).(int); return n }

func CacheModes(dialect Dialect, explicit bool) []string {
	if dialect == DialectAnthropic || (dialect != DialectResponses && explicit) {
		return []string{"auto", "explicit", "off"}
	}
	return []string{"auto"}
}
func CacheTTLs(dialect Dialect, explicit bool, retentions []string) []string {
	if dialect == DialectAnthropic {
		return []string{"5m", "1h"}
	}
	if dialect == DialectResponses {
		return nil
	}
	if explicit {
		return []string{"30m"}
	}
	return append([]string(nil), retentions...)
}
func cacheDiagnosticState(raw json.RawMessage, previous string) string {
	if len(raw) == 0 {
		return "not_returned"
	}
	if string(raw) == "null" {
		if previous == "" {
			return "no_comparison"
		}
		return "no_divergence"
	}
	var diagnostic struct {
		Reason *struct {
			Type string `json:"type"`
		} `json:"cache_miss_reason"`
	}
	if json.Unmarshal(raw, &diagnostic) != nil {
		return "unavailable"
	}
	if diagnostic.Reason == nil {
		return "pending"
	}
	return diagnostic.Reason.Type
}
