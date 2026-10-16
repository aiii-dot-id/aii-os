package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	methodInvokeCall = "invoke-call"

	opHTTPGet = "http.get"
	opKVPut   = "kv.put"

	opHTTPPost   = "http.post"
	opHTTPPut    = "http.put"
	opHTTPPatch  = "http.patch"
	opHTTPDelete = "http.delete"

	opHTTPRead  = "http.read"
	opHTTPClose = "http.close"
	opKVGet     = "kv.get"
	opKVDelete  = "kv.delete"

	opVoiceObserve = "voice.observe"

	opKVList = "kv.list"

	opEmbeddingsCreate = "embeddings.create"

	opSettingsGet = "settings.get"

	opToolsPublish  = "tools.publish"
	opToolsWithdraw = "tools.withdraw"

	capToolsPublish = "tools.publish"

	capNetOutbound = "net.outbound"

	capNetLocal = "net.local"

	capRing4KV = "ring4.kv"

	capVoiceObserve = "voice.observe"

	capModelEmbeddings = "model.embeddings"
)

const (
	maxUtteranceBytes = 16 << 10

	maxSpeakerLabelBytes = 64

	reasonNotInEnvelope = "CAPABILITY_NOT_IN_STATIC_ENVELOPE"
	reasonPolicyDeny    = "POLICY_DENY"

	reasonValidationDeny = "VALIDATION_DENY"
	reasonTierDenied     = "capability_policy_denied"

	reasonOpNotAllowed = "OPERATION_NOT_ALLOWED_FOR_CAPABILITY"

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

	reasonAuthDisconnected = "NET_AUTH_PROFILE_DISCONNECTED"
	reasonNetHeaderInvalid = "NET_HEADER_INVALID"

	reasonNetEffectUnknown = "NET_EFFECT_UNKNOWN"

	reasonNetRequestTooBig = "NET_REQUEST_TOO_LARGE"

	reasonNetStreamLimit = "NET_STREAM_LIMIT_EXCEEDED"

	reasonKVNotFound      = "KV_NOT_FOUND"
	reasonKVValueTooLarge = "KV_VALUE_TOO_LARGE"
	reasonKVQuotaExceeded = "KV_QUOTA_EXCEEDED"

	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusDenied    = "denied"
	deniedAtCapEval = "capability_evaluation"

	bearerPrefix = "Bearer "
)

func NoEffect(reason string) bool {
	switch reason {
	case reasonNotInEnvelope, reasonPolicyDeny, reasonTierDenied, reasonOpNotAllowed,
		reasonTargetInvalid, reasonArgumentInvalid, reasonNetUnknownArgument,
		reasonNetTimeoutLimit, reasonNetRequestTooBig, reasonNetHeaderInvalid,
		reasonAuthInvalid, reasonAuthUnavailable, reasonAuthScopeMismatch,
		reasonAuthSecretMissing, reasonAuthNotAdmitted, reasonAuthRequiresHTTPS,
		reasonAuthDisconnected:
		return true
	}
	return false
}

const (
	DefaultMaxResponseBytes = 768 << 10

	maxOperationResultBytes = bbb.MaxControlFrameBytes - 8192

	DefaultMaxRequestBytes = 256 << 10

	MaxRequestHeaders     = 16
	MaxRequestHeaderBytes = 4096

	MaxCredentialBytes = MaxRequestHeaderBytes - len(bearerPrefix)

	MaxStreamChunkBytes = 64 << 10

	DefaultMaxStreamBytes = 8 << 20

	MaxOpenStreams = 4

	DefaultHTTPTimeout = 10 * time.Second
	MaxHTTPTimeout     = bbb.MaxHTTPTimeout

	DefaultMaxKVKeyBytes = 256

	DefaultMaxKVValueBytes = 64 << 10

	DefaultMaxKVKeys = 256

	DefaultMaxKVTotalBytes = 1 << 20
)

type Grant struct {
	Voice bool `json:"voice,omitempty"`

	KV bool `json:"kv,omitempty"`

	Hosts []string `json:"hosts,omitempty"`

	Local []string `json:"local,omitempty"`

	PlaintextCredentials bool `json:"plaintext_credentials,omitempty"`

	AutoConfirm []string `json:"auto_confirm,omitempty"`

	ReadOnly bool `json:"read_only,omitempty"`

	CredentialHandles []string `json:"credential_handles,omitempty"`

	Embeddings bool `json:"embeddings,omitempty"`

	Memory       bool `json:"memory,omitempty"`
	Interactions bool `json:"interactions,omitempty"`

	Files bool `json:"files,omitempty"`

	Tools bool `json:"tools,omitempty"`
}

func (g Grant) isEmpty() bool {
	return !g.Voice && !g.KV && !g.Embeddings && !g.Memory && !g.Interactions && !g.Tools && !g.Files && len(g.Hosts) == 0 && len(g.Local) == 0 && len(g.CredentialHandles) == 0
}

type AuthProfile struct {
	SecretEnv  string `json:"secret_env,omitempty"`
	SecretFile string `json:"secret_file,omitempty"`

	Host string `json:"host"`
	Port int    `json:"port"`

	Scheme string `json:"scheme,omitempty"`

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

const SchemeOAuth2 = "oauth2"

func (p AuthProfile) Contract(catalog ...map[string]oauth.Provider) (oauth.OAuthParams, oauth.Provider, error) {
	var providers map[string]oauth.Provider
	if len(catalog) > 0 {
		providers = catalog[0]
	}
	var tpl oauth.Provider
	if p.Provider != "" && p.Provider != "custom" {
		t, ok := oauth.ProviderTemplate(p.Provider, providers)
		if !ok {
			return oauth.OAuthParams{}, oauth.Provider{}, fmt.Errorf("auth profile names provider %q, which this host has no template for (%s, or custom with the endpoints inline)", p.Provider, strings.Join(oauth.ProviderNames(providers), ", "))
		}
		tpl = t
	}
	params := tpl.Params()
	if p.ClientID != "" {
		params.ClientID = p.ClientID
	}
	if p.RedirectURI != "" {
		params.RedirectURI = p.RedirectURI
	}
	params.Scope = strings.Join(append(append([]string(nil), tpl.BaseScopes...), p.Scopes...), " ")
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
	if params.TokenURL == "" || params.ClientID == "" || p.TokenFile == "" || len(tpl.Hosts) == 0 {
		return oauth.OAuthParams{}, oauth.Provider{}, errors.New("an oauth2 auth profile needs a provider (or custom endpoints), a client_id, a token_file and at least one host")
	}
	return params, tpl, nil
}

type Config struct {
	Interactions interaction.Reader

	Store *store.Store

	Later func(job func()) bool

	Voice VoiceObserver

	Embed Embedder

	Meaning memory.Embedder

	InSAFE func() bool

	Grants map[string]Grant

	AuthProfiles   map[string]AuthProfile
	OAuthProviders map[string]oauth.Provider

	ProfilesDir string

	ObserveFetch func(url string)

	Guard func(ctx context.Context, rawURL string) error

	OwnListener func(ip net.IP, port int) bool

	Transport http.RoundTripper

	MaxResponseBytes int
	MaxRequestBytes  int
	MaxStreamBytes   int
	MaxFilesBytes    int

	SlowStorageCall time.Duration

	Sandbox         Sandbox
	MaxKVKeyBytes   int
	MaxKVValueBytes int
	MaxKVKeys       int
	MaxKVTotalBytes int

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

type Host struct {
	cfg Config

	instruments *memory.Facility
	counter     atomic.Uint64

	policyMu sync.RWMutex

	publishMu    sync.Mutex
	publishLocks map[string]*sync.Mutex
	policy       policySnapshot

	oauthMu  sync.Mutex
	oauthSrc map[string]*oauth.Source

	afterSnapshot func()

	fsWriteLook func(osRoot *os.Root, rel string) (fs.FileInfo, error)

	fsPublishLook func(osRoot *os.Root, rel string)

	fsDeleteLook func(osRoot *os.Root, rel string)

	fsReadLook func(osRoot *os.Root, rel string)
}

func New(cfg Config) (*Host, error) {
	if cfg.Store == nil {
		return nil, errors.New("broker: refusing to build without a store — effects without host-authored receipts are the A3 hole")
	}
	instruments := memory.New(cfg.Store)
	if cfg.Meaning != nil {
		instruments.SetEmbedder(cfg.Meaning)
	}
	return &Host{cfg: cfg, instruments: instruments, policy: policySnapshot{grants: cfg.Grants, profiles: cfg.AuthProfiles, oauth: cfg.OAuthProviders}}, nil
}

func (h *Host) ReadsByMeaning() bool { return h.cfg.Meaning != nil }

func (h *Host) Bind(pluginID string, tier packagefmt.Tier, envelope []string) *Binding {
	return h.BindRelease(pluginID, tier, envelope, Release{})
}

type Release struct {
	Version     string
	PackageHash string
}

func (h *Host) BindRelease(pluginID string, tier packagefmt.Tier, envelope []string, release Release) *Binding {
	if h == nil {
		return nil
	}

	lifetime, cancel := context.WithCancel(context.Background())
	return &Binding{host: h, pluginID: pluginID, tier: tier, envelope: envelope, release: release,
		activation: rand.Text(), lifetime: lifetime, cancel: cancel}
}

type Binding struct {
	host     *Host
	pluginID string
	tier     packagefmt.Tier
	envelope []string
	release  Release

	activation string
	lifetime   context.Context
	cancel     context.CancelFunc

	lifecycleMu  sync.Mutex
	storageCalls int
	storageIdle  chan struct{}
	cleaning     chan struct{}

	scope atomic.Pointer[OperationScope]

	settings func() map[string]any
	declared func() map[string]any

	streamsMu sync.Mutex
	streams   map[string]*httpStream
	streamSeq uint64
	streamCap int

	streamsHeld int

	privateDir     string
	filesCap       int
	filesMu        sync.Mutex
	sandboxWritten int

	publisher Publisher

	closed atomic.Bool
}

type policySnapshot struct {
	oauth    map[string]oauth.Provider
	grants   map[string]Grant
	profiles map[string]AuthProfile
}

func (h *Host) ReplacePolicy(grants map[string]Grant, profiles map[string]AuthProfile, catalog ...map[string]oauth.Provider) {
	if h == nil {
		return
	}
	h.policyMu.Lock()
	providers := h.policy.oauth
	if len(catalog) > 0 {
		providers = catalog[0]
	}
	h.policy = policySnapshot{grants: grants, profiles: profiles, oauth: providers}
	h.oauthMu.Lock()
	h.oauthSrc = nil
	h.oauthMu.Unlock()
	h.policyMu.Unlock()
}

func (h *Host) snapshot() policySnapshot {
	h.policyMu.RLock()
	defer h.policyMu.RUnlock()
	return h.policy
}

func (s policySnapshot) grant(pluginID string) Grant { return s.grants[pluginID] }

func (b *Binding) storageScope() store.PluginScope {
	return store.PluginScope{PluginID: b.pluginID, Activation: b.activation}
}

func (b *Binding) ClearTempScope() error {
	return b.clearTempScope(context.Background(), false)
}

func (b *Binding) Close() error {
	return b.CloseContext(context.Background())
}

func (b *Binding) CloseContext(ctx context.Context) error {
	return b.clearTempScope(ctx, true)
}

func (b *Binding) clearTempScope(ctx context.Context, retire bool) error {
	if b == nil {
		return nil
	}
	for {
		b.lifecycleMu.Lock()
		if retire {
			b.closed.Store(true)
		}
		if done := b.cleaning; done != nil {
			b.lifecycleMu.Unlock()
			if retire {
				b.cancel()
			}
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		done := make(chan struct{})
		b.cleaning = done
		idle := b.storageIdle
		b.lifecycleMu.Unlock()
		defer func() {
			b.lifecycleMu.Lock()
			b.cleaning = nil
			close(done)
			b.lifecycleMu.Unlock()
		}()
		if retire {
			b.cancel()
			b.endStreams("closed with the activation")
		}
		if idle != nil {
			select {
			case <-idle:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.Join(b.clearTempFiles(),
			leftUnderSAFE(b.host.cfg.Store.PluginKVClearTemp(b.storageScope())),
			leftUnderSAFE(b.host.cfg.Store.PluginMemoryClearTemp(b.storageScope())))
	}
}

func leftUnderSAFE(err error) error {
	if errors.As(err, new(*store.FrozenError)) {
		return nil
	}
	return err
}

func (b *Binding) admitStorage(ctx context.Context) (context.Context, func(), bool) {
	b.lifecycleMu.Lock()
	if b.closed.Load() || b.cleaning != nil {
		b.lifecycleMu.Unlock()
		return ctx, nil, false
	}
	if b.storageCalls == 0 {
		b.storageIdle = make(chan struct{})
	}
	b.storageCalls++
	b.lifecycleMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.lifetime, cancel)
	return ctx, func() {
		stop()
		cancel()
		b.lifecycleMu.Lock()
		b.storageCalls--
		if b.storageCalls == 0 {
			close(b.storageIdle)
			b.storageIdle = nil
		}
		b.lifecycleMu.Unlock()
	}, true
}

func (b *Binding) SetStreamCap(n int) {
	if b == nil {
		return
	}
	b.streamCap = n
}

func (b *Binding) PluginID() string { return b.pluginID }

type rpcError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *errorData `json:"data,omitempty"`
}

type errorData struct {
	ReasonCode string `json:"reasonCode"`
	DeniedAt   string `json:"denied_at,omitempty"`
}

type invokeParams struct {
	Operation           string          `json:"operation"`
	PluginOperation     string          `json:"plugin_operation"`
	Target              json.RawMessage `json:"target"`
	Arguments           json.RawMessage `json:"arguments"`
	WorkDoneToken       json.RawMessage `json:"work_done_token"`
	Grant               json.RawMessage `json:"grant"`
	ParentRuntimeCallID string          `json:"parent_runtime_call_id"`
}

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

	Method string `json:"method,omitempty"`
	Effect string `json:"effect,omitempty"`
}

const (
	EffectPerformed    = "performed"
	EffectNotPerformed = "not-performed"
	EffectUnknown      = "unknown"
)

const retryHintUnavailable = "retry hint unavailable: no usable Retry-After"

const (
	EffectsReadInternal  = "read.internal"
	EffectsReadExternal  = "read.external"
	EffectsWriteLocal    = "write.local"
	EffectsWriteExternal = "write.external"
	EffectsExec          = "exec"
)

func KnownEffects(s string) bool {
	switch s {
	case "", EffectsReadInternal, EffectsReadExternal, EffectsWriteLocal, EffectsWriteExternal, EffectsExec:
		return true
	}
	return false
}

func IsReadEffect(effects string) bool {
	return effects == EffectsReadInternal || effects == EffectsReadExternal
}

type OperationScope struct {
	Operation     string
	Effects       string
	Capabilities  []string
	Declared      bool
	Validation    bool
	WorkSession   string
	SessionReason string
	CallID        string
}

func (b *Binding) BeginOperation(s OperationScope) {
	if b == nil {
		return
	}
	b.scope.Store(&s)
}

func (b *Binding) EndOperation() {
	if b == nil {
		return
	}
	b.scope.Store(nil)

	b.endStreams("closed at the operation's end")
}

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

	return p.Operation == opSettingsGet || (strings.HasPrefix(p.Operation, "fs.") && p.Target.Root == PrivateRoot)
}

type PublishedTool struct {
	Name         string         `json:"name"`
	Summary      string         `json:"summary"`
	Input        map[string]any `json:"input"`
	Effects      string         `json:"effects"`
	Capabilities []string       `json:"capabilities"`
	Family       string         `json:"family,omitempty"`
}

type Publisher interface {
	PublishTool(spec PublishedTool) (registryName string, err error)
	WithdrawTool(name string) error
}

func (b *Binding) SetPublisher(p Publisher) {
	if b == nil {
		return
	}
	b.publisher = p
}

func (b *Binding) dispatchTools(p invokeParams, grant Grant) ([]byte, error) {
	if !b.tier.PublisherProven() {
		return errorReply(rpcForbidden,
			fmt.Sprintf("tier %s holds no %s ceiling (a plugin below a publisher-proven tier cannot grow the identity's tools)", b.tier, capToolsPublish),
			&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
	}
	if !b.envelopeHas(capToolsPublish) {
		return errorReply(rpcForbidden, capToolsPublish+" is not in the signed capability envelope",
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !grant.Tools {
		return errorReply(rpcForbidden, fmt.Sprintf("no operator grant lets plugin %s publish tools (plugins.grants.%s.tools)", b.pluginID, b.pluginID),
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
		or, _ := json.Marshal(map[string]any{"name": target.Name, "withdrawn": true})
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
	or, _ := json.Marshal(map[string]any{"name": spec.Name, "tool": registryName, "published": true})
	return b.resultReply(p.Operation, p.PluginOperation, spec.Name, outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

func (b *Binding) SetSettings(fn func() map[string]any) {
	if b == nil {
		return
	}
	b.settings = fn
}

func (b *Binding) SetDeclaredSettings(fn func() map[string]any) {
	if b == nil {
		return
	}
	b.declared = fn
}

func (b *Binding) dispatchSettings(p invokeParams) ([]byte, error) {
	if denial, denied := b.scopeDenies(opSettingsGet, ""); denied {
		return denial, nil
	}
	source := b.settings
	if sc := b.scope.Load(); sc != nil && sc.Validation {
		source = b.declared
	}
	values := map[string]any{}
	if source != nil {
		if got := source(); got != nil {
			values = got
		}
	}
	or, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		return nil, fmt.Errorf("broker: settings.get encode: %w", err)
	}
	return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

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

func forbiddenRequestHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "host", "content-length", "transfer-encoding", "connection", "cookie", "set-cookie", "upgrade", "te", "trailer", "user-agent":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "proxy-")
}

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

func (b *Binding) scopeDenies(op, capability string) ([]byte, bool) {
	sc := b.scope.Load()
	if sc == nil {
		return nil, false
	}
	if !effectsAllow(sc.Effects, op) {
		r, _ := errorReply(rpcForbidden,
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
		r, _ := errorReply(rpcForbidden,
			fmt.Sprintf("operation %s did not declare %s among its capabilities", sc.Operation, capability),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
		return r, true
	}
	return nil, false
}

func (b *Binding) scopeDeniesNet(host string, port int) ([]byte, bool) {
	sc := b.scope.Load()
	if sc == nil || !sc.Declared {
		return nil, false
	}
	if anyScopeMatches(netEnvelopeScopes(sc.Capabilities), host, port) {
		return nil, false
	}
	r, _ := errorReply(rpcForbidden,
		fmt.Sprintf("operation %s did not declare %s:%s:%d among its capabilities", sc.Operation, capNetOutbound, host, port),
		&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	return r, true
}

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
	keys, truncated, err := b.host.cfg.Store.PluginKVList(b.storageScope(), target.Prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("broker: kv.list: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	or, _ := json.Marshal(map[string]any{"prefix": target.Prefix, "keys": keys, "truncated": truncated})
	return b.resultReply(p.Operation, p.PluginOperation, target.Prefix, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

func (b *Binding) dispatchEmbeddings(ctx context.Context, p invokeParams, g Grant) ([]byte, error) {
	if !b.tier.PublisherProven() {
		return errorReply(rpcForbidden,
			fmt.Sprintf("tier %s holds no %s ceiling (the trust contract backs no external effects below a publisher-proven tier)", b.tier, capModelEmbeddings),
			&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
	}
	if !b.envelopeHas(capModelEmbeddings) {
		return errorReply(rpcForbidden,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capModelEmbeddings, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.Embeddings {
		return errorReply(rpcForbidden,
			fmt.Sprintf("embeddings are not granted to plugin %s; the operator grants them in plugins.grants.%s.embeddings", b.pluginID, b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if r, denied := b.scopeDenies(p.Operation, capModelEmbeddings); denied {
		return r, nil
	}
	if b.host.cfg.Embed == nil {
		return errorReply(rpcForbidden, "this host serves no embeddings",
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
	or, merr := json.Marshal(map[string]any{"model": model, "dimensions": dims, "vectors": vectors})
	if merr != nil {
		return nil, fmt.Errorf("broker: embeddings.create: %w", merr)
	}
	return b.resultReply(p.Operation, p.PluginOperation, model, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

const opInvokeResult = "invoke.result"

func (b *Binding) InvokeRefused(operation, detail, effect string) {
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
		Effect:          effect,
	}

	raw, err := json.Marshal(rec)
	if err == nil {
		err = b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, opInvokeResult, "", false, raw)
	}
	if err != nil {
		logsink.Error("broker.error", "%s: the receipt of a refused %s answer was not recorded: %v", b.pluginID, operation, err)
	}
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

const slowStorageCallDefault = time.Second

func (b *Binding) noteSlowStorage(operation string, took time.Duration) {
	from := b.host.cfg.SlowStorageCall
	if from <= 0 {
		from = slowStorageCallDefault
	}
	if took < from {
		return
	}
	logsink.Warn("broker.budget", "plugin %s: %s took %s on this machine's storage (said from %s: plugins.runtime.slow_storage_call_ms)", b.pluginID, operation, took.Round(time.Millisecond), from)
}

func (b *Binding) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {

	if b == nil {
		return errorReply(rpcForbidden, "this plugin holds no capability binding",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	pol := b.host.snapshot()
	if b.host.afterSnapshot != nil {
		b.host.afterSnapshot()
	}

	if pol.grant(b.pluginID).isEmpty() && !asksSettings(method, params) {
		return errorReply(rpcForbidden,
			fmt.Sprintf("no capability is granted to plugin %s; invoke-call denied", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if b.closed.Load() {

		return errorReply(rpcForbidden, "this plugin's activation has ended",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	reply, err := b.dispatch(ctx, method, params, pol)
	if err != nil {
		return nil, err
	}
	if len(reply) > bbb.MaxControlFrameBytes {

		return nil, fmt.Errorf("broker: produced a %d-byte reply over the %d-byte plugin frame ceiling", len(reply), bbb.MaxControlFrameBytes)
	}
	return reply, nil
}

func (b *Binding) dispatch(ctx context.Context, method string, params []byte, pol policySnapshot) ([]byte, error) {
	if method != methodInvokeCall {

		return errorReply(rpcForbidden, fmt.Sprintf("no %s surface in the step-4 broker; denied", method),
			&errorData{ReasonCode: reasonPolicyDeny})
	}

	var p invokeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorReply(-32602, "params must be a JSON object", nil)
	}
	if p.Operation == "" {

		return errorReply(-32602, "operation (string) required", nil)
	}
	if len(p.Grant) > 0 {

		return errorReply(-32602, "grant is retired; invoke.call evaluates capability per request", nil)
	}
	if len(p.WorkDoneToken) > 0 {

		return errorReply(-32602, "work_done_token requires negotiated rpc.cancel capability", nil)
	}

	if sc := b.scope.Load(); sc != nil && sc.Validation && !answersChecks(p.Operation) {
		return errorReply(rpcForbidden,
			fmt.Sprintf("operation %s is one of its publisher's checks, which read nothing of the identity's and write nothing; %s is refused during one", sc.Operation, p.Operation),
			&errorData{ReasonCode: reasonValidationDeny, DeniedAt: deniedAtCapEval})
	}

	if !passesSAFEGate(p.Operation) && b.host.cfg.InSAFE != nil && b.host.cfg.InSAFE() {
		return errorReply(rpcForbidden,
			fmt.Sprintf("this identity is in SAFE; %s is refused while it holds", p.Operation),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	if pol.grant(b.pluginID).ReadOnly && opMutates(p.Operation) {
		return errorReply(rpcForbidden,
			fmt.Sprintf("plugin %s is granted read only; %s writes", b.pluginID, p.Operation),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	if usesStorage(p.Operation) {
		var finish func()
		var admitted bool
		ctx, finish, admitted = b.admitStorage(ctx)
		if !admitted {
			return errorReply(rpcForbidden, "this plugin's storage is retiring or being cleared",
				&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
		}
		defer finish()
		began := time.Now()
		defer func() { b.noteSlowStorage(p.Operation, time.Since(began)) }()
	}

	switch p.Operation {
	case opInteractionQuery, opInteractionRead:
		return b.dispatchInteraction(ctx, p, pol.grant(b.pluginID))
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

		return errorReply(rpcForbidden,
			fmt.Sprintf("operation %q is not allowed for any capability this broker serves", p.Operation),
			&errorData{ReasonCode: reasonOpNotAllowed, DeniedAt: deniedAtCapEval})
	}
}

const rpcForbidden = -32000

func errorReply(code int, message string, data *errorData) ([]byte, error) {
	raw, err := json.Marshal(rpcError{Code: code, Message: message, Data: data})
	if err != nil {
		return nil, fmt.Errorf("broker: encode error reply: %w", err)
	}
	return raw, nil
}

func (b *Binding) resultReply(operation, pluginOperation, target string, o outcome) ([]byte, error) {

	if len(o.operationResult) > maxOperationResultBytes {
		o = outcome{status: statusFailed, reason: reasonNetResponseTooBig,
			transportOK: o.transportOK, protocolStatus: o.protocolStatus,
			method: o.method, effect: o.effect,
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

	deferred := false
	if later := b.host.cfg.Later; later != nil && answersFirst(operation) {
		stored, pluginID, success := recRaw, b.pluginID, rec.Success
		deferred = later(func() {
			if err := b.host.cfg.Store.AppendPluginReceipt(rec.ID, pluginID, operation, target, success, stored); err != nil {
				logsink.Error("broker.error", "%s: the receipt of a %s answer was not recorded: %v", pluginID, operation, err)
			}
		})
	}
	if deferred {
		pending := rec
		pending.AuditPersisted = false
		if recRaw, err = json.Marshal(pending); err != nil {
			return nil, fmt.Errorf("broker: encode receipt: %w", err)
		}
	} else if serr := b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, operation, target, rec.Success, recRaw); serr != nil {

		rec.AuditPersisted = false
		if recRaw, err = json.Marshal(rec); err != nil {
			return nil, fmt.Errorf("broker: encode receipt: %w", err)
		}
	}

	var reply any
	if o.status == statusSucceeded {
		reply = successResult{Success: true, OK: true, Status: statusSucceeded,
			OperationResult: o.operationResult, ExternalReceipt: recRaw}
	} else {

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

type VoiceObserver interface {
	Observe(VoiceObservation) error
}

type Embedder interface {
	Embed(ctx context.Context, inputs []string) (model string, vectors [][]float32, err error)
}

const (
	maxEmbedInputs     = 16
	maxEmbedInputBytes = 8 << 10
)

type VoiceObservation struct {
	PluginID string

	Text string

	Speaker string

	SpeakerScore float64
}

func (b *Binding) voiceRings(capability string, g Grant) ([]byte, bool) {
	if !b.envelopeHas(capability) {
		r, _ := errorReply(rpcForbidden,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capability, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
		return r, false
	}
	if !g.Voice {
		r, _ := errorReply(rpcForbidden,
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
		return errorReply(rpcForbidden,
			"this host accepts no voice observations",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	if b.host.cfg.InSAFE != nil && b.host.cfg.InSAFE() {
		return errorReply(rpcForbidden,
			"this identity is in SAFE; nothing heard is recorded while it holds",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	var args struct {
		Text    string `json:"text"`
		Speaker string `json:"speaker"`

		SpeakerScore float64 `json:"speaker_score"`
	}
	if len(p.Arguments) > 0 {

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

	if !b.envelopeHas(capRing4KV) {
		return errorReply(rpcForbidden,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capRing4KV, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.KV {
		return errorReply(rpcForbidden,
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
		err := st.PluginKVPut(b.storageScope(), key, *args.Value, temp, b.host.cfg.maxKVKeys(), b.host.cfg.maxKVTotalBytes())
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
		or, _ := json.Marshal(map[string]any{"stored": true, "key": key, "value_bytes": len(*args.Value), "scope": scope})
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})

	case opKVGet:
		value, found, err := st.PluginKVGet(b.storageScope(), key)
		if err != nil {
			return nil, fmt.Errorf("broker: kv.get: %w", err)
		}
		if !found {
			return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
				status: statusFailed, reason: reasonKVNotFound, transportOK: true})
		}
		or, merr := json.Marshal(map[string]any{"key": key, "value": value})
		if merr != nil {
			return nil, fmt.Errorf("broker: kv.get encode: %w", merr)
		}
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})

	default:
		deleted, err := st.PluginKVDelete(b.storageScope(), key, temp)
		if err != nil {
			return nil, fmt.Errorf("broker: kv.delete: %w", err)
		}
		or, _ := json.Marshal(map[string]any{"deleted": deleted, "key": key})
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})
	}
}

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

const (
	ReasonNetEffectUnknown = reasonNetEffectUnknown
	ReasonArgumentInvalid  = reasonArgumentInvalid
)

const CredentialPlaceholder = "{credential}"

func localTarget(host string, port int, scopes []firewall.LocalScope) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return firewall.IsLocalIP(ip.Unmap().AsSlice())
	}
	for _, s := range scopes {
		if s.CoversName(host, port) {
			return true
		}
	}
	return false
}

func (b *Binding) envelopeHas(capability string) bool {
	return slices.Contains(b.envelope, capability)
}

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
	r, _ := errorReply(rpcForbidden,
		fmt.Sprintf("operation %s did not declare %s among its capabilities", sc.Operation, capNetLocal),
		&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	return r, true
}
