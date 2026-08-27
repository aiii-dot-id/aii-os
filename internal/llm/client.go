// Package llm implements the provider wire clients for AII OS.
//
// The OpenAI-compatible path covers OpenAI, local models, Lilac, OpenRouter,
// and any endpoint that speaks /v1/chat/completions. Anthropic and ChatGPT
// subscription credentials use their native dialects.
//
// The client sends the composed prompt as a chat completion request and
// parses the response into verb calls and tool calls.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client translates the runtime's message shape to one provider dialect.
type Client struct {
	endpoint         string
	apiKey           string
	model            string
	maxInputTokens   int
	maxOutputTokens  int
	provider         string // "" / "openai" = OpenAI-compatible; "anthropic" = native Messages API
	oauthBillingText string
	thinkingBudget   int
	thinkingMode     string
	thinkingDisplay  string
	reasoningEffort  string
	temperature      *float64         // nil = omit on the wire (server default); 0 is a VALID temperature
	topP             *float64         // nil = omit on the wire
	extra            map[string]any   // OpenAI-path passthrough, merged verbatim at top level; typed fields win
	extraWarned      sync.Map         // extra keys already warned for colliding with typed fields (log once)
	creds            CredentialSource // OAuth: resolves the credential per request; nil = static apiKey
	httpClient       *http.Client
	retries          int           // transport-class retries per call (llm.retries)
	retryBackoff     time.Duration // pause before each retry (llm.retry_backoff_ms)
}

// HTTPError preserves a provider's structured refusal across retries.
// Callers may classify the status without parsing the human-readable body.
type HTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
	Retryable  bool
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("API returned %d: %s", e.StatusCode, e.Body)
}

// Credential is one request's authorization: the token, plus any extra
// headers the credential itself requires (a ChatGPT credential carries
// its account id; an API key carries nothing).
type Credential struct {
	Token   string
	Headers map[string]string
	Gen     uint64
}

// CredentialSource hands out credentials and can be told that one was
// rejected. Implemented by internal/oauth; the static-key path leaves it
// nil.
type CredentialSource interface {
	Credential(ctx context.Context) (Credential, error)
	// Stale asks the source to advance the rejected generation. A source
	// may reload an owner-maintained original or renew its own credential.
	Stale(ctx context.Context, gen uint64) error
}

// SetCredentialSource wires an OAuth credential source: every request
// resolves through it and the static apiKey is ignored.
func (c *Client) SetCredentialSource(s CredentialSource) { c.creds = s }

// credential resolves the request credential: OAuth source when wired,
// else the static key.
func (c *Client) credential(ctx context.Context) (Credential, error) {
	if c.creds != nil {
		cr, err := c.credentialSource(ctx)
		if err != nil {
			return Credential{}, err
		}
		// Record which generation THIS request is presenting. A
		// client-global "last generation" is wrong under concurrency: a
		// delayed 401 from an older request would mark a newer credential
		// stale and reject a credential the source has already advanced.
		if h, ok := ctx.Value(genKey{}).(*atomic.Uint64); ok {
			h.Store(cr.Gen)
		}
		return cr, nil
	}
	return Credential{Token: c.apiKey}, nil
}

// genKey carries the per-request credential generation.
type genKey struct{}

func (c *Client) credentialSource(ctx context.Context) (Credential, error) {
	return c.creds.Credential(ctx)
}

// applyAuth sets the bearer and whatever else the credential requires.
func applyAuth(req *http.Request, cr Credential) {
	for k, v := range cr.Headers {
		if authOwnsHeader(k) {
			continue
		}
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+cr.Token)
}

func authOwnsHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") ||
		strings.EqualFold(name, "x-api-key") ||
		strings.EqualFold(name, "Content-Type")
}

// sendAuthed is doWithRetry plus the credential rule: a 401 or a typed
// 400 credential error means stale, so ask the source to advance once and
// replay. A 403 means valid but not permitted and must never come here — a
// failure would loop forever against a wall (probed 2026-08-20: a ChatGPT
// credential answers 403 on the API platform all day long).
func (c *Client) sendAuthed(ctx context.Context, build func(context.Context) (*http.Request, error)) (*http.Response, error) {
	var used atomic.Uint64
	ctx = context.WithValue(ctx, genKey{}, &used)
	resp, err := c.doWithRetry(ctx, build)
	if err != nil || c.creds == nil {
		return resp, err
	}
	rejectedStatus := resp.StatusCode
	stale := rejectedStatus == http.StatusUnauthorized
	if !stale && resp.StatusCode == http.StatusBadRequest {
		// Some providers report an expired credential as a typed 400.
		// Inspect a bounded prefix, then restore the complete stream when
		// this is an ordinary bad request.
		stream := resp.Body
		body, readErr := io.ReadAll(io.LimitReader(stream, 8<<10))
		if readErr != nil {
			stream.Close()
			return nil, fmt.Errorf("inspect credential rejection: %w", readErr)
		}
		if !bodyIsAuthFailure(body) {
			resp.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), stream), stream}
			return resp, nil
		}
		stale = true
	}
	if !stale {
		return resp, nil
	}
	gen := used.Load()
	if resp.Body != nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}
	if rerr := c.creds.Stale(ctx, gen); rerr != nil {
		return nil, fmt.Errorf("credential rejected (%d) and its source could not advance: %w", rejectedStatus, rerr)
	}
	log.Printf("LLM: credential rejected (%d) — source advanced, replaying once", rejectedStatus)
	return c.doWithRetry(ctx, build)
}

// ClientConfig holds LLM client configuration.
type ClientConfig struct {
	// Credential, when set, replaces APIKey: every request resolves
	// through it (internal/oauth).
	Credential CredentialSource
	Endpoint   string // e.g. "https://api.openai.com/v1"
	APIKey     string // API key
	Model      string // e.g. "gpt-5.2"
	Provider   string // "" / "openai" = OpenAI-compatible; "anthropic" = native
	// ThinkingBudget is the ANTHROPIC extended-thinking budget. It has no
	// OpenAI-compatible equivalent — "thinking_budget" is not a field any
	// OpenAI-shaped provider defines, and we used to send it anyway, so
	// setting it on a zAI or OpenAI entry did exactly nothing. Reasoning
	// on that path is reasoning_effort; anything provider-specific goes
	// through Extra, which exists for precisely that.
	ThinkingBudget int // anthropic dialect only; 0 = server default. Only consulted when ThinkingMode is "budget"
	// ThinkingMode selects which thinking parameter shape to send:
	// "" / "adaptive", "budget" (pre-4.6 only), or "off". See
	// providerEntry.ThinkingMode for why this is data and not a
	// model-name switch.
	ThinkingMode string
	// ThinkingDisplay asks the provider to return readable reasoning:
	// "summarized" or "" (omitted, the vendor default — blocks still
	// arrive, carrying signatures with empty text).
	ThinkingDisplay string
	ReasoningEffort string // OpenAI-compat reasoning_effort ("" = omit)
	MaxOutputTokens int    // Max response tokens (0 = DefaultMaxOutputTokens on the wire — never unbounded)
	MaxInputTokens  int    // Pre-dispatch input ceiling (0 = unchecked test/probe client)
	// AnthropicOAuthBillingText is the provider-configured Claude Code
	// billing block. It is sent only on Anthropic credential requests.
	AnthropicOAuthBillingText string
	Temperature               *float64 // sampling temperature; nil = omit (0 is a valid temperature)
	TopP                      *float64 // nucleus sampling; nil = omit
	// Extra is the OpenAI-path passthrough: merged VERBATIM into the
	// request body top level; typed fields win on collision. Never
	// applied on the anthropic path (typed API).
	Extra          map[string]any
	TimeoutSeconds int // per-request HTTP timeout (0 = 120; grouped with the rest of llm.* — R15: tunables live in config)
	Retries        int // transport-class retries per call (0 = default 1; -1 = none)
	RetryBackoffMS int // pause before each retry (0 = default 2000)
}

func normalizeRetries(n int) int {
	switch {
	case n < 0:
		return 0 // explicit opt-out
	case n == 0:
		return 1 // the default: one bounded retry
	default:
		return n
	}
}

func normalizeBackoff(ms int) time.Duration {
	if ms <= 0 {
		return 2 * time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

// doWithRetry is the ONE door every provider request leaves through. A
// transient transport failure must not cost the identity their turn
// (the getlilac finding, 2026-08-19): we retry ONLY the transport
// class — Do() errors (timeouts included; these calls are
// request/response, never streaming, so nothing has been consumed) and
// 429/5xx statuses. 4xx is the caller's bug and never retries. build
// runs per attempt because request bodies are consumed on send.
func (c *Client) doWithRetry(ctx context.Context, build func(context.Context) (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	var wait time.Duration // server-directed pause (Retry-After), 0 = our own backoff
	var congested bool     // the last failure was the server saying "too busy"
	attempts := 1 + c.retries
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			pause := backoffFor(c.retryBackoff, attempt, congested)
			if wait > 0 {
				pause = wait // the server said when; it knows better than we do
			}
			log.Printf("LLM retry %d/%d in %s after: %v", attempt-1, c.retries, pause, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pause):
			}
		}
		req, err := build(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			// A dead caller context is not a transport flake. The caller
			// (steering, shutdown) abandoned this request; retrying can
			// never succeed and "LLM retry … context canceled" lies
			// about the cause to anyone reading the log.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			congested = false
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			body, truncated, readErr := readBounded(resp.Body, 64<<10)
			if readErr != nil {
				resp.Body.Close()
				return nil, fmt.Errorf("read retryable API response: %w", readErr)
			}
			if truncated {
				body = append(body, []byte(" [truncated]")...)
			}
			// The server knows when to retry better than a fixed backoff;
			// preserve its Retry-After instruction in the typed refusal.
			wait = serverRetryAfter(resp.Header)
			resp.Body.Close()
			httpErr := &HTTPError{
				StatusCode: resp.StatusCode,
				Body:       string(body),
				RetryAfter: wait,
				Retryable:  responseRetryable(resp.StatusCode, resp.Header, body),
			}
			if !httpErr.Retryable {
				return nil, httpErr
			}
			lastErr = httpErr
			congested = true
			continue
		}
		return resp, nil // 2xx and non-retryable statuses go to the caller
	}
	return nil, lastErr
}

func responseRetryable(status int, h http.Header, body []byte) bool {
	if v := strings.TrimSpace(h.Get("x-should-retry")); v != "" {
		return strings.EqualFold(v, "true")
	}
	if serverRetryAfter(h) > 0 || status >= 500 {
		return true
	}
	if status != http.StatusTooManyRequests {
		return false
	}
	var response struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &response) == nil && response.Error.Type == "rate_limit_error"
}

func newHTTPClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	return &http.Client{
		Timeout:       time.Duration(timeoutSeconds) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// New creates a new LLM client.
func New(cfg *ClientConfig) *Client {
	return &Client{
		endpoint:         cfg.Endpoint,
		apiKey:           cfg.APIKey,
		model:            cfg.Model,
		maxInputTokens:   cfg.MaxInputTokens,
		provider:         cfg.Provider,
		oauthBillingText: cfg.AnthropicOAuthBillingText,
		thinkingBudget:   cfg.ThinkingBudget,
		thinkingMode:     cfg.ThinkingMode,
		thinkingDisplay:  cfg.ThinkingDisplay,
		reasoningEffort:  cfg.ReasoningEffort,
		maxOutputTokens:  cfg.MaxOutputTokens,
		temperature:      cfg.Temperature,
		topP:             cfg.TopP,
		extra:            cfg.Extra,
		httpClient:       newHTTPClient(cfg.TimeoutSeconds),
		creds:            cfg.Credential,
		retries:          normalizeRetries(cfg.Retries),
		retryBackoff:     normalizeBackoff(cfg.RetryBackoffMS),
	}
}

// mergeExtra overlays the provider entry's extra passthrough keys onto
// a marshaled OpenAI-compatible request body, at the top level and
// VERBATIM — the treadmill-killer: a provider's newest serving knob
// works the day it ships, no typed field required. Typed fields win on
// collision (warned once per key per client); the anthropic path never
// applies extra (typed API).
func (c *Client) mergeExtra(body []byte) ([]byte, error) {
	if len(c.extra) == 0 {
		return body, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("merge extra: %w", err)
	}
	for k, v := range c.extra {
		if _, taken := m[k]; taken {
			if _, warned := c.extraWarned.LoadOrStore(k, true); !warned {
				log.Printf("LLM: extra key %q collides with a typed request field — the typed value wins", k)
			}
			continue
		}
		m[k] = v
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("merge extra: %w", err)
	}
	return merged, nil
}

// Message is a single chat message.
//
// StableLen (system messages only, never serialized): the cache seam —
// Content[:StableLen] is byte-stable across turns (the composed stable
// prefix). The OpenAI-compatible path ignores it (prefix caching is
// implicit there); the Anthropic path splits the system prompt at the
// seam and marks the stable block with cache_control.
// ThinkingBlock is one reasoning block exactly as the provider returned
// it. The SIGNATURE is the load-bearing half: it is what lets the
// provider accept the block when it is replayed, and it is present even
// when Text is empty (the current default returns thinking with the
// text omitted). Blocks belong to the model that produced them and are
// replayed only within the same turn on the same client.
type ThinkingBlock struct {
	// Kind is the block type as the provider sent it: "thinking" or
	// "redacted_thinking". Replay must use the ORIGINAL type — a
	// redacted block carries opaque Data and no text, and re-sending it
	// as a plain thinking block is a malformed request.
	Kind      string
	Text      string
	Signature string
	Data      string // redacted_thinking only
}

type Message struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StableLen  int        `json:"-"`
	// Thinking carries the assistant turn's reasoning blocks back to the
	// provider on the next iteration. Without it the model re-derives
	// its plan from the transcript after every tool result, which is the
	// whole point of interleaved thinking thrown away. json:"-" because
	// no OpenAI-shaped dialect has a field for it; the anthropic dialect
	// reads the struct directly.
	Thinking []ThinkingBlock `json:"-"`
}

// ToolDefinition describes a tool the LLM can call.
type ToolDefinition struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function schema for a tool.
type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall is a tool invocation from the LLM.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// Choice is a single completion choice.
type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Response is the full API response.
type Response struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
	ModelID string   `json:"-"` // configured model that made this response
}

type modelIDKey struct{}

// WithModelID carries response provenance into identity actions.
func WithModelID(ctx context.Context, modelID string) context.Context {
	if modelID == "" {
		return ctx
	}
	return context.WithValue(ctx, modelIDKey{}, modelID)
}

// ModelIDFromContext returns response provenance, if this action came from a model.
func ModelIDFromContext(ctx context.Context) string {
	modelID, _ := ctx.Value(modelIDKey{}).(string)
	return modelID
}

// DefaultMaxOutputTokens is the output allocation used when a provider
// entry declares none. It lives here, beside the dialects that send it,
// because it is a WIRE fact — and it is exported because the prompt
// budget on the other side of the application must reserve the SAME
// number it will actually be held to. Two copies of it (one budgeting,
// one on the wire) agreed only by coincidence.
const DefaultMaxOutputTokens = 8192

// Usage tracks token consumption.
//
// REPORTED IS THE POINT. Three bare ints made a provider that omits
// usage indistinguishable from one that genuinely spent nothing: both
// decode to {0,0,0} and both look like an answer. Any accounting built
// on that would silently treat unknown as free, which is the worst
// possible direction for a budget to be wrong in. Reported says whether
// the numbers are the provider's or merely the zero value of a struct.
//
// It is never serialized: it is a fact about the response we received,
// not a field any provider sends.
type Usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	Reported         bool `json:"-"`
	// CachedPromptTokens is the SUBSET of PromptTokens that was served
	// from cache. It is a cost fact, not a context fact: cached input
	// occupies the window exactly like fresh input, and bills at roughly
	// a tenth of it. Collapsing the two — which the first version did —
	// measures context correctly and overstates cost by up to an order
	// of magnitude, on a system whose caching is deliberate and heavy.
	//
	// Zero on dialects that do not report it, which is honest: absent is
	// not the same as none, and Reported already carries that.
	CachedPromptTokens int `json:"-"`
}

// UnmarshalJSON records that the provider actually sent a usage object.
// Go calls this ONLY when the key is present, which is exactly the
// distinction being drawn — so the OpenAI-compatible path gets presence
// detection with no work at the call site, and cannot forget to.
func (u *Usage) UnmarshalJSON(b []byte) error {
	type plain Usage // shed the method to avoid infinite recursion
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*u = Usage(p)
	u.Reported = true
	return nil
}

// ChatRequest is the request body sent to the API. Temperature/TopP are
// POINTERS: nil = omit on the wire (server default) — a float64 with
// omitempty could never send the valid value 0.
type ChatRequest struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      string           `json:"tool_choice,omitempty"`
	MaxTokens       int              `json:"max_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

// maxResponseBytes bounds response reads (success and error paths).
// The longest legitimate response is model text bounded by max_output
// tokens (~128k tokens ≈ 512KB text) — 32MB is an order-of-magnitude
// generous ceiling that still stops an unbounded stream from a
// misbehaving endpoint (every other HTTP client in the tree bounds its
// reads; this was the exception — 2026-08-17 review).
const maxResponseBytes = 32 << 20

func readBounded(r io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}

// Chat sends a chat completion request and returns the response,
// speaking the configured provider dialect.
// Chat runs one turn. thinkingBudget is the live per-turn value the
// conversation loop threads through; ZERO means "use what this client was
// configured with", so a client built from config alone — birth, a
// sub-agent, ChatSimple — thinks exactly as the operator configured it
// instead of silently not thinking at all.
// ChatOptions carries the per-request knobs. It is a STRUCT so that the
// next capability is a new field rather than a new parameter on every
// caller, every interface that names this method, and every test fake —
// the churn that made a fifth positional argument look reasonable.
//
// What varies between providers is wire FORMAT, not control flow, so the
// dialects translate these options and the loop above never learns which
// provider it is talking to.
type ChatOptions struct {
	Tools          []ToolDefinition
	ThinkingBudget int

	// RequireTool asks the provider to force a tool call rather than
	// permit a bare text reply. It resolves one ambiguity the wire
	// cannot: text with no tool call is either a finished answer or work
	// announced and not taken, and those are indistinguishable. Forcing
	// a choice makes the model say which, in a channel that cannot be
	// misread. BEST EFFORT — an endpoint that does not honour forcing
	// may answer anyway or reject the request, so callers must have an
	// answer for both.
	RequireTool bool
}

func (c *Client) Chat(ctx context.Context, messages []Message, opts ChatOptions) (resp *Response, err error) {
	defer func() {
		if resp != nil {
			resp.ModelID = c.model
		}
		if err != nil {
			// THE IDENTITY STOPS THINKING HERE, and until now the record
			// showed nothing. A failed call was formatted into a chat
			// message for whoever happened to be watching and logged
			// nowhere: the 400 that blocked every turn on 2026-08-23
			// appears ZERO times across every log file in the identity's
			// history. Anyone diagnosing from the log was reading a
			// channel that could not contain the answer.
			//
			// One line, at the single door every caller leaves through —
			// conversation, facilities, birth and probes alike, whether
			// or not anyone is watching a dashboard. Bounded, because a
			// provider body can be 64KB and a log is not a transcript.
			// But abandonment is not failure. A canceled context means
			// the CALLER chose to end this request — steering replaced
			// the turn, shutdown — the provider never faulted, and "LLM
			// call FAILED" would point diagnosis at the wrong party.
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
				log.Printf("LLM call abandoned (caller ended it): %v", ctxErr)
				return
			}
			log.Printf("LLM call FAILED (%s %s): %s", providerLabel(c.provider), c.model, clip(err.Error(), 500))
		}
	}()
	tools, thinkingBudget := opts.Tools, opts.ThinkingBudget
	if thinkingBudget == 0 {
		thinkingBudget = c.thinkingBudget
		opts.ThinkingBudget = thinkingBudget
	}
	if opts.RequireTool && len(tools) == 0 {
		return nil, fmt.Errorf("RequireTool needs at least one tool to choose from")
	}
	admissionMessages := messages
	if c.provider == "anthropic" && c.creds != nil && c.oauthBillingText != "" {
		admissionMessages = make([]Message, 1, len(messages)+1)
		admissionMessages[0] = Message{Role: "system", Content: c.oauthBillingText}
		admissionMessages = append(admissionMessages, messages...)
	}
	if err := ValidateInput(admissionMessages, tools, c.maxInputTokens); err != nil {
		return nil, err
	}
	if c.provider == "chatgpt" {
		return c.chatGPTResponses(ctx, messages, opts)
	}
	if c.provider == "anthropic" {
		return c.chatAnthropic(ctx, messages, opts)
	}
	toolChoice := ""
	if opts.RequireTool {
		toolChoice = "required"
	}
	req := ChatRequest{
		Model:           c.model,
		Messages:        messages,
		Tools:           tools,
		ToolChoice:      toolChoice,
		ReasoningEffort: c.reasoningEffort,
		MaxTokens:       c.maxOutputTokens,
		Temperature:     c.temperature,
		TopP:            c.topP,
	}
	if req.MaxTokens <= 0 {
		// Same rule the anthropic dialect has always applied: a dialect
		// must never send UNBOUNDED where the entry declares nothing.
		// "0 = server default" let the 2026-08-26 collapse emit a 37KB
		// degenerate reply through exactly this omission. An entry's
		// max_output_tokens overrides.
		req.MaxTokens = DefaultMaxOutputTokens
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if body, err = c.mergeExtra(body); err != nil {
		return nil, err
	}

	httpResp, err := c.sendAuthed(ctx, func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, "POST",
			strings.TrimSuffix(c.endpoint, "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		cred, err := c.credential(ctx)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		applyAuth(httpReq, cred)
		return httpReq, nil
	})
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, truncated, readErr := readBounded(httpResp.Body, 64<<10)
		if readErr != nil {
			return nil, fmt.Errorf("read API error response: %w", readErr)
		}
		if truncated {
			respBody = append(respBody, []byte(" [truncated]")...)
		}
		return nil, fmt.Errorf("API returned %d: %s", httpResp.StatusCode, string(respBody))
	}

	respBody, truncated, err := readBounded(httpResp.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}

	// Debug: log raw response when finish_reason is tool_calls but no tool calls parsed
	var response Response
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("decode response: %w\nbody: %s", err, string(respBody[:min(2000, len(respBody))]))
	}

	// If finish_reason is tool_calls but we got 0, the API response format may have changed
	if len(response.Choices) > 0 && response.Choices[0].FinishReason == "tool_calls" && len(response.Choices[0].Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("tool_calls finish but 0 parsed. Raw: %s", string(respBody[:min(2000, len(respBody))]))
	}

	return &response, nil
}

// ChatSimple sends a system + user message and returns the assistant text
// with the model that produced it.
// Convenience method for simple one-shot queries.
// ChatStructured offers ONE tool and accepts EITHER channel: the tool
// call's arguments when the model called it, the plain text when it did
// not. Both are returned as a payload the caller parses the same way.
//
// This is the shape the cognitive facilities need and the reason they
// could not simply be moved to native tools. SELF_MODEL requires a tool
// call and fails without one. CONSOLIDATE and DREAM deliberately do not:
// "emission-agnostic by design: the local model may have weak
// tool_calls, so the contract is plain JSON the loop parses." That is a
// five-platform constraint, not an oversight, and reversing it would
// make an identity on a weak local substrate unable to consolidate at
// all.
//
// Offering the tool costs nothing on a model that ignores it, and buys
// provider-side schema validation and a payload with no markdown fence
// to strip on one that does not. The capability is never declared,
// stored or probed for — it is simply observed, per call, by which
// channel came back.
func (c *Client) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool ToolDefinition) (payload, modelID string, viaTool bool, err error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}
	resp, err := c.Chat(ctx, messages, ChatOptions{Tools: []ToolDefinition{tool}})
	if err != nil {
		return "", "", false, err
	}
	c.logUnattendedCost(resp)
	if len(resp.Choices) == 0 {
		return "", "", false, fmt.Errorf("no choices in response")
	}
	msg := resp.Choices[0].Message
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name == tool.Function.Name && strings.TrimSpace(tc.Function.Arguments) != "" {
			return tc.Function.Arguments, resp.ModelID, true, nil
		}
	}
	return msg.Content, resp.ModelID, false, nil
}

func (c *Client) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}

	resp, err := c.Chat(ctx, messages, ChatOptions{})
	if err != nil {
		return "", "", err
	}

	// ChatSimple is the door the COGNITIVE FACILITIES leave through —
	// DREAM, CONSOLIDATE and MORNING_BRIEF all arrive here. They run
	// unattended, on alarms, with no operator watching and no
	// conversation loop to account for them, so their spend was the one
	// cost in the system that nobody could see at all. A turn at least
	// had somebody reading the reply.
	//
	// Logged rather than returned: nothing consumes facility usage yet,
	// and widening this signature would churn four call sites, an
	// interface and its fakes to carry a value with no reader. When one
	// exists, it can be returned then.
	//
	// SELF_MODEL is NOT covered here — it needs tool definitions, so it
	// calls Chat directly. Named rather than papered over with a second
	// log site for a single caller.
	c.logUnattendedCost(resp)

	if len(resp.Choices) == 0 {
		return "", "", fmt.Errorf("no choices in response")
	}

	return resp.Choices[0].Message.Content, resp.ModelID, nil
}

// ModelName exposes the configured model (operator surfaces + tests).
func (c *Client) ModelName() string { return c.model }

// serverRetryAfter reads the provider's own guidance. Retry-After is
// either delta-seconds or an HTTP date; anything else, or an absurd
// value, falls back to our configured backoff (0 means "use ours").
func serverRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 || secs > 300 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d <= 0 || d > 5*time.Minute {
			return 0
		}
		return d
	}
	return 0
}

// bodyIsAuthFailure recognises a structured credential error that arrived
// with the wrong status code. Human-readable messages never control refresh.
func bodyIsAuthFailure(body []byte) bool {
	var envelope struct {
		Error json.RawMessage `json:"error"`
		Code  string          `json:"code"`
		Type  string          `json:"type"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	if authFailureCode(envelope.Code) || authFailureCode(envelope.Type) {
		return true
	}
	var code string
	if json.Unmarshal(envelope.Error, &code) == nil {
		return authFailureCode(code)
	}
	var nested struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	return json.Unmarshal(envelope.Error, &nested) == nil &&
		(authFailureCode(nested.Code) || authFailureCode(nested.Type))
}

func authFailureCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_token", "token_expired", "expired_token", "invalid_grant",
		"authentication_error", "invalid_authentication_error", "invalid_api_key", "unauthorized":
		return true
	default:
		return false
	}
}

// providerLabel names the dialect for a log line; the OpenAI-compatible
// path carries no provider string of its own.
func providerLabel(p string) string {
	if p == "" {
		return "openai-compatible"
	}
	return p
}

// clip bounds text going into the log. Provider error bodies are read up
// to 64KB and belong in the operator's error, not in every log line.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… (%d more bytes)", len(s)-n)
}

// congestionFloor is the smallest first pause worth taking when a
// SERVER says it is too busy. An operator picks retry_backoff_ms for
// their own transport — 50ms is right for a local endpoint on a socket
// — but nobody can pick a sensible number for someone else's load, and
// a provider answering 529 needs seconds, not milliseconds.
const congestionFloor = 1500 * time.Millisecond

// backoffFor decides how long to wait before a retry.
//
// Transport hiccups keep the operator's flat pause: the failure was
// local, a fast retry is the right answer, and doubling it would only
// slow down recovery from something already fixed.
//
// SERVER CONGESTION doubles from a floor. Live case, 2026-08-24: a
// substrate probe against Claude hit 529 "Overloaded", retried four
// times at the configured 50ms, exhausted its attempts in a fifth of a
// second, and refused a perfectly good provider — telling the operator
// their credential could not complete a minimal inference request when
// the truth was that Anthropic was briefly busy. Four retries inside
// 200ms is not a retry policy against congestion; it is the same
// request sent five times.
//
// A server-supplied Retry-After still overrides both. It knows.
func backoffFor(base time.Duration, attempt int, congested bool) time.Duration {
	if !congested {
		return base
	}
	pause := base
	if pause < congestionFloor {
		pause = congestionFloor
	}
	for i := 2; i < attempt; i++ { // attempt 2 is the first retry
		pause *= 2
	}
	if pause > 30*time.Second {
		pause = 30 * time.Second
	}
	return pause
}

// logUnattendedCost records what a facility pass spent. Both facility
// doors call it, so a pass that gains a tool does not lose its cost
// record on the way.
func (c *Client) logUnattendedCost(resp *Response) {
	if resp == nil || !resp.Usage.Reported {
		return
	}
	cached := ""
	if resp.Usage.CachedPromptTokens > 0 {
		cached = fmt.Sprintf(", %d cached", resp.Usage.CachedPromptTokens)
	}
	log.Printf("Unattended call cost: %d tokens (in %d, out %d%s) on %s",
		resp.Usage.TotalTokens, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cached, c.model)
}
