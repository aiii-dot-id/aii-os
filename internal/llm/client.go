// .
// .
// .
// .
// .
// .
// .
// .
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// .
type Client struct {
	cache               *CachePolicy
	explicitCache       bool
	maxCompletionTokens bool
	cacheRetentions     []string
	endpoint            string
	apiKey              string
	model               string
	maxInputTokens      int
	maxOutputTokens     int
	provider            string
	oauthBillingText    string
	thinkingBudget      int
	thinkingMode        string
	thinkingDisplay     string
	reasoningEffort     string
	effortLevels        []string
	temperature         *float64
	topP                *float64
	extra               map[string]any
	extraWarned         sync.Map
	creds               CredentialSource
	httpClient          *http.Client
	streamClient        *http.Client
	streamBase          http.RoundTripper
	streamMu            sync.Mutex
	idle                time.Duration
	noStream            bool
	streamMode          atomic.Int32
	retries             int
	retryBackoff        time.Duration
}

// .
// .
type HTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
	Retryable  bool
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("API returned %d: %s", e.StatusCode, e.Body)
}

// .
// .
// .
type Credential struct {
	Token   string
	Headers map[string]string
	Gen     uint64
}

// .
// .
// .
type CredentialSource interface {
	Credential(ctx context.Context) (Credential, error)
	// .
	// .
	Stale(ctx context.Context, gen uint64) error
}

// .
// .
func (c *Client) SetCredentialSource(s CredentialSource) { c.creds = s }

// .
// .
func (c *Client) credential(ctx context.Context) (Credential, error) {
	if c.creds != nil {
		cr, err := c.credentialSource(ctx)
		if err != nil {
			return Credential{}, err
		}
		// .
		// .
		// .
		// .
		if h, ok := ctx.Value(genKey{}).(*atomic.Uint64); ok {
			h.Store(cr.Gen)
		}
		return cr, nil
	}
	return Credential{Token: c.apiKey}, nil
}

// .
type genKey struct{}

func (c *Client) credentialSource(ctx context.Context) (Credential, error) {
	return c.creds.Credential(ctx)
}

// .
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

// .
// .
// .
// .
// .
func (c *Client) sendAuthed(ctx context.Context, build func(context.Context) (*http.Request, error)) (*http.Response, error) {
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
	promptOn := logsink.TapEnabled("llm.prompt")
	returnOn := logsink.TapEnabled("llm.return")
	if promptOn {
		inner := build
		build = func(ctx context.Context) (*http.Request, error) {
			req, berr := inner(ctx)
			if berr == nil {
				tapRequest(ctx, c.model, req, true)
			}
			return req, berr
		}
	}
	var used atomic.Uint64
	ctx = context.WithValue(ctx, genKey{}, &used)
	resp, err := c.doWithRetry(ctx, build)
	if resp != nil {
		resp.Body = tapResponse(ctx, c.model, resp.Body, returnOn)
	}
	if err != nil || c.creds == nil {
		return resp, err
	}
	rejectedStatus := resp.StatusCode
	stale := rejectedStatus == http.StatusUnauthorized
	if !stale && resp.StatusCode == http.StatusBadRequest {
		// .
		// .
		// .
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
	logsink.Warn("llm.refusal", "credential rejected (%d) — source advanced, replaying once", rejectedStatus)
	resp, err = c.doWithRetry(ctx, build)
	if resp != nil {
		resp.Body = tapResponse(ctx, c.model, resp.Body, returnOn)
	}
	return resp, err
}

// .
type ClientConfig struct {
	Cache               *CachePolicy
	ExplicitCache       bool
	MaxCompletionTokens bool
	CacheRetentions     []string
	// .
	// .
	Credential CredentialSource
	Endpoint   string
	APIKey     string
	Model      string
	Provider   string
	// .
	// .
	// .
	// .
	// .
	// .
	ThinkingBudget int
	// .
	// .
	// .
	// .
	ThinkingMode string
	// .
	// .
	// .
	ThinkingDisplay string
	ReasoningEffort string
	// .
	// .
	EffortLevels    []string
	MaxOutputTokens int
	MaxInputTokens  int
	// .
	// .
	AnthropicOAuthBillingText string
	Temperature               *float64
	TopP                      *float64
	// .
	// .
	// .
	Extra          map[string]any
	TimeoutSeconds int
	NoStream       bool
	Retries        int
	RetryBackoffMS int
}

func normalizeRetries(n int) int {
	switch {
	case n < 0:
		return 0
	case n == 0:
		return 1
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
// .
// .
// .
func (c *Client) doWithRetry(ctx context.Context, build func(context.Context) (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	var wait time.Duration
	var congested bool
	attempts := 1 + c.retries
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			pause := backoffFor(c.retryBackoff, attempt, congested)
			if wait > 0 {
				pause = wait
			}
			logsink.Warn("llm.error", "retry %d/%d in %s after: %v", attempt-1, c.retries, pause, lastErr)
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
		record, _ := ctx.Value(attemptKey{}).(*attemptRecord)
		if record != nil {
			record.lastUnknown = false
		}
		resp, err := c.clientFor(ctx).Do(req)
		if err != nil {
			// .
			// .
			// .
			// .
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if record, ok := ctx.Value(attemptKey{}).(*attemptRecord); ok {
				record.unknown++
				record.lastUnknown = true
			}
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			congested = false
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if record != nil && resp.StatusCode >= 500 {
				record.unknown++
				record.lastUnknown = true
			}
			body, truncated, readErr := readBounded(resp.Body, 64<<10)
			if readErr != nil {
				resp.Body.Close()
				return nil, fmt.Errorf("read retryable API response: %w", readErr)
			}
			if truncated {
				body = append(body, []byte(" [truncated]")...)
			}
			// .
			// .
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
		return resp, nil
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
	return &http.Client{
		Timeout:       idleCeiling(timeoutSeconds),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// .
// .
func idleCeiling(timeoutSeconds int) time.Duration {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	return time.Duration(timeoutSeconds) * time.Second
}

// .
func New(cfg *ClientConfig) *Client {
	var policy *CachePolicy
	if cfg.Cache != nil {
		value := *cfg.Cache
		policy = &value
	}
	return &Client{
		cache: policy, explicitCache: cfg.ExplicitCache, maxCompletionTokens: cfg.MaxCompletionTokens, cacheRetentions: slices.Clone(cfg.CacheRetentions),
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
		effortLevels:     cfg.EffortLevels,
		maxOutputTokens:  cfg.MaxOutputTokens,
		temperature:      cfg.Temperature,
		topP:             cfg.TopP,
		extra:            cfg.Extra,
		httpClient:       newHTTPClient(cfg.TimeoutSeconds),
		idle:             idleCeiling(cfg.TimeoutSeconds),
		noStream:         cfg.NoStream,
		creds:            cfg.Credential,
		retries:          normalizeRetries(cfg.Retries),
		retryBackoff:     normalizeBackoff(cfg.RetryBackoffMS),
	}
}

// .
// .
// .
// .
// .
// .
func (c *Client) mergeExtra(body []byte) ([]byte, error) {
	if len(c.extra) == 0 {
		return body, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("merge extra: %w", err)
	}
	for k, v := range c.extra {
		if (k == "max_tokens" && c.maxCompletionTokens) || (k == "max_completion_tokens" && !c.maxCompletionTokens) {
			if _, warned := c.extraWarned.LoadOrStore(k, true); !warned {
				logsink.Warn("llm.refusal", "extra key %q cannot override the configured output allocation", k)
			}
			continue
		}
		if _, taken := m[k]; taken {
			if _, warned := c.extraWarned.LoadOrStore(k, true); !warned {
				logsink.Warn("llm.refusal", "extra key %q collides with a typed request field — the typed value wins", k)
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
type ThinkingBlock struct {
	// .
	// .
	// .
	// .
	Kind      string
	Text      string
	Signature string
	Data      string
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StableLen  int        `json:"-"`
	// .
	// .
	// .
	// .
	CacheBefore bool `json:"-"`
	// .
	// .
	// .
	// .
	// .
	// .
	Thinking []ThinkingBlock `json:"-"`
	// .
	// .
	NativeContent json.RawMessage `json:"-"`
	NativeDialect string          `json:"-"`
	// .
	// .
	OutputTokens int `json:"-"`
}

// .
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// .
type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// .
type ToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`
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
	EmissionOrdinal int `json:"-"`
	Function        struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// .
type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// .
type Response struct {
	Choices   []Choice `json:"choices"`
	Usage     Usage    `json:"usage"`
	ModelID   string   `json:"-"`
	ID        string   `json:"id,omitempty"`
	RequestID string   `json:"-"`
	// .
	// .
	// .
	CallID      string          `json:"-"`
	Diagnostics json.RawMessage `json:"-"`
	Duration    time.Duration   `json:"-"`
}

type modelIDKey struct{}

// .
func WithModelID(ctx context.Context, modelID string) context.Context {
	if modelID == "" {
		return ctx
	}
	return context.WithValue(ctx, modelIDKey{}, modelID)
}

// .
func ModelIDFromContext(ctx context.Context) string {
	modelID, _ := ctx.Value(modelIDKey{}).(string)
	return modelID
}

// .
// .
// .
// .
// .
// .
const DefaultMaxOutputTokens = 8192

// .
// .
// .
type ChatRequest struct {
	Model               string           `json:"model"`
	Messages            []Message        `json:"messages"`
	Tools               []ToolDefinition `json:"tools,omitempty"`
	ToolChoice          string           `json:"tool_choice,omitempty"`
	MaxTokens           int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Temperature         *float64         `json:"temperature,omitempty"`
	TopP                *float64         `json:"top_p,omitempty"`
	ReasoningEffort     string           `json:"reasoning_effort,omitempty"`
}

// .
// .
// .
// .
// .
// .
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
type ChatOptions struct {
	Tools          []ToolDefinition
	ThinkingBudget int

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	RequireTool        bool
	DisableTools       bool
	PreviousResponseID string
}

func (c *Client) Chat(ctx context.Context, messages []Message, opts ChatOptions) (resp *Response, err error) {
	started := time.Now()
	attempts := &attemptRecord{}
	ctx = context.WithValue(ctx, attemptKey{}, attempts)
	admittedAt := 0
	defer func() {
		c.warnIfUnderCounted(admittedAt, resp)
		if resp != nil {
			resp.Duration = time.Since(started)
			resp.Usage.UnknownAttempts += attempts.unknown
			if resp.Usage.Problem == UsageInvalid && err == nil {
				err = &UsageError{}
			}
			if (c.cache != nil && c.cache.Diagnostics) || resp.Usage.Problem == UsageInvalid {
				logsink.Debug("llm.return", "cache receipt: model=%q request_id=%q response_id=%q elapsed_ms=%d input=%d read=%d write=%d usage_reported=%t problem=%q diagnostics=%q", c.model, resp.RequestID, resp.ID, resp.Duration.Milliseconds(), resp.Usage.PromptTokens, resp.Usage.CachedPromptTokens, resp.Usage.CacheWriteTokens, resp.Usage.Reported, resp.Usage.Problem, cacheDiagnosticState(resp.Diagnostics, opts.PreviousResponseID))
			}
		}
		if err != nil {
			unknown := attempts.unknown
			if attempts.lastUnknown && unknown > 0 {
				unknown--
			}
			err = &CallError{Err: err, UnknownAttempts: unknown}
		}
	}()
	defer func() {
		if resp != nil {
			resp.ModelID = c.model
		}
		if err != nil {
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
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
				logsink.Info("llm.refusal", "call abandoned (caller ended it): %v", ctxErr)
				return
			}
			logsink.Error("llm.error", "call FAILED (%s %s): %s", providerLabel(c.provider), c.model, clip(err.Error(), 500))
		}
	}()
	tools, thinkingBudget := opts.Tools, opts.ThinkingBudget
	if _, err := c.cachePolicy(); err != nil {
		return nil, err
	}
	if thinkingBudget == 0 {
		thinkingBudget = c.thinkingBudget
		opts.ThinkingBudget = thinkingBudget
	}
	if opts.RequireTool && opts.DisableTools {
		return nil, fmt.Errorf("tool choice cannot require and disable tools")
	}
	if opts.RequireTool && len(tools) == 0 {
		return nil, fmt.Errorf("RequireTool needs at least one tool to choose from")
	}
	admittedAt, err = c.admit(messages, tools)
	if err != nil {
		return nil, err
	}
	// .
	// .
	// .
	callID := newCallID()
	ctx = withCallID(ctx, callID)
	if c.provider == "chatgpt" {
		r, rerr := c.chatGPTResponses(ctx, messages, opts)
		if r != nil {
			r.CallID = callID
		}
		return r, rerr
	}
	if c.provider == "anthropic" {
		r, rerr := c.chatAnthropic(ctx, messages, opts)
		if r != nil {
			r.CallID = callID
		}
		return r, rerr
	}
	toolChoice := ""
	if opts.DisableTools {
		toolChoice = "none"
	}
	if opts.RequireTool {
		toolChoice = "required"
	}
	req := ChatRequest{
		Model:           c.model,
		Messages:        messages,
		Tools:           tools,
		ToolChoice:      toolChoice,
		ReasoningEffort: c.effortPlan().Wire,
		MaxTokens:       c.maxOutputTokens,
		Temperature:     c.temperature,
		TopP:            c.topP,
	}
	if req.MaxTokens <= 0 {
		// .
		// .
		// .
		// .
		// .
		req.MaxTokens = DefaultMaxOutputTokens
	}

	if c.maxCompletionTokens {
		req.MaxCompletionTokens = req.MaxTokens
		req.MaxTokens = 0
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if body, err = c.mergeExtra(body); err != nil {
		return nil, err
	}

	body, err = c.applyOpenAICache(body, messages)
	if err != nil {
		return nil, err
	}
	// .
	// .
	// .
	for c.streams() {
		resp, fallback, serr := c.chatOpenAIStream(ctx, body)
		if !fallback {
			if resp != nil {
				resp.CallID = callID
			}
			return resp, serr
		}
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

	// .
	// .
	// .
	response, err := decodeCompletion(httpResp.Body)
	if err != nil {
		return nil, err
	}
	response.RequestID = httpResp.Header.Get("x-request-id")
	response.CallID = callID
	return response, nil
}

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
func (c *Client) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool ToolDefinition) (payload, modelID string, viaTool bool, err error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt, StableLen: StablePrefix(ctx)},
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
	if err := Finished(resp.Choices[0]); err != nil {
		return "", "", false, err
	}
	msg := resp.Choices[0].Message
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name == tool.Function.Name && strings.TrimSpace(tc.Function.Arguments) != "" {
			return tc.Function.Arguments, resp.ModelID, true, nil
		}
	}
	return msg.Content, resp.ModelID, false, nil
}

// .
// .
// .
// .
// .
type IncompleteResponseError struct {
	Reason string
	Got    int
}

func (e *IncompleteResponseError) Error() string {
	why := "the provider ended it with " + strconv.Quote(e.Reason)
	switch e.Reason {
	case "length":
		why = "it was cut off at the output limit"
	case "refusal":
		why = "the provider declined the request"
	}
	return fmt.Sprintf("the model did not finish its reply — %s (%d characters had arrived): an unfinished reply is not a product", why, e.Got)
}

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
func Finished(c Choice) error {
	switch c.FinishReason {
	case "", "stop":
		return nil
	case "tool_calls":
		if len(c.Message.ToolCalls) > 0 {
			return nil
		}
	}
	return &IncompleteResponseError{Reason: c.FinishReason, Got: utf8.RuneCountInString(c.Message.Content)}
}

// .
// .
// .
// .
// .
func (c *Client) admit(messages []Message, tools []ToolDefinition) (required int, err error) {
	admissionMessages := messages
	if c.provider == "anthropic" && c.creds != nil && c.oauthBillingText != "" {
		admissionMessages = make([]Message, 1, len(messages)+1)
		admissionMessages[0] = Message{Role: "system", Content: c.oauthBillingText}
		admissionMessages = append(admissionMessages, messages...)
	}
	return AdmitInput(admissionMessages, tools, c.maxInputTokens)
}

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
func (c *Client) warnIfUnderCounted(admittedAt int, resp *Response) {
	if admittedAt <= 0 || resp == nil || !resp.Usage.Reported || resp.Usage.PromptTokens <= admittedAt {
		return
	}
	logsink.Warn("llm.budget", "the provider counted %d input tokens for a request admitted at an estimate of %d (%s %s): the tokenizer-free estimate UNDER-counts for this model, so the input ceiling of %d is not protecting it — lower the model's input ceiling in config by at least that ratio",
		resp.Usage.PromptTokens, admittedAt, providerLabel(c.provider), c.model, c.maxInputTokens)
}

// .
func simpleMessages(ctx context.Context, systemPrompt, userMessage string) []Message {
	return []Message{
		{Role: "system", Content: systemPrompt, StableLen: StablePrefix(ctx)},
		{Role: "user", Content: userMessage},
	}
}

// .
// .
// .
// .
// .
// .
func (c *Client) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	_, err := c.admit(simpleMessages(ctx, systemPrompt, userMessage), nil)
	return err
}

func (c *Client) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	messages := simpleMessages(ctx, systemPrompt, userMessage)

	resp, err := c.Chat(ctx, messages, ChatOptions{})
	if err != nil {
		return "", "", err
	}

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
	c.logUnattendedCost(resp)

	if len(resp.Choices) == 0 {
		return "", "", fmt.Errorf("no choices in response")
	}
	if err := Finished(resp.Choices[0]); err != nil {
		return "", "", err
	}

	return resp.Choices[0].Message.Content, resp.ModelID, nil
}

// .
func (c *Client) ModelName() string { return c.model }

// .
// .
// .
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

// .
// .
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

// .
// .
func providerLabel(p string) string {
	if p == "" {
		return "openai-compatible"
	}
	return p
}

// .
// .
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… (%d more bytes)", len(s)-n)
}

// .
// .
// .
// .
// .
const congestionFloor = 1500 * time.Millisecond

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
func backoffFor(base time.Duration, attempt int, congested bool) time.Duration {
	if !congested {
		return base
	}
	pause := base
	if pause < congestionFloor {
		pause = congestionFloor
	}
	for i := 2; i < attempt; i++ {
		pause *= 2
	}
	if pause > 30*time.Second {
		pause = 30 * time.Second
	}
	return pause
}

// .
// .
// .
func (c *Client) logUnattendedCost(resp *Response) {
	if resp == nil || !resp.Usage.Reported {
		return
	}
	cached := ""
	if resp.Usage.CachedPromptTokens > 0 {
		cached = fmt.Sprintf(", %d cached", resp.Usage.CachedPromptTokens)
	}
	logsink.Info("llm.budget", "unattended call cost: %d tokens (in %d, out %d%s) on %s",
		resp.Usage.TotalTokens, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cached, c.model)
}

// .
// .
// .
// .
func (c *Client) effortPlan() WirePlan {
	return PlanEffort(DialectFor(c.provider), c.reasoningEffort, c.effortLevels)
}

// .
func (c *Client) ModelContractMatches(explicit, modernOutput bool, retentions []string) bool {
	if c.explicitCache != explicit || c.maxCompletionTokens != modernOutput || (c.cacheRetentions == nil) != (retentions == nil) || len(c.cacheRetentions) != len(retentions) {
		return false
	}
	for i := range retentions {
		if c.cacheRetentions[i] != retentions[i] {
			return false
		}
	}
	return true
}
