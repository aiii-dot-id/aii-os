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
package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .

const (
	// .
	// .
	// .
	// .
	methodInvokeCall = "invoke-call"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	opHTTPGet = "http.get"
	opKVPut   = "kv.put"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	opHTTPPost   = "http.post"
	opHTTPPut    = "http.put"
	opHTTPPatch  = "http.patch"
	opHTTPDelete = "http.delete"

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
	opHTTPRead  = "http.read"
	opHTTPClose = "http.close"
	opKVGet     = "kv.get"
	opKVDelete  = "kv.delete"

	// .
	opVoiceObserve = "voice.observe"

	// .
	opKVList = "kv.list"
	// .
	// .
	opEmbeddingsCreate = "embeddings.create"
	// .
	// .
	// .
	// .
	opSettingsGet = "settings.get"
	// .
	// .
	// .
	// .
	// .
	opToolsPublish  = "tools.publish"
	opToolsWithdraw = "tools.withdraw"
	// .
	// .
	capToolsPublish = "tools.publish"

	// .
	// .
	// .
	// .
	capNetOutbound = "net.outbound"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	capNetLocal = "net.local"

	// .
	// .
	// .
	// .
	// .
	capRing4KV = "ring4.kv"

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
	capVoiceObserve = "voice.observe"

	// .
	// .
	// .
	// .
	capModelEmbeddings = "model.embeddings"
)

// .
// .
// .
// .
const (
	// .
	maxUtteranceBytes = 16 << 10
	// .
	// .
	maxSpeakerLabelBytes = 64

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
	reasonNotInEnvelope = "CAPABILITY_NOT_IN_STATIC_ENVELOPE"
	reasonPolicyDeny    = "POLICY_DENY"
	reasonTierDenied    = "capability_policy_denied"

	// .
	// .
	// .
	// .
	reasonOpNotAllowed = "OPERATION_NOT_ALLOWED_FOR_CAPABILITY"

	// .
	reasonTargetInvalid      = "OPERATION_TARGET_INVALID"
	reasonArgumentInvalid    = "OPERATION_ARGUMENT_INVALID"
	reasonNetUnknownArgument = "NET_UNKNOWN_ARGUMENT"
	reasonNetResponseTooBig  = "NET_RESPONSE_TOO_LARGE"
	reasonNetRemoteFailed    = "NET_REMOTE_OUTCOME_FAILED"
	reasonNetTimeoutLimit    = "NET_TIMEOUT_LIMIT_EXCEEDED"
	reasonAuthInvalid        = "NET_AUTH_PROFILE_INVALID"
	reasonAuthUnavailable    = "NET_AUTH_PROFILE_UNAVAILABLE"
	reasonAuthScopeMismatch  = "NET_AUTH_PROFILE_SCOPE_MISMATCH"
	reasonAuthSecretMissing  = "NET_AUTH_PROFILE_SECRET_UNAVAILABLE"
	reasonAuthNotAdmitted    = "NET_AUTH_PROFILE_NOT_ADMITTED"
	reasonAuthRequiresHTTPS  = "NET_AUTH_PROFILE_REQUIRES_HTTPS"
	// .
	// .
	// .
	reasonAuthDisconnected = "NET_AUTH_PROFILE_DISCONNECTED"
	reasonNetHeaderInvalid = "NET_HEADER_INVALID"
	// .
	// .
	// .
	// .
	// .
	reasonNetEffectUnknown = "NET_EFFECT_UNKNOWN"
	// .
	// .
	reasonNetRequestTooBig = "NET_REQUEST_TOO_LARGE"

	// .
	// .
	reasonKVNotFound      = "KV_NOT_FOUND"
	reasonKVValueTooLarge = "KV_VALUE_TOO_LARGE"
	reasonKVQuotaExceeded = "KV_QUOTA_EXCEEDED"

	// .
	// .
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusDenied    = "denied"
	deniedAtCapEval = "capability_evaluation"

	// .
	// .
	// .
	// .
	bearerPrefix = "Bearer "
)

// .
// .
// .
// .
const (
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	DefaultMaxResponseBytes = 768 << 10

	// .
	// .
	// .
	// .
	// .
	DefaultMaxRequestBytes = 256 << 10
	// .
	// .
	MaxRequestHeaders     = 16
	MaxRequestHeaderBytes = 4096

	// .
	// .
	// .
	MaxStreamChunkBytes = 64 << 10
	// .
	// .
	// .
	// .
	// .
	DefaultMaxStreamBytes = 8 << 20
	// .
	MaxOpenStreams = 4

	// .
	// .
	// .
	DefaultHTTPTimeout = 10 * time.Second
	MaxHTTPTimeout     = 60 * time.Second

	// .
	// .
	// .
	DefaultMaxKVKeyBytes = 256

	// .
	// .
	// .
	// .
	DefaultMaxKVValueBytes = 64 << 10

	// .
	// .
	// .
	DefaultMaxKVKeys = 256

	// .
	// .
	// .
	DefaultMaxKVTotalBytes = 1 << 20
)

// .
// .
// .
// .
type Grant struct {
	// .
	// .
	// .
	// .
	// .
	Voice bool `json:"voice,omitempty"`
	// .
	// .
	// .
	// .
	KV bool `json:"kv,omitempty"`
	// .
	// .
	// .
	Hosts []string `json:"hosts,omitempty"`
	// .
	// .
	// .
	Local []string `json:"local,omitempty"`
	// .
	// .
	// .
	PlaintextCredentials bool `json:"plaintext_credentials,omitempty"`
	// .
	// .
	// .
	// .
	// .
	AutoConfirm []string `json:"auto_confirm,omitempty"`
	// .
	// .
	// .
	// .
	ReadOnly bool `json:"read_only,omitempty"`
	// .
	// .
	// .
	CredentialHandles []string `json:"credential_handles,omitempty"`
	// .
	// .
	// .
	// .
	// .
	Embeddings bool `json:"embeddings,omitempty"`
	// .
	// .
	// .
	// .
	// .
	Memory bool `json:"memory,omitempty"`
	// .
	// .
	// .
	Roots []RootGrant `json:"roots,omitempty"`
	// .
	// .
	// .
	Tools bool `json:"tools,omitempty"`
}

// .
// .
// .
func (g Grant) isEmpty() bool {
	return !g.Voice && !g.KV && !g.Embeddings && !g.Memory && !g.Tools && len(g.Hosts) == 0 && len(g.Local) == 0 && len(g.CredentialHandles) == 0 && len(g.Roots) == 0
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
type AuthProfile struct {
	// .
	// .
	SecretEnv  string `json:"secret_env,omitempty"`
	SecretFile string `json:"secret_file,omitempty"`
	// .
	// .
	// .
	Host string `json:"host"`
	Port int    `json:"port"`
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
	Scheme string `json:"scheme,omitempty"`

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Provider         string   `json:"provider,omitempty"`
	ClientID         string   `json:"client_id,omitempty"`
	ClientSecretFile string   `json:"client_secret_file,omitempty"`
	ClientSecretEnv  string   `json:"client_secret_env,omitempty"`
	Hosts            []string `json:"hosts,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	TokenFile        string   `json:"token_file,omitempty"`
	AuthorizeURL     string   `json:"authorize_url,omitempty"`
	TokenURL         string   `json:"token_url,omitempty"`
	DeviceURL        string   `json:"device_url,omitempty"`
	RevokeURL        string   `json:"revoke_url,omitempty"`
	RedirectURI      string   `json:"redirect_uri,omitempty"`
}

// .
// .
const SchemeOAuth2 = "oauth2"

// .
// .
// .
func (p AuthProfile) Contract() (oauth.OAuthParams, oauth.Provider, error) {
	var tpl oauth.Provider
	if p.Provider != "" && p.Provider != "custom" {
		t, ok := oauth.ProviderTemplate(p.Provider)
		if !ok {
			return oauth.OAuthParams{}, oauth.Provider{}, fmt.Errorf("auth profile names provider %q, which this host has no template for (%s, or custom with the endpoints inline)", p.Provider, strings.Join(oauth.ProviderNames(), ", "))
		}
		tpl = t
	}
	params := oauth.OAuthParams{ClientID: p.ClientID, AuthorizeURL: tpl.AuthorizeURL, TokenURL: tpl.TokenURL, RedirectURI: p.RedirectURI,
		Scope: strings.Join(append(append([]string(nil), tpl.BaseScopes...), p.Scopes...), " "), AuthorizeParams: tpl.AuthorizeParams}
	if p.AuthorizeURL != "" {
		params.AuthorizeURL = p.AuthorizeURL
	}
	if p.TokenURL != "" {
		params.TokenURL = p.TokenURL
	}
	if p.DeviceURL != "" {
		tpl.DeviceURL = p.DeviceURL
	}
	if p.RevokeURL != "" {
		tpl.RevokeURL = p.RevokeURL
	}
	if len(p.Hosts) > 0 {
		tpl.Hosts = append([]string(nil), p.Hosts...)
	}
	if params.TokenURL == "" || p.ClientID == "" || p.TokenFile == "" || len(tpl.Hosts) == 0 {
		return oauth.OAuthParams{}, oauth.Provider{}, errors.New("an oauth2 auth profile needs a provider (or custom endpoints), a client_id, a token_file and at least one host")
	}
	return params, tpl, nil
}

// .
// .
type Config struct {
	// .
	Store *store.Store
	// .
	// .
	// .
	// .
	Voice VoiceObserver
	// .
	// .
	// .
	Embed Embedder
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
	InSAFE func() bool
	// .
	// .
	// .
	// .
	Grants map[string]Grant
	// .
	// .
	AuthProfiles map[string]AuthProfile
	// .
	// .
	// .
	ObserveFetch func(url string)
	// .
	// .
	// .
	// .
	Guard func(ctx context.Context, rawURL string) error
	// .
	// .
	// .
	// .
	OwnListener func(ip net.IP, port int) bool
	// .
	// .
	Transport http.RoundTripper

	// .
	MaxResponseBytes int
	MaxRequestBytes  int
	MaxStreamBytes   int
	MaxFilesBytes    int

	// .
	// .
	// .
	// .
	ProtectedPaths  []string
	MaxKVKeyBytes   int
	MaxKVValueBytes int
	MaxKVKeys       int
	MaxKVTotalBytes int
	// .
	// .
	MaxMemoryTextBytes int
	MaxMemories        int
}

func (c Config) maxResponseBytes() int {
	if c.MaxResponseBytes > 0 {
		return c.MaxResponseBytes
	}
	return DefaultMaxResponseBytes
}

func (c *Config) maxRequestBytes() int {
	if c.MaxRequestBytes > 0 {
		return c.MaxRequestBytes
	}
	return DefaultMaxRequestBytes
}

func (c *Config) maxStreamBytes() int {
	if c.MaxStreamBytes > 0 {
		return c.MaxStreamBytes
	}
	return DefaultMaxStreamBytes
}
func (c Config) maxKVKeyBytes() int {
	if c.MaxKVKeyBytes > 0 {
		return c.MaxKVKeyBytes
	}
	return DefaultMaxKVKeyBytes
}
func (c Config) maxKVValueBytes() int {
	if c.MaxKVValueBytes > 0 {
		return c.MaxKVValueBytes
	}
	return DefaultMaxKVValueBytes
}
func (c Config) maxKVKeys() int {
	if c.MaxKVKeys > 0 {
		return c.MaxKVKeys
	}
	return DefaultMaxKVKeys
}
func (c Config) maxKVTotalBytes() int {
	if c.MaxKVTotalBytes > 0 {
		return c.MaxKVTotalBytes
	}
	return DefaultMaxKVTotalBytes
}

// .
// .
type Host struct {
	cfg Config
	// .
	// .
	instruments *memory.Facility
	counter     atomic.Uint64

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
	policyMu sync.RWMutex
	// .
	// .
	// .
	publishMu    sync.Mutex
	publishLocks map[string]*sync.Mutex
	policy       policySnapshot
	// .
	// .
	// .
	// .
	oauthMu  sync.Mutex
	oauthSrc map[string]*oauth.Source
	// .
	// .
	// .
	afterSnapshot func()
}

// .
// .
// .
// .
func New(cfg Config) (*Host, error) {
	if cfg.Store == nil {
		return nil, errors.New("broker: refusing to build without a store — effects without host-authored receipts are the A3 hole")
	}
	return &Host{cfg: cfg, instruments: memory.New(cfg.Store), policy: policySnapshot{grants: cfg.Grants, profiles: cfg.AuthProfiles}}, nil
}

// .
// .
// .
// .
// .
// .
// .
func (h *Host) Bind(pluginID string, tier packagefmt.Tier, envelope []string) *Binding {
	return h.BindRelease(pluginID, tier, envelope, Release{})
}

// .
// .
// .
// .
// .
type Release struct {
	Version     string
	PackageHash string
}

// .
func (h *Host) BindRelease(pluginID string, tier packagefmt.Tier, envelope []string, release Release) *Binding {
	if h == nil {
		return nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	return &Binding{host: h, pluginID: pluginID, tier: tier, envelope: envelope, release: release}
}

// .
// .
// .
// .
type Binding struct {
	host     *Host
	pluginID string
	tier     packagefmt.Tier
	envelope []string
	release  Release
	// .
	// .
	// .
	scope atomic.Pointer[OperationScope]
	// .
	// .
	// .
	settings func() map[string]interface{}
	// .
	// .
	// .
	streamsMu sync.Mutex
	streams   map[string]*httpStream
	streamSeq uint64
	streamCap int
	// .
	// .
	// .
	privateDir  string
	filesCap    int
	filesMu     sync.Mutex
	rootWritten int
	// .
	// .
	publisher Publisher
	// .
	// .
	closed atomic.Bool
}

// .
type policySnapshot struct {
	grants   map[string]Grant
	profiles map[string]AuthProfile
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
func (h *Host) ReplacePolicy(grants map[string]Grant, profiles map[string]AuthProfile) {
	if h == nil {
		return
	}
	h.policyMu.Lock()
	h.policy = policySnapshot{grants: grants, profiles: profiles}
	h.oauthMu.Lock()
	h.oauthSrc = nil
	h.oauthMu.Unlock()
	h.policyMu.Unlock()
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
func (h *Host) snapshot() policySnapshot {
	h.policyMu.RLock()
	defer h.policyMu.RUnlock()
	return h.policy
}

// .
// .
func (s policySnapshot) grant(pluginID string) Grant { return s.grants[pluginID] }

// .
// .
// .
// .
// .
// .
// .
// .
func (b *Binding) ClearTempScope() error {
	if b == nil {
		return nil
	}
	if err := b.host.cfg.Store.PluginKVClearTemp(b.pluginID); err != nil {
		return err
	}
	return b.host.cfg.Store.PluginMemoryClearTemp(b.pluginID)
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
func (b *Binding) Close() error {
	if b == nil {
		return nil
	}
	b.closed.Store(true)
	b.endStreams("closed with the activation")
	b.clearTempFiles()
	if err := b.host.cfg.Store.PluginKVClearTemp(b.pluginID); err != nil {
		return err
	}
	return b.host.cfg.Store.PluginMemoryClearTemp(b.pluginID)
}

// .
// .
// .
func (b *Binding) SetStreamCap(n int) {
	if b == nil {
		return
	}
	b.streamCap = n
}

// .
func (b *Binding) PluginID() string { return b.pluginID }

// .

type rpcError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *errorData `json:"data,omitempty"`
}

type errorData struct {
	ReasonCode string `json:"reasonCode"`
	DeniedAt   string `json:"denied_at,omitempty"`
}

// .
// .
// .
// .
type invokeParams struct {
	Operation           string          `json:"operation"`
	PluginOperation     string          `json:"plugin_operation"`
	Target              json.RawMessage `json:"target"`
	Arguments           json.RawMessage `json:"arguments"`
	WorkDoneToken       json.RawMessage `json:"work_done_token"`
	Grant               json.RawMessage `json:"grant"`
	ParentRuntimeCallID string          `json:"parent_runtime_call_id"`
}

// .
// .
// .
// .
// .
// .
// .
type externalReceipt struct {
	Success          bool   `json:"success"`
	TransportOutcome bool   `json:"transport_outcome"`
	ProtocolStatus   *int   `json:"protocol_status"`
	OperationOutcome bool   `json:"operation_outcome"`
	AuditPersisted   bool   `json:"audit_persisted"`
	HostAuthored     bool   `json:"host_authored"`
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	PluginID         string `json:"plugin_id"`
	PluginVersion    string `json:"plugin_version,omitempty"`
	PackageHash      string `json:"package_hash,omitempty"`
	Tier             string `json:"tier"`
	Operation        string `json:"operation"`
	PluginOperation  string `json:"plugin_operation,omitempty"`
	Target           string `json:"target"`
	Detail           string `json:"detail,omitempty"`
	// .
	// .
	// .
	// .
	// .
	Method string `json:"method,omitempty"`
	Effect string `json:"effect,omitempty"`
}

// .
const (
	EffectPerformed    = "performed"
	EffectNotPerformed = "not-performed"
	EffectUnknown      = "unknown"
)

// .
// .
// .
// .
// .
const (
	EffectsReadInternal  = "read.internal"
	EffectsReadExternal  = "read.external"
	EffectsWriteLocal    = "write.local"
	EffectsWriteExternal = "write.external"
	EffectsExec          = "exec"
)

// .
// .
// .
func KnownEffects(s string) bool {
	switch s {
	case "", EffectsReadInternal, EffectsReadExternal, EffectsWriteLocal, EffectsWriteExternal, EffectsExec:
		return true
	}
	return false
}

// .
// .
// .
// .
// .
// .
// .
type OperationScope struct {
	Operation    string
	Effects      string
	Capabilities []string
	Declared     bool
}

// .
// .
// .
func (b *Binding) BeginOperation(s OperationScope) {
	if b == nil {
		return
	}
	b.scope.Store(&s)
}

// .
func (b *Binding) EndOperation() {
	if b == nil {
		return
	}
	b.scope.Store(nil)
	// .
	// .
	// .
	b.endStreams("closed at the operation's end")
}

// .
// .
func asksSettings(method string, params []byte) bool {
	if method != methodInvokeCall {
		return false
	}
	var p struct {
		Operation string `json:"operation"`
		Target    struct {
			Root string `json:"root"`
		} `json:"target"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	// .
	// .
	// .
	// .
	return p.Operation == opSettingsGet || (strings.HasPrefix(p.Operation, "fs.") && p.Target.Root == PrivateRoot)
}

// .
// .
// .
// .
type PublishedTool struct {
	Name         string                 `json:"name"`
	Summary      string                 `json:"summary"`
	Input        map[string]interface{} `json:"input"`
	Effects      string                 `json:"effects"`
	Capabilities []string               `json:"capabilities"`
	Family       string                 `json:"family,omitempty"`
}

// .
// .
type Publisher interface {
	PublishTool(spec PublishedTool) (registryName string, err error)
	WithdrawTool(name string) error
}

// .
// .
func (b *Binding) SetPublisher(p Publisher) {
	if b == nil {
		return
	}
	b.publisher = p
}

// .
// .
// .
// .
func (b *Binding) dispatchTools(p invokeParams, grant Grant) ([]byte, error) {
	if !b.tier.PublisherProven() {
		return errorReply(-32000,
			fmt.Sprintf("tier %s holds no %s ceiling (a plugin below a publisher-proven tier cannot grow the identity's tools)", b.tier, capToolsPublish),
			&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
	}
	if !b.envelopeHas(capToolsPublish) {
		return errorReply(-32000, capToolsPublish+" is not in the signed capability envelope",
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !grant.Tools {
		return errorReply(-32000, fmt.Sprintf("no operator grant lets plugin %s publish tools (plugins.grants.%s.tools)", b.pluginID, b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if r, denied := b.scopeDenies(p.Operation, capToolsPublish); denied {
		return r, nil
	}
	if b.publisher == nil {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonPolicyDeny, detail: "this activation publishes no tools"})
	}
	if p.Operation == opToolsWithdraw {
		var target struct {
			Name string `json:"name"`
		}
		if len(p.Target) > 0 {
			_ = json.Unmarshal(p.Target, &target)
		}
		if target.Name == "" {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonTargetInvalid, detail: "tools.withdraw requires target.name"})
		}
		if err := b.publisher.WithdrawTool(target.Name); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.Name, outcome{status: statusFailed, reason: reasonTargetInvalid, transportOK: true, detail: err.Error()})
		}
		or, _ := json.Marshal(map[string]interface{}{"name": target.Name, "withdrawn": true})
		return b.resultReply(p.Operation, p.PluginOperation, target.Name, outcome{status: statusSucceeded, transportOK: true, operationResult: or})
	}
	var spec PublishedTool
	if len(p.Arguments) == 0 {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "tools.publish requires arguments {name, summary, input, effects, capabilities}"})
	}
	if err := json.Unmarshal(p.Arguments, &spec); err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be a published tool: " + err.Error()})
	}
	registryName, err := b.publisher.PublishTool(spec)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, spec.Name, outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: err.Error()})
	}
	or, _ := json.Marshal(map[string]interface{}{"name": spec.Name, "tool": registryName, "published": true})
	return b.resultReply(p.Operation, p.PluginOperation, spec.Name, outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

// .
// .
func (b *Binding) SetSettings(fn func() map[string]interface{}) {
	if b == nil {
		return
	}
	b.settings = fn
}

// .
// .
// .
// .
// .
func (b *Binding) dispatchSettings(p invokeParams) ([]byte, error) {
	if denial, denied := b.scopeDenies(opSettingsGet, ""); denied {
		return denial, nil
	}
	values := map[string]interface{}{}
	if b.settings != nil {
		if got := b.settings(); got != nil {
			values = got
		}
	}
	or, err := json.Marshal(map[string]interface{}{"values": values})
	if err != nil {
		return nil, fmt.Errorf("broker: settings.get encode: %w", err)
	}
	return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
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
func opMutates(op string) bool {
	switch op {
	case opKVPut, opKVDelete, opMemoryRemember, opVoiceObserve,
		opFSWrite, opFSDelete, opFSPublish,
		opHTTPPost, opHTTPPut, opHTTPPatch, opHTTPDelete:
		return true
	}
	return false
}

func effectsAllow(effects, op string) bool {
	switch op {
	case opKVGet, opKVList, opSettingsGet, opMemoryRecall:
		return true
	case opKVPut, opKVDelete, opVoiceObserve, opMemoryRemember:
		return effects == "" || effects == EffectsWriteLocal || effects == EffectsWriteExternal || effects == EffectsExec
	case opHTTPGet, opEmbeddingsCreate:
		return effects == "" || effects == EffectsReadExternal || effects == EffectsWriteExternal || effects == EffectsExec
	case opHTTPPost, opHTTPPut, opHTTPPatch, opHTTPDelete:
		// .
		// .
		return effects == "" || effects == EffectsWriteExternal || effects == EffectsExec
	case opHTTPRead, opHTTPClose:
		// .
		// .
		return true
	case opFSList, opFSRead:
		return true
	case opFSWrite, opFSDelete, opFSPublish, opToolsPublish, opToolsWithdraw:
		return effects == "" || effects == EffectsWriteLocal || effects == EffectsWriteExternal || effects == EffectsExec
	}
	return true
}

// .
func httpMethodOf(op string) string {
	switch op {
	case opHTTPGet:
		return http.MethodGet
	case opHTTPPost:
		return http.MethodPost
	case opHTTPPut:
		return http.MethodPut
	case opHTTPPatch:
		return http.MethodPatch
	case opHTTPDelete:
		return http.MethodDelete
	}
	return ""
}

// .
// .
// .
func forbiddenRequestHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "host", "content-length", "transfer-encoding", "connection", "cookie", "set-cookie", "upgrade", "te", "trailer", "user-agent":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "proxy-")
}

// .
func validHeaderToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// .
// .
func (b *Binding) scopeDenies(op, capability string) ([]byte, bool) {
	sc := b.scope.Load()
	if sc == nil {
		return nil, false
	}
	if !effectsAllow(sc.Effects, op) {
		r, _ := errorReply(-32000,
			fmt.Sprintf("operation %s declares effects %s; %s is outside that class", sc.Operation, sc.Effects, op),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
		return r, true
	}
	if sc.Declared && capability != "" {
		for _, c := range sc.Capabilities {
			if c == capability {
				return nil, false
			}
		}
		r, _ := errorReply(-32000,
			fmt.Sprintf("operation %s did not declare %s among its capabilities", sc.Operation, capability),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
		return r, true
	}
	return nil, false
}

// .
// .
func (b *Binding) scopeDeniesNet(host string, port int) ([]byte, bool) {
	sc := b.scope.Load()
	if sc == nil || !sc.Declared {
		return nil, false
	}
	if anyScopeMatches(netEnvelopeScopes(sc.Capabilities), host, port) {
		return nil, false
	}
	r, _ := errorReply(-32000,
		fmt.Sprintf("operation %s did not declare %s:%s:%d among its capabilities", sc.Operation, capNetOutbound, host, port),
		&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	return r, true
}

// .
// .
// .
// .
func (b *Binding) dispatchKVList(p invokeParams) ([]byte, error) {
	var target struct {
		Prefix string `json:"prefix"`
	}
	if len(p.Target) > 0 {
		if err := json.Unmarshal(p.Target, &target); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonTargetInvalid, detail: "target must be an object"})
		}
	}
	if len(target.Prefix) > b.host.cfg.maxKVKeyBytes() || strings.ContainsRune(target.Prefix, 0) {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("kv.list target.prefix must be at most %d bytes without NUL", b.host.cfg.maxKVKeyBytes())})
	}
	limit := b.host.cfg.maxKVKeys()
	var args struct {
		Limit int `json:"limit"`
	}
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.Prefix, outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
	}
	if args.Limit > 0 && args.Limit < limit {
		limit = args.Limit
	}
	keys, truncated, err := b.host.cfg.Store.PluginKVList(b.pluginID, target.Prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("broker: kv.list: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	or, _ := json.Marshal(map[string]interface{}{"prefix": target.Prefix, "keys": keys, "truncated": truncated})
	return b.resultReply(p.Operation, p.PluginOperation, target.Prefix, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

// .
// .
// .
// .
// .
// .
// .
func (b *Binding) dispatchEmbeddings(ctx context.Context, p invokeParams, g Grant) ([]byte, error) {
	if !b.tier.PublisherProven() {
		return errorReply(-32000,
			fmt.Sprintf("tier %s holds no %s ceiling (the trust contract backs no external effects below a publisher-proven tier)", b.tier, capModelEmbeddings),
			&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
	}
	declared := false
	for _, c := range b.envelope {
		if c == capModelEmbeddings {
			declared = true
			break
		}
	}
	if !declared {
		return errorReply(-32000,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capModelEmbeddings, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.Embeddings {
		return errorReply(-32000,
			fmt.Sprintf("embeddings are not granted to plugin %s; the operator grants them in plugins.grants.%s.embeddings", b.pluginID, b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if r, denied := b.scopeDenies(p.Operation, capModelEmbeddings); denied {
		return r, nil
	}
	if b.host.cfg.Embed == nil {
		return errorReply(-32000, "this host serves no embeddings",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	var args struct {
		Input []string `json:"input"`
	}
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
	}
	if len(args.Input) == 0 {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: "embeddings.create requires arguments.input, a non-empty list of strings"})
	}
	if len(args.Input) > maxEmbedInputs {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid,
			detail: fmt.Sprintf("embeddings.create takes at most %d inputs per call, got %d", maxEmbedInputs, len(args.Input))})
	}
	for i, s := range args.Input {
		if s == "" || len(s) > maxEmbedInputBytes {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid,
				detail: fmt.Sprintf("embeddings.create input[%d] must be 1..%d bytes", i, maxEmbedInputBytes)})
		}
	}
	model, vectors, err := b.host.cfg.Embed.Embed(ctx, args.Input)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, model, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, detail: "the identity's model endpoint did not answer: " + err.Error()})
	}
	if len(vectors) != len(args.Input) {
		return b.resultReply(p.Operation, p.PluginOperation, model, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed,
			detail: fmt.Sprintf("the endpoint returned %d vectors for %d inputs", len(vectors), len(args.Input))})
	}
	dims := 0
	if len(vectors) > 0 {
		dims = len(vectors[0])
	}
	or, merr := json.Marshal(map[string]interface{}{"model": model, "dimensions": dims, "vectors": vectors})
	if merr != nil {
		return nil, fmt.Errorf("broker: embeddings.create: %w", merr)
	}
	return b.resultReply(p.Operation, p.PluginOperation, model, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

// .
// .
const opInvokeResult = "invoke.result"

// .
// .
// .
// .
func (b *Binding) InvokeRefused(operation, detail string) {
	if b == nil || b.host == nil {
		return
	}
	now := time.Now().UTC()
	rec := externalReceipt{
		TransportOutcome: true, AuditPersisted: true, HostAuthored: true,
		ID:              fmt.Sprintf("invoke-%d-%d", now.UnixNano(), b.host.counter.Add(1)),
		Timestamp:       now.Format(time.RFC3339Nano),
		PluginID:        b.pluginID,
		PluginVersion:   b.release.Version,
		PackageHash:     b.release.PackageHash,
		Tier:            b.tier.String(),
		Operation:       opInvokeResult,
		PluginOperation: operation,
		Detail:          detail,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_ = b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, opInvokeResult, "", false, raw)
}

type successResult struct {
	Success         bool            `json:"success"`
	OK              bool            `json:"ok"`
	Status          string          `json:"status"`
	OperationResult json.RawMessage `json:"operation_result"`
	ExternalReceipt json.RawMessage `json:"external_receipt"`
}

type failureResult struct {
	Success         bool            `json:"success"`
	OK              bool            `json:"ok"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason"`
	ReasonCode      string          `json:"reasonCode"`
	ReasonCodeSnake string          `json:"reason_code"`
	OperationResult json.RawMessage `json:"operation_result,omitempty"`
	ExternalReceipt json.RawMessage `json:"external_receipt"`
}

// .
// .
type outcome struct {
	status          string
	reason          string
	operationResult json.RawMessage
	transportOK     bool
	protocolStatus  *int
	detail          string
	method          string
	effect          string
}

// .

// .
// .
// .
func (b *Binding) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if b == nil {
		return errorReply(-32000, "this plugin holds no capability binding",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	// .
	// .
	// .
	// .
	// .
	// .
	pol := b.host.snapshot()
	if b.host.afterSnapshot != nil {
		b.host.afterSnapshot()
	}
	// .
	// .
	// .
	// .
	// .
	if pol.grant(b.pluginID).isEmpty() && !asksSettings(method, params) {
		return errorReply(-32000,
			fmt.Sprintf("no capability is granted to plugin %s; invoke-call denied", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if b.closed.Load() {
		// .
		// .
		// .
		// .
		return errorReply(-32000, "this plugin's activation has ended",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	reply, err := b.dispatch(ctx, method, params, pol)
	if err != nil {
		return nil, err
	}
	if len(reply) > bbb.MaxControlFrameBytes {
		// .
		// .
		// .
		// .
		return nil, fmt.Errorf("broker: produced a %d-byte reply over the %d-byte plugin frame ceiling", len(reply), bbb.MaxControlFrameBytes)
	}
	return reply, nil
}

func (b *Binding) dispatch(ctx context.Context, method string, params []byte, pol policySnapshot) ([]byte, error) {
	if method != methodInvokeCall {
		// .
		// .
		// .
		return errorReply(-32000, fmt.Sprintf("no %s surface in the step-4 broker; denied", method),
			&errorData{ReasonCode: reasonPolicyDeny})
	}

	var p invokeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorReply(-32602, "params must be a JSON object", nil)
	}
	if p.Operation == "" {
		// .
		return errorReply(-32602, "operation (string) required", nil)
	}
	if len(p.Grant) > 0 {
		// .
		return errorReply(-32602, "grant is retired; invoke.call evaluates capability per request", nil)
	}
	if len(p.WorkDoneToken) > 0 {
		// .
		// .
		return errorReply(-32602, "work_done_token requires negotiated rpc.cancel capability", nil)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if p.Operation != opKVGet && p.Operation != opKVList && p.Operation != opMemoryRecall && p.Operation != opSettingsGet && p.Operation != opVoiceObserve && !strings.HasPrefix(p.Operation, "fs.") && b.host.cfg.InSAFE != nil && b.host.cfg.InSAFE() {
		return errorReply(-32000,
			fmt.Sprintf("this identity is in SAFE; %s is refused while it holds", p.Operation),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
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
	if pol.grant(b.pluginID).ReadOnly && opMutates(p.Operation) {
		return errorReply(-32000,
			fmt.Sprintf("plugin %s is granted read only; %s writes", b.pluginID, p.Operation),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	switch p.Operation {
	case opKVPut, opKVGet, opKVDelete, opKVList:
		return b.dispatchKV(ctx, p, pol.grant(b.pluginID))
	case opMemoryRemember, opMemoryRecall:
		return b.dispatchMemory(ctx, p, pol.grant(b.pluginID))
	case opEmbeddingsCreate:
		return b.dispatchEmbeddings(ctx, p, pol.grant(b.pluginID))
	case opSettingsGet:
		return b.dispatchSettings(p)
	case opToolsPublish, opToolsWithdraw:
		return b.dispatchTools(p, pol.grant(b.pluginID))
	case opHTTPGet, opHTTPPost, opHTTPPut, opHTTPPatch, opHTTPDelete:
		return b.dispatchHTTP(ctx, p, pol)
	case opHTTPRead:
		return b.dispatchHTTPRead(p)
	case opHTTPClose:
		return b.dispatchHTTPClose(p)
	case opFSList, opFSRead, opFSWrite, opFSDelete, opFSPublish:
		return b.dispatchFS(p, pol)
	case opVoiceObserve:
		return b.dispatchVoiceObserve(ctx, p, pol.grant(b.pluginID))
	default:
		// .
		// .
		return errorReply(-32000,
			fmt.Sprintf("operation %q is not allowed for any capability this broker serves", p.Operation),
			&errorData{ReasonCode: reasonOpNotAllowed, DeniedAt: deniedAtCapEval})
	}
}

func errorReply(code int, message string, data *errorData) ([]byte, error) {
	raw, err := json.Marshal(rpcError{Code: code, Message: message, Data: data})
	if err != nil {
		return nil, fmt.Errorf("broker: encode error reply: %w", err)
	}
	return raw, nil
}

// .
// .
// .
// .
func (b *Binding) resultReply(operation, pluginOperation, target string, o outcome) ([]byte, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	const frameBudget = bbb.MaxControlFrameBytes - 8192
	if len(o.operationResult) > frameBudget {
		o = outcome{status: statusFailed, reason: reasonNetResponseTooBig,
			transportOK: o.transportOK, protocolStatus: o.protocolStatus,
			detail: fmt.Sprintf("encoded result of %d bytes exceeds the plugin frame budget", len(o.operationResult))}
	}
	now := time.Now().UTC()
	rec := externalReceipt{
		Success:          o.status == statusSucceeded,
		TransportOutcome: o.transportOK,
		ProtocolStatus:   o.protocolStatus,
		OperationOutcome: o.status == statusSucceeded,
		AuditPersisted:   true,
		HostAuthored:     true,
		// .
		// .
		// .
		ID:              fmt.Sprintf("invoke-%d-%d", now.UnixNano(), b.host.counter.Add(1)),
		Timestamp:       now.Format(time.RFC3339Nano),
		PluginID:        b.pluginID,
		PluginVersion:   b.release.Version,
		PackageHash:     b.release.PackageHash,
		Tier:            b.tier.String(),
		Operation:       operation,
		PluginOperation: pluginOperation,
		Target:          target,
		Detail:          o.detail,
		Method:          o.method,
		Effect:          o.effect,
	}
	recRaw, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("broker: encode receipt: %w", err)
	}
	if serr := b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, operation, target, rec.Success, recRaw); serr != nil {
		// .
		// .
		// .
		rec.AuditPersisted = false
		if recRaw, err = json.Marshal(rec); err != nil {
			return nil, fmt.Errorf("broker: encode receipt: %w", err)
		}
	}

	var reply interface{}
	if o.status == statusSucceeded {
		reply = successResult{Success: true, OK: true, Status: statusSucceeded,
			OperationResult: o.operationResult, ExternalReceipt: recRaw}
	} else {
		// .
		// .
		reply = failureResult{Success: false, OK: false, Status: o.status,
			Reason: o.reason, ReasonCode: o.reason, ReasonCodeSnake: o.reason,
			OperationResult: o.operationResult, ExternalReceipt: recRaw}
	}
	raw, err := json.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("broker: encode result: %w", err)
	}
	return raw, nil
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
type VoiceObserver interface {
	Observe(VoiceObservation) error
}

// .
// .
// .
type Embedder interface {
	Embed(ctx context.Context, inputs []string) (model string, vectors [][]float32, err error)
}

// .
// .
const (
	maxEmbedInputs     = 16
	maxEmbedInputBytes = 8 << 10
)

// .
// .
// .
// .
// .
// .
// .
type VoiceObservation struct {
	// .
	PluginID string
	// .
	Text string
	// .
	// .
	// .
	Speaker string
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
	SpeakerScore float64
}

// .
// .
// .
// .
// .
// .
func (b *Binding) voiceRings(capability string, g Grant) ([]byte, bool) {
	declared := false
	for _, c := range b.envelope {
		if c == capability {
			declared = true
			break
		}
	}
	if !declared {
		r, _ := errorReply(-32000,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capability, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
		return r, false
	}
	if !g.Voice {
		r, _ := errorReply(-32000,
			fmt.Sprintf("voice is not granted to plugin %s; the operator grants it in plugins.grants", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
		return r, false
	}
	if r, denied := b.scopeDenies(opVoiceObserve, capability); denied {
		return r, false
	}
	return nil, true
}

func (b *Binding) dispatchVoiceObserve(_ context.Context, p invokeParams, g Grant) ([]byte, error) {
	if r, ok := b.voiceRings(capVoiceObserve, g); !ok {
		return r, nil
	}
	if b.host.cfg.Voice == nil {
		return errorReply(-32000,
			"this host accepts no voice observations",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if b.host.cfg.InSAFE != nil && b.host.cfg.InSAFE() {
		return errorReply(-32000,
			"this identity is in SAFE; nothing heard is recorded while it holds",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	var args struct {
		Text    string `json:"text"`
		Speaker string `json:"speaker"`
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		SpeakerScore float64 `json:"speaker_score"`
	}
	if len(p.Arguments) > 0 {
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
		dec := json.NewDecoder(bytes.NewReader(p.Arguments))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonTargetInvalid,
				detail: "voice.observe takes exactly {text, speaker, speaker_score}: " + err.Error() +
					" — a voice plugin proposes what it heard and never decides what it means, so there is no field here for a role, a confidence, or an is_operator"})
		}
	}
	if strings.TrimSpace(args.Text) == "" {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: "voice.observe requires arguments.text — an utterance with no words is not an observation"})
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if len(args.Text) > maxUtteranceBytes {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("voice.observe text is %d bytes; the limit is %d — an utterance is speech, not a payload",
				len(args.Text), maxUtteranceBytes)})
	}
	if len(args.Speaker) > maxSpeakerLabelBytes {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("voice.observe speaker label is %d bytes; the limit is %d — a label names a voice, it does not carry one",
				len(args.Speaker), maxSpeakerLabelBytes)})
	}
	// .
	// .
	// .
	// .
	if args.SpeakerScore < 0 || args.SpeakerScore > 1 {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("voice.observe speaker_score %v is outside 0..1 — a score is a confidence, and one that is not is not evidence",
				args.SpeakerScore)})
	}
	if err := b.host.cfg.Voice.Observe(VoiceObservation{
		PluginID:     b.pluginID,
		Text:         args.Text,
		Speaker:      args.Speaker,
		SpeakerScore: args.SpeakerScore,
	}); err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, args.Speaker, outcome{
			status: statusFailed, reason: reasonPolicyDeny, detail: err.Error()})
	}
	return b.resultReply(p.Operation, p.PluginOperation, args.Speaker, outcome{
		status: statusSucceeded, operationResult: []byte(`{"recorded":true}`)})
}

func (b *Binding) dispatchKV(_ context.Context, p invokeParams, g Grant) ([]byte, error) {
	// .
	// .
	// .
	// .
	declared := false
	for _, c := range b.envelope {
		if c == capRing4KV {
			declared = true
			break
		}
	}
	if !declared {
		return errorReply(-32000,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capRing4KV, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.KV {
		return errorReply(-32000,
			fmt.Sprintf("kv is not granted to plugin %s; the operator grants storage in plugins.grants", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if r, denied := b.scopeDenies(p.Operation, capRing4KV); denied {
		return r, nil
	}
	if p.Operation == opKVList {
		return b.dispatchKVList(p)
	}

	var target struct {
		Key string `json:"key"`
	}
	if len(p.Target) > 0 {
		if err := json.Unmarshal(p.Target, &target); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonTargetInvalid, detail: "target must be an object"})
		}
	}
	key := target.Key
	if key == "" || len(key) > b.host.cfg.maxKVKeyBytes() || strings.ContainsRune(key, 0) {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("kv target.key must be 1..%d bytes without NUL", b.host.cfg.maxKVKeyBytes())})
	}

	// .
	// .
	// .
	// .
	temp := !b.tier.PublisherProven()
	st := b.host.cfg.Store

	switch p.Operation {
	case opKVPut:
		var args struct {
			Value *string `json:"value"`
		}
		if len(p.Arguments) > 0 {
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
					status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
			}
		}
		if args.Value == nil {
			return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "kv.put requires arguments.value (string)"})
		}
		if len(*args.Value) > b.host.cfg.maxKVValueBytes() {
			return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
				status: statusFailed, reason: reasonKVValueTooLarge,
				detail: fmt.Sprintf("value of %d bytes exceeds the %d-byte ceiling", len(*args.Value), b.host.cfg.maxKVValueBytes())})
		}
		err := st.PluginKVPut(b.pluginID, key, *args.Value, temp, b.host.cfg.maxKVKeys(), b.host.cfg.maxKVTotalBytes())
		if errors.Is(err, store.ErrPluginKVQuota) {
			return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
				status: statusFailed, reason: reasonKVQuotaExceeded, detail: err.Error()})
		}
		if err != nil {
			return nil, fmt.Errorf("broker: kv.put: %w", err)
		}
		scope := "persistent"
		if temp {
			scope = "temp"
		}
		or, _ := json.Marshal(map[string]interface{}{"stored": true, "key": key, "value_bytes": len(*args.Value), "scope": scope})
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})

	case opKVGet:
		value, found, err := st.PluginKVGet(b.pluginID, key)
		if err != nil {
			return nil, fmt.Errorf("broker: kv.get: %w", err)
		}
		if !found {
			return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
				status: statusFailed, reason: reasonKVNotFound, transportOK: true})
		}
		or, merr := json.Marshal(map[string]interface{}{"key": key, "value": value})
		if merr != nil {
			return nil, fmt.Errorf("broker: kv.get encode: %w", merr)
		}
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})

	default:
		deleted, err := st.PluginKVDelete(b.pluginID, key)
		if err != nil {
			return nil, fmt.Errorf("broker: kv.delete: %w", err)
		}
		or, _ := json.Marshal(map[string]interface{}{"deleted": deleted, "key": key})
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})
	}
}

// .

// .
// .
// .
// .
type hostScope struct {
	host    string
	port    int
	anyPort bool
}

func parseHostScope(s string) (hostScope, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return hostScope{}, false
	}
	host, portText := s, ""
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s, "]") {
		host, portText = s[:i], s[i+1:]
	}
	if host == "" || host == "*" {
		return hostScope{}, false
	}
	sc := hostScope{host: strings.ToLower(host)}
	switch portText {
	case "", "*":
		sc.anyPort = true
	default:
		var p int
		if _, err := fmt.Sscanf(portText, "%d", &p); err != nil || p < 1 || p > 65535 {
			return hostScope{}, false
		}
		sc.port = p
	}
	return sc, true
}

func (s hostScope) matches(host string, port int) bool {
	return s.host == strings.ToLower(host) && (s.anyPort || s.port == port)
}

// .
// .
// .
func netEnvelopeScopes(envelope []string) []hostScope {
	var out []hostScope
	for _, entry := range envelope {
		rest, ok := strings.CutPrefix(entry, capNetOutbound+":")
		if !ok {
			continue
		}
		if sc, ok := parseHostScope(rest); ok {
			out = append(out, sc)
		}
	}
	return out
}

func anyScopeMatches(scopes []hostScope, host string, port int) bool {
	for _, sc := range scopes {
		if sc.matches(host, port) {
			return true
		}
	}
	return false
}

func urlHostPort(u *url.URL) (string, int) {
	host := u.Hostname()
	port := 0
	switch {
	case u.Port() != "":
		fmt.Sscanf(u.Port(), "%d", &port)
	case u.Scheme == "https":
		port = 443
	case u.Scheme == "http":
		port = 80
	}
	return host, port
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
type httpCall struct {
	method          string
	timeout         time.Duration
	authProfile     string
	body            []byte
	contentType     string
	headers         http.Header
	followRedirects bool
	idempotent      bool
	stream          bool
}

func (b *Binding) dispatchHTTP(ctx context.Context, p invokeParams, pol policySnapshot) ([]byte, error) {
	method := httpMethodOf(p.Operation)
	var target struct {
		URL string `json:"url"`
	}
	if len(p.Target) > 0 {
		if err := json.Unmarshal(p.Target, &target); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonTargetInvalid, detail: "target must be an object"})
		}
	}
	if target.URL == "" {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid, detail: p.Operation + " requires target.url (rpc_capability.c:331-338)", method: method})
	}
	u, err := url.Parse(target.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL"})
	}
	// .
	// .
	// .
	if portText := u.Port(); portText != "" {
		n, serr := strconv.Atoi(portText)
		if serr != nil || n < 1 || n > 65535 {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL port"})
		}
	}
	host, port := urlHostPort(u)
	grant := pol.grant(b.pluginID)
	var grantScopes []hostScope
	for _, g := range grant.Hosts {
		if sc, ok := parseHostScope(g); ok {
			grantScopes = append(grantScopes, sc)
		}
	}
	localScopes, _ := tools.LocalScopes(grant.Local)
	// .
	// .
	// .
	localCapable := envelopeHas(b.envelope, capNetLocal)
	local := localCapable && localTarget(host, port, localScopes)
	if !localCapable {
		localScopes = nil
	}
	if local {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if r, denied := b.scopeDenies(p.Operation, ""); denied {
			return r, nil
		}
		if r, denied := b.scopeDeniesLocal(); denied {
			return r, nil
		}
	} else {
		// .
		// .
		// .
		// .
		if !b.tier.PublisherProven() {
			return errorReply(-32000,
				fmt.Sprintf("tier %s holds no %s ceiling (the trust contract backs no external effects below a publisher-proven tier)", b.tier, capNetOutbound),
				&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
		}
		// .
		// .
		if !anyScopeMatches(netEnvelopeScopes(b.envelope), host, port) {
			return errorReply(-32000,
				fmt.Sprintf("%s:%s:%d is outside the signed capability envelope", capNetOutbound, host, port),
				&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
		}
		// .
		// .
		if !anyScopeMatches(grantScopes, host, port) {
			return errorReply(-32000,
				fmt.Sprintf("no operator grant covers %s:%s:%d (plugins.grants.%s.hosts)", capNetOutbound, host, port, b.pluginID),
				&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
		}
		// .
		// .
		if r, denied := b.scopeDenies(p.Operation, ""); denied {
			return r, nil
		}
		if r, denied := b.scopeDeniesNet(host, port); denied {
			return r, nil
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	call := httpCall{method: method, timeout: DefaultHTTPTimeout, followRedirects: method == http.MethodGet, idempotent: method == http.MethodGet}
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object", method: method})
		}
		deny := func(reason, detail string) ([]byte, error) {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reason, detail: detail, method: method})
		}
		for name, raw := range args {
			switch name {
			case "timeout_ms":
				var ms int
				if err := json.Unmarshal(raw, &ms); err != nil || ms <= 0 {
					return deny(reasonArgumentInvalid, "timeout_ms must be a positive integer")
				}
				if time.Duration(ms)*time.Millisecond > MaxHTTPTimeout {
					return deny(reasonNetTimeoutLimit, fmt.Sprintf("timeout_ms %d exceeds the %d ms ceiling", ms, MaxHTTPTimeout/time.Millisecond))
				}
				call.timeout = time.Duration(ms) * time.Millisecond
			case "auth_profile":
				if err := json.Unmarshal(raw, &call.authProfile); err != nil || call.authProfile == "" {
					return deny(reasonAuthInvalid, "auth_profile must be a non-empty string")
				}
			case "body":
				if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
					return deny(reasonNetUnknownArgument, fmt.Sprintf("body is not an argument of %s", p.Operation))
				}
				var body string
				if err := json.Unmarshal(raw, &body); err != nil {
					return deny(reasonArgumentInvalid, "body must be a string (encode JSON bodies as text)")
				}
				if len(body) > b.host.cfg.maxRequestBytes() {
					return deny(reasonNetRequestTooBig, fmt.Sprintf("body of %d bytes exceeds the %d-byte ceiling", len(body), b.host.cfg.maxRequestBytes()))
				}
				call.body = []byte(body)
			case "content_type":
				if err := json.Unmarshal(raw, &call.contentType); err != nil || call.contentType == "" || len(call.contentType) > 256 || strings.ContainsAny(call.contentType, "\x00\r\n") {
					return deny(reasonArgumentInvalid, "content_type must be a short media type")
				}
			case "headers":
				var hdrs map[string]string
				if err := json.Unmarshal(raw, &hdrs); err != nil {
					return deny(reasonArgumentInvalid, "headers must be an object of strings")
				}
				if len(hdrs) > MaxRequestHeaders {
					return deny(reasonNetHeaderInvalid, fmt.Sprintf("%d headers exceed the %d ceiling", len(hdrs), MaxRequestHeaders))
				}
				call.headers = http.Header{}
				for hn, hv := range hdrs {
					if !validHeaderToken(hn) || len(hv) > MaxRequestHeaderBytes || strings.ContainsAny(hv, "\x00\r\n") {
						return deny(reasonNetHeaderInvalid, fmt.Sprintf("header %q is not a valid header", hn))
					}
					if forbiddenRequestHeader(hn) {
						return deny(reasonNetHeaderInvalid, fmt.Sprintf("header %q is the broker's or the transport's, never a call's (a credential rides an auth_profile)", hn))
					}
					if strings.EqualFold(hn, "content-type") {
						return deny(reasonNetHeaderInvalid, "content-type is the content_type argument")
					}
					call.headers.Set(hn, hv)
				}
			case "follow_redirects":
				if err := json.Unmarshal(raw, &call.followRedirects); err != nil {
					return deny(reasonArgumentInvalid, "follow_redirects must be true or false")
				}
			case "idempotent":
				if err := json.Unmarshal(raw, &call.idempotent); err != nil {
					return deny(reasonArgumentInvalid, "idempotent must be true or false")
				}
			case "stream":
				if err := json.Unmarshal(raw, &call.stream); err != nil {
					return deny(reasonArgumentInvalid, "stream must be true or false")
				}
			default:
				return deny(reasonNetUnknownArgument, fmt.Sprintf("argument %q is not supported by this broker (timeout_ms, auth_profile, body, content_type, headers, follow_redirects, idempotent, stream)", name))
			}
		}
	}
	if call.body != nil && call.contentType == "" {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: "body requires content_type", method: method})
	}
	if call.body == nil && call.contentType != "" {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: "content_type belongs to a body", method: method})
	}
	timeout, authProfile := call.timeout, call.authProfile

	// .
	// .
	// .
	// .
	var auth resolvedAuth
	if authProfile != "" {
		if o := b.resolveAuthProfile(pol, authProfile, u, target.URL, host, port, &auth, local, grant.PlaintextCredentials); o != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, *o)
		}
	} else if strings.Contains(target.URL, CredentialPlaceholder) {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonArgumentInvalid, method: method,
			detail: fmt.Sprintf("the URL names %s but no auth_profile rides this call", CredentialPlaceholder)})
	}
	// .
	// .
	// .
	// .
	// .
	authHeader, pathSecret := auth.header, auth.pathSecret
	dialURL := target.URL
	scrub := func(s string) string { return s }
	if pathSecret != "" {
		if call.stream {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonAuthInvalid, method: method,
				detail: "a credential in the URL path rides non-streaming calls only"})
		}
		escaped := url.PathEscape(pathSecret)
		dialURL = strings.Replace(target.URL, CredentialPlaceholder, escaped, 1)
		scrub = func(s string) string {
			s = strings.ReplaceAll(s, escaped, CredentialPlaceholder)
			return strings.ReplaceAll(s, pathSecret, CredentialPlaceholder)
		}
	}

	// .
	// .
	// .
	// .
	// .
	baseGuard := b.host.cfg.Guard
	if baseGuard == nil {
		baseGuard = tools.FetchGuard
	}
	// .
	// .
	// .
	lg := tools.GuardAdmitting(baseGuard, localScopes, b.host.cfg.OwnListener)
	guard := lg.Guard
	// .
	// .
	// .
	// .
	// .
	// .
	envelopeScopes := netEnvelopeScopes(b.envelope)
	hopGuard := func(ctx context.Context, rawURL string) error {
		if err := guard(ctx, rawURL); err != nil {
			return err
		}
		hu, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("%w: unparseable redirect target", tools.ErrEgressBlocked)
		}
		hh, hp := urlHostPort(hu)
		if localCapable && localTarget(hh, hp, localScopes) {
			return nil
		}
		if !anyScopeMatches(envelopeScopes, hh, hp) || !anyScopeMatches(grantScopes, hh, hp) {
			return fmt.Errorf("%w: %s:%d is outside the plugin's granted hosts", tools.ErrEgressBlocked, hh, hp)
		}
		return nil
	}
	if gerr := hopGuard(ctx, dialURL); gerr != nil {
		detail := scrub(fmt.Sprintf("egress guard: %v", gerr))
		switch {
		case local:
			detail += fmt.Sprintf(" (plugins.grants.%s.local names what this plugin may reach on your network)", b.pluginID)
		case !localCapable && localTarget(host, port, nil):
			detail += fmt.Sprintf(" (this plugin's signed envelope does not declare %s, so no local grant can apply to it)", capNetLocal)
		}
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonPolicyDeny, detail: detail})
	}

	var bodyReader io.Reader
	if call.body != nil {
		bodyReader = bytes.NewReader(call.body)
	}
	req, err := http.NewRequestWithContext(tools.WithPins(ctx, lg), method, dialURL, bodyReader)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL", method: method})
	}
	for hn, hv := range call.headers {
		req.Header[hn] = hv
	}
	req.Header.Set("User-Agent", "AII-OS/1.0")
	if call.contentType != "" {
		req.Header.Set("Content-Type", call.contentType)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	client := tools.GuardedClient(timeout, hopGuard, b.host.cfg.Transport)
	if !call.followRedirects {
		// .
		// .
		// .
		// .
		// .
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	// .
	// .
	// .
	// .
	// .
	// .
	attempts := 1
	if call.idempotent {
		attempts = 2
	}
	var resp *http.Response
	var wrote bool
	send := func() {
		for attempt := 1; attempt <= attempts; attempt++ {
			wrote = false
			trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wrote = true }}
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
			attemptReq := req.Clone(httptrace.WithClientTrace(req.Context(), trace))
			if call.body != nil {
				attemptReq.Body = io.NopCloser(bytes.NewReader(call.body))
			}
			resp, err = client.Do(attemptReq)
			if err == nil || errors.Is(err, tools.ErrEgressBlocked) || attempt == attempts {
				break
			}
		}
	}
	send()
	// .
	// .
	// .
	// .
	// .
	// .
	if err == nil && resp.StatusCode == http.StatusUnauthorized && auth.source != nil {
		resp.Body.Close()
		cred, rerr := auth.source.ForceRefresh(ctx, auth.gen)
		if rerr != nil || cred.Refreshed {
			b.receiptAuthRefresh(auth.profile, rerr)
		}
		if rerr != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, *oauthDenial(auth.profile, rerr))
		}
		req.Header.Set("Authorization", bearerPrefix+cred.Token)
		send()
	}
	if err != nil {
		// .
		// .
		// .
		reason, status, effect := reasonNetRemoteFailed, statusFailed, EffectNotPerformed
		switch {
		case errors.Is(err, tools.ErrEgressBlocked):
			reason, status = reasonPolicyDeny, statusDenied
			if wrote {
				effect = EffectPerformed
			}
		case wrote:
			reason, effect = reasonNetEffectUnknown, EffectUnknown
		}
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: status, reason: reason, detail: scrub(fmt.Sprintf("transport: %v", err)), method: method, effect: effect})
	}
	if call.stream && resp.StatusCode < 400 {
		// .
		return b.openStream(p, target.URL, method, resp)
	}
	defer resp.Body.Close()

	maxBytes := b.host.cfg.maxResponseBytes()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	httpStatus := resp.StatusCode
	if rerr != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			detail: fmt.Sprintf("read: %v", rerr), method: method, effect: EffectPerformed})
	}
	if len(body) > maxBytes {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusFailed, reason: reasonNetResponseTooBig, transportOK: true, protocolStatus: &httpStatus,
			detail: fmt.Sprintf("response exceeds the %d-byte ceiling", maxBytes), method: method, effect: EffectPerformed})
	}

	// .
	// .
	// .
	// .
	if method == http.MethodGet && b.host.cfg.ObserveFetch != nil {
		b.host.cfg.ObserveFetch(target.URL)
	}

	// .
	// .
	// .
	// .
	// .
	or := map[string]interface{}{"http_status": httpStatus}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		or["content_type"] = ct
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		or["location"] = scrub(loc)
	}
	if pathSecret != "" {
		body = []byte(scrub(string(body)))
	}
	switch {
	case len(body) == 0:
		or["body"] = nil
	case json.Valid(body):
		or["body"] = json.RawMessage(body)
	default:
		or["body"] = string(body)
	}
	orRaw, merr := json.Marshal(or)
	if merr != nil {
		return nil, fmt.Errorf("broker: encode operation_result: %w", merr)
	}

	// .
	// .
	// .
	if httpStatus >= 400 {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			operationResult: orRaw, method: method, effect: EffectPerformed})
	}
	return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
		status: statusSucceeded, transportOK: true, protocolStatus: &httpStatus, operationResult: orRaw, method: method, effect: EffectPerformed})
}

// .

// .
// .
type httpStream struct {
	id              string
	operation       string
	pluginOperation string
	target          string
	method          string
	resp            *http.Response
	seq             int
	total           int
	cap             int
}

// .
// .
func (b *Binding) openStream(p invokeParams, target, method string, resp *http.Response) ([]byte, error) {
	b.streamsMu.Lock()
	if len(b.streams) >= MaxOpenStreams {
		b.streamsMu.Unlock()
		resp.Body.Close()
		httpStatus := resp.StatusCode
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			detail: fmt.Sprintf("%d streams are open; the ceiling is %d — close one", len(b.streams), MaxOpenStreams), method: method, effect: EffectPerformed})
	}
	b.streamSeq++
	s := &httpStream{id: fmt.Sprintf("s%d", b.streamSeq), operation: p.Operation, pluginOperation: p.PluginOperation, target: target, method: method, resp: resp, cap: b.host.cfg.maxStreamBytes()}
	if b.streamCap > 0 {
		s.cap = b.streamCap
	}
	if b.streams == nil {
		b.streams = map[string]*httpStream{}
	}
	b.streams[s.id] = s
	b.streamsMu.Unlock()
	if method == http.MethodGet && b.host.cfg.ObserveFetch != nil {
		b.host.cfg.ObserveFetch(target)
	}
	or := map[string]interface{}{"stream_id": s.id, "http_status": resp.StatusCode, "chunk_max_bytes": MaxStreamChunkBytes, "stream_max_bytes": s.cap}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		or["content_type"] = ct
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		or["location"] = loc
	}
	orRaw, err := json.Marshal(or)
	if err != nil {
		return nil, fmt.Errorf("broker: encode stream head: %w", err)
	}
	// .
	// .
	return json.Marshal(successResult{Success: true, OK: true, Status: statusSucceeded, OperationResult: orRaw})
}

// .
// .
func (b *Binding) takeStream(id string, remove bool) *httpStream {
	b.streamsMu.Lock()
	defer b.streamsMu.Unlock()
	s := b.streams[id]
	if s != nil && remove {
		delete(b.streams, id)
	}
	return s
}

// .
// .
func (b *Binding) endStreams(why string) {
	b.streamsMu.Lock()
	open := b.streams
	b.streams = nil
	b.streamsMu.Unlock()
	for _, s := range open {
		s.resp.Body.Close()
		httpStatus := s.resp.StatusCode
		_, _ = b.resultReply(s.operation, s.pluginOperation, s.target, outcome{
			status: statusSucceeded, transportOK: true, protocolStatus: &httpStatus, method: s.method, effect: EffectPerformed,
			detail: fmt.Sprintf("stream %s %s after %d bytes in %d chunks", s.id, why, s.total, s.seq)})
	}
}

// .
// .
// .
// .
func (b *Binding) dispatchHTTPRead(p invokeParams) ([]byte, error) {
	var target struct {
		StreamID string `json:"stream_id"`
	}
	if len(p.Target) > 0 {
		if err := json.Unmarshal(p.Target, &target); err != nil {
			return errorReply(-32602, "target must be an object", nil)
		}
	}
	if target.StreamID == "" {
		return errorReply(-32602, "http.read requires target.stream_id", nil)
	}
	maxBytes := MaxStreamChunkBytes
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return errorReply(-32602, "arguments must be an object", nil)
		}
		for name, raw := range args {
			switch name {
			case "max_bytes":
				var n int
				if err := json.Unmarshal(raw, &n); err != nil || n <= 0 {
					return errorReply(-32602, "max_bytes must be a positive integer", nil)
				}
				if n > MaxStreamChunkBytes {
					return errorReply(-32602, fmt.Sprintf("max_bytes %d exceeds the %d-byte chunk ceiling", n, MaxStreamChunkBytes), nil)
				}
				maxBytes = n
			default:
				return errorReply(-32602, fmt.Sprintf("argument %q is not supported by http.read (max_bytes)", name), nil)
			}
		}
	}
	s := b.takeStream(target.StreamID, false)
	if s == nil {
		return errorReply(-32000, fmt.Sprintf("no open stream %s in this activation", target.StreamID),
			&errorData{ReasonCode: reasonTargetInvalid, DeniedAt: deniedAtCapEval})
	}
	buf := make([]byte, maxBytes)
	n, rerr := s.resp.Body.Read(buf)
	httpStatus := s.resp.StatusCode
	s.total += n
	if n > 0 {
		s.seq++
	}
	terminal := func(status, reason, detail string) ([]byte, error) {
		b.takeStream(s.id, true)
		s.resp.Body.Close()
		or, _ := json.Marshal(map[string]interface{}{"stream_id": s.id, "seq": s.seq, "data_b64": base64.StdEncoding.EncodeToString(buf[:n]), "bytes": n, "done": true, "total_bytes": s.total})
		return b.resultReply(s.operation, s.pluginOperation, s.target, outcome{
			status: status, reason: reason, transportOK: true, protocolStatus: &httpStatus, method: s.method, effect: EffectPerformed,
			operationResult: or, detail: detail})
	}
	if s.total > s.cap {
		return terminal(statusFailed, reasonNetResponseTooBig, fmt.Sprintf("stream %s exceeds the %d-byte ceiling after %d chunks", s.id, s.cap, s.seq))
	}
	switch {
	case rerr == io.EOF:
		return terminal(statusSucceeded, "", fmt.Sprintf("stream %s complete: %d bytes in %d chunks", s.id, s.total, s.seq))
	case rerr != nil:
		return terminal(statusFailed, reasonNetRemoteFailed, fmt.Sprintf("stream %s ended after %d bytes: %v", s.id, s.total, rerr))
	}
	or, err := json.Marshal(map[string]interface{}{"stream_id": s.id, "seq": s.seq, "data_b64": base64.StdEncoding.EncodeToString(buf[:n]), "bytes": n, "done": false, "total_bytes": s.total})
	if err != nil {
		return nil, fmt.Errorf("broker: encode chunk: %w", err)
	}
	return json.Marshal(successResult{Success: true, OK: true, Status: statusSucceeded, OperationResult: or})
}

// .
// .
func (b *Binding) dispatchHTTPClose(p invokeParams) ([]byte, error) {
	var target struct {
		StreamID string `json:"stream_id"`
	}
	if len(p.Target) > 0 {
		if err := json.Unmarshal(p.Target, &target); err != nil {
			return errorReply(-32602, "target must be an object", nil)
		}
	}
	s := b.takeStream(target.StreamID, true)
	if s == nil {
		return errorReply(-32000, fmt.Sprintf("no open stream %s in this activation", target.StreamID),
			&errorData{ReasonCode: reasonTargetInvalid, DeniedAt: deniedAtCapEval})
	}
	s.resp.Body.Close()
	httpStatus := s.resp.StatusCode
	or, _ := json.Marshal(map[string]interface{}{"stream_id": s.id, "closed": true, "total_bytes": s.total, "chunks": s.seq})
	return b.resultReply(s.operation, s.pluginOperation, s.target, outcome{
		status: statusSucceeded, transportOK: true, protocolStatus: &httpStatus, method: s.method, effect: EffectPerformed,
		operationResult: or, detail: fmt.Sprintf("stream %s closed by the plugin after %d bytes in %d chunks", s.id, s.total, s.seq)})
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
type resolvedAuth struct {
	header     string
	pathSecret string
	source     *oauth.Source
	gen        uint64
	profile    string
}

func (b *Binding) resolveAuthProfile(pol policySnapshot, name string, u *url.URL, rawURL, host string, port int, auth *resolvedAuth, local, plaintextOK bool) *outcome {
	authHeader, pathSecret := &auth.header, &auth.pathSecret
	auth.profile = name
	// .
	// .
	// .
	// .
	// .
	// .
	if !local && !b.tier.ReviewProven() {
		return &outcome{status: statusDenied, reason: reasonAuthNotAdmitted,
			detail: fmt.Sprintf("auth_profile requires a review-proven tier; %s is not", b.tier)}
	}
	admitted := false
	for _, h := range pol.grant(b.pluginID).CredentialHandles {
		if h == name {
			admitted = true
			break
		}
	}
	if !admitted {
		return &outcome{status: statusDenied, reason: reasonAuthNotAdmitted,
			detail: "auth_profile was not admitted for this plugin (plugins.grants credential_handles)"}
	}
	// .
	// .
	// .
	// .
	if u.Scheme != "https" && !(local && plaintextOK) {
		detail := "auth_profile requires an https target"
		if local {
			detail = fmt.Sprintf("auth_profile over plain http on the local network needs plugins.grants.%s.plaintext_credentials: true — a device on your network could read it", b.pluginID)
		}
		return &outcome{status: statusDenied, reason: reasonAuthRequiresHTTPS, detail: detail}
	}
	profile, ok := pol.profiles[name]
	if !ok {
		return &outcome{status: statusDenied, reason: reasonAuthUnavailable,
			detail: "no such auth profile in plugins.auth_profiles"}
	}
	if profile.Scheme == SchemeOAuth2 {
		return b.resolveOAuth2(name, profile, rawURL, host, port, auth)
	}
	if profile.Host == "" || profile.Port == 0 {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: "auth profile must pin host and port"}
	}
	// .
	// .
	if !strings.EqualFold(profile.Host, host) || profile.Port != port {
		return &outcome{status: statusDenied, reason: reasonAuthScopeMismatch,
			detail: "auth_profile host:port does not match target URL"}
	}

	var secret string
	switch {
	case profile.SecretEnv != "":
		secret = os.Getenv(profile.SecretEnv)
	case profile.SecretFile != "":
		raw, err := os.ReadFile(profile.SecretFile)
		if err != nil {
			return &outcome{status: statusDenied, reason: reasonAuthSecretMissing,
				detail: "auth_profile secret is unavailable"}
		}
		secret = strings.TrimSpace(string(raw))
	}
	if secret == "" {
		return &outcome{status: statusDenied, reason: reasonAuthSecretMissing,
			detail: "auth_profile secret is unavailable"}
	}
	// .
	// .
	// .
	// .
	// .
	if len(secret)+len(bearerPrefix) > 4096 || strings.ContainsAny(secret, "\x00\r\n") {
		return &outcome{status: statusDenied, reason: reasonNetHeaderInvalid,
			detail: "auth_profile secret is not a valid credential"}
	}
	placeholder := strings.Contains(rawURL, CredentialPlaceholder)
	switch profile.Scheme {
	case "", "bearer":
		if placeholder {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: fmt.Sprintf("the URL names %s but auth profile %q rides as a bearer header; a credential in the path is scheme \"path\"", CredentialPlaceholder, name)}
		}
		*authHeader = bearerPrefix + secret
	case "basic":
		if placeholder {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: fmt.Sprintf("the URL names %s but auth profile %q rides as a basic header; a credential in the path is scheme \"path\"", CredentialPlaceholder, name)}
		}
		if !strings.Contains(secret, ":") {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: "a basic auth profile's secret is user:password"}
		}
		*authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(secret))
	case "path":
		// .
		// .
		// .
		// .
		if !placeholder {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: fmt.Sprintf("auth profile %q rides in the path, and the URL names no %s", name, CredentialPlaceholder)}
		}
		*pathSecret = secret
	default:
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: fmt.Sprintf("auth profile scheme %q is not bearer, basic, path or oauth2", profile.Scheme)}
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
func (b *Binding) resolveOAuth2(name string, profile AuthProfile, rawURL, host string, port int, auth *resolvedAuth) *outcome {
	if strings.Contains(rawURL, CredentialPlaceholder) {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: fmt.Sprintf("the URL names %s but auth profile %q rides as a bearer header (oauth2)", CredentialPlaceholder, name)}
	}
	params, tpl, err := profile.Contract()
	if err != nil {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid, detail: err.Error()}
	}
	hp := fmt.Sprintf("%s:%d", strings.ToLower(host), port)
	pinned := false
	for _, h := range tpl.Hosts {
		if strings.EqualFold(strings.TrimSpace(h), hp) {
			pinned = true
			break
		}
	}
	if !pinned {
		return &outcome{status: statusDenied, reason: reasonAuthScopeMismatch,
			detail: fmt.Sprintf("auth profile %q rides only to its authority's hosts (%s); %s is not one of them", name, strings.Join(tpl.Hosts, ", "), hp)}
	}
	switch {
	case profile.ClientSecretEnv != "":
		params.ClientSecret = os.Getenv(profile.ClientSecretEnv)
	case profile.ClientSecretFile != "":
		raw, rerr := os.ReadFile(profile.ClientSecretFile)
		if rerr != nil {
			return &outcome{status: statusDenied, reason: reasonAuthSecretMissing, detail: "the client secret is unavailable"}
		}
		params.ClientSecret = strings.TrimSpace(string(raw))
	}
	src, o := b.host.profileSource(name, profile, params)
	if o != nil {
		return o
	}
	cred, cerr := src.Credential(context.Background())
	if cerr != nil || cred.Refreshed {
		b.receiptAuthRefresh(name, cerr)
	}
	if cerr != nil {
		return oauthDenial(name, cerr)
	}
	if len(cred.Token)+len(bearerPrefix) > 4096 || strings.ContainsAny(cred.Token, "\x00\r\n") {
		return &outcome{status: statusDenied, reason: reasonNetHeaderInvalid, detail: "the access token is not a valid credential"}
	}
	auth.header = bearerPrefix + cred.Token
	auth.source, auth.gen = src, cred.Gen
	return nil
}

// .
func oauthDenial(name string, err error) *outcome {
	switch {
	case errors.Is(err, oauth.ErrGrantInvalid), errors.Is(err, os.ErrNotExist), errors.Is(err, oauth.ErrOwnerRefreshRequired):
		return &outcome{status: statusDenied, reason: reasonAuthDisconnected,
			detail: fmt.Sprintf("auth profile %q is not connected (or the authority no longer accepts it): the operator connects it on the Plugins page", name)}
	default:
		return &outcome{status: statusDenied, reason: reasonAuthUnavailable,
			detail: fmt.Sprintf("auth profile %q could not be refreshed just now; try again later", name)}
	}
}

// .
// .
// .
// .
func (h *Host) profileSource(name string, profile AuthProfile, params oauth.OAuthParams) (*oauth.Source, *outcome) {
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	if src, ok := h.oauthSrc[name]; ok {
		return src, nil
	}
	tu, err := url.Parse(params.TokenURL)
	if err != nil || tu.Scheme != "https" || tu.Host == "" {
		return nil, &outcome{status: statusDenied, reason: reasonAuthInvalid, detail: "the token endpoint must be an https URL"}
	}
	th, tp := urlHostPort(tu)
	base := h.cfg.Guard
	if base == nil {
		base = tools.FetchGuard
	}
	guard := func(ctx context.Context, raw string) error {
		if gerr := base(ctx, raw); gerr != nil {
			return gerr
		}
		gu, perr := url.Parse(raw)
		if perr != nil {
			return fmt.Errorf("%w: unparseable token endpoint", tools.ErrEgressBlocked)
		}
		gh, gp := urlHostPort(gu)
		if !strings.EqualFold(gh, th) || gp != tp {
			return fmt.Errorf("%w: a token refresh dials only its authority (%s:%d)", tools.ErrEgressBlocked, th, tp)
		}
		return nil
	}
	client := tools.GuardedClient(30*time.Second, guard, h.cfg.Transport)
	src, err := oauth.NewProfileSource(profile.TokenFile, params, client)
	if err != nil {
		return nil, oauthDenial(name, err)
	}
	if h.oauthSrc == nil {
		h.oauthSrc = map[string]*oauth.Source{}
	}
	h.oauthSrc[name] = src
	return src, nil
}

// .
// .
func (b *Binding) receiptAuthRefresh(profile string, err error) {
	if b == nil || b.host == nil || b.host.cfg.Store == nil {
		return
	}
	now := time.Now().UTC()
	detail := "refreshed"
	if err != nil {
		switch {
		case errors.Is(err, oauth.ErrGrantInvalid):
			detail = "refresh refused: the authority no longer accepts the grant"
		case errors.Is(err, os.ErrNotExist):
			detail = "not connected: no token file"
		default:
			detail = "refresh failed"
		}
	}
	rec := externalReceipt{
		Success: err == nil, TransportOutcome: true, AuditPersisted: true, HostAuthored: true,
		ID:        fmt.Sprintf("auth-%d-%d", now.UnixNano(), b.host.counter.Add(1)),
		Timestamp: now.Format(time.RFC3339Nano),
		PluginID:  b.pluginID, PluginVersion: b.release.Version, PackageHash: b.release.PackageHash,
		Tier: b.tier.String(), Operation: opAuthRefresh, Target: "auth_profile:" + profile, Detail: detail,
	}
	raw, merr := json.Marshal(rec)
	if merr != nil {
		return
	}
	_ = b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, opAuthRefresh, rec.Target, err == nil, raw)
}

// .
// .
const opAuthRefresh = "auth.refresh"

// .
// .
// .
// .
func (h *Host) publishLock(pluginID, root, rel string) func() {
	key := pluginID + "\x00" + root + "\x00" + rel
	h.publishMu.Lock()
	if h.publishLocks == nil {
		h.publishLocks = map[string]*sync.Mutex{}
	}
	mu := h.publishLocks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		h.publishLocks[key] = mu
	}
	h.publishMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// .
// .
// .
// .
const (
	ReasonNetEffectUnknown = reasonNetEffectUnknown
	ReasonArgumentInvalid  = reasonArgumentInvalid
)

// .
// .
// .
const CredentialPlaceholder = "{credential}"

// .
// .
// .
// .
func localTarget(host string, port int, scopes []tools.LocalScope) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return tools.IsLocalIP(ip.Unmap().AsSlice())
	}
	for _, s := range scopes {
		if s.CoversName(host, port) {
			return true
		}
	}
	return false
}

// .
// .
func envelopeHas(envelope []string, capability string) bool {
	for _, c := range envelope {
		if c == capability {
			return true
		}
	}
	return false
}

// .
// .
// .
func (b *Binding) scopeDeniesLocal() ([]byte, bool) {
	sc := b.scope.Load()
	if sc == nil || !sc.Declared {
		return nil, false
	}
	for _, c := range sc.Capabilities {
		if c == capNetLocal {
			return nil, false
		}
	}
	r, _ := errorReply(-32000,
		fmt.Sprintf("operation %s did not declare %s among its capabilities", sc.Operation, capNetLocal),
		&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	return r, true
}
