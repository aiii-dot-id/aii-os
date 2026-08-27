// Package broker is the capability broker — build-order step 4
// (PLUGIN_FRAMEWORK.md §15), the ONLY path from a quarantined plugin to
// any external effect (PLUGIN_THREAT_MODEL.md §2: the third boundary).
// It replaces the worker's deny-all HostDispatcher stub through the one
// seam step 3 left for it (pluginworker.Config.Dispatcher).
//
// The model is three intersections, not a policy engine (§6: "effective
// = tier ceiling ∩ signed package policy ∩ local activation grant",
// evaluated per invocation, never retained): the manifest's signed
// capability envelope, the operator's grant in the substrate-protected
// config, and the trust-tier ceiling derived from the shared contract
// data. Each ring narrows; none widens the ring above; nothing is
// cached across calls — a grant is not a socket.
//
// Vocabulary is ADOPTED from the C daemon, never invented (audited
// citations at each constant): operations ride `invoke.call` exactly as
// the daemon dispatches them (rpc.c handle_invoke_call; BBB_V2_AUDIT
// §6.3) — `http.get` (sev_operations.h:87) under the `net.outbound`
// capability (operation_capability_registry.c), targets as
// {"url": ...} objects (rpc_capability.c:331-338), credentials as the
// `auth_profile` argument resolved from operator config, injected as
// the Authorization header, never handed to the plugin (operations.c;
// PLUGIN_FRAMEWORK §6 "Secrets stay in the broker"). RING4 kv has NO C
// vocabulary (no kv operation exists anywhere in the daemon or SDK —
// recorded finding); its operations are minted in the daemon's own
// operation grammar (`kv.put`/`kv.get`/`kv.delete`, target {"key":..})
// and ride the same invoke.call frames.
//
// For every result the broker answers, IT authors the external_receipt
// and the store record (trust-tiers.json invariants:
// plugin_output_is_not_proof, host_authored_receipts_are_proof; the
// daemon-injects rule, invoke_contract.c:598-628). Denial verdicts on
// the capability rings use the audited JSON-RPC error shape: code
// -32000 (SEV_RPC_ERR_FORBIDDEN, sev_rpc.h:84), camelCase reasonCode,
// denied_at "capability_evaluation"
// (SEV_OPERATION_DISPATCH_PHASE_CAPABILITY_EVALUATION,
// sev_operations.h:174) — the same shape the step-3 deny-all stub
// speaks, so an SDK classifies both identically.
package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// --- adopted wire vocabulary (every string cited to its C owner) ---

const (
	// methodInvokeCall is the WIT kebab name of the one import the
	// step-4 broker serves (bbbImportNames order, ADR-033 Decision 6);
	// its JSON-RPC identity is invoke.call (sev_method_ids.h
	// SEV_METHOD_INVOKE_CALL).
	methodInvokeCall = "invoke-call"

	// Operations the broker implements. http.get/http.post are the
	// net.outbound pair (sev_operations.h:87-88;
	// operation_capability_registry.c maps both to SEV_CAP_NET_OUTBOUND)
	// — http.post is NOT implemented in this MVP (recorded finding; the
	// GET plumbing is the reused web_fetch path). The kv triple is
	// Go-minted in the daemon's operation grammar (no C kv vocabulary
	// exists — recorded finding).
	opHTTPGet  = "http.get"
	opKVPut    = "kv.put"
	opKVGet    = "kv.get"
	opKVDelete = "kv.delete"

	// opVoiceObserve carries one heard utterance upward.
	opVoiceObserve = "voice.observe"

	// capNetOutbound is the capability the http operations require
	// (SEV_CAP_NET_OUTBOUND, sev_capability.h:81; scope kind host_port,
	// declaration grammar "net.outbound:<host>[:<port>|:*]",
	// capability.c:338).
	capNetOutbound = "net.outbound"

	// capRing4KV is the storage capability — Go-minted 2026-08-19 at
	// the S5 co-build (DELTA_D1 D4-F1 addendum): access to the plugin's
	// scoped RING4 kv plane. Exact-match membership only; a scoped form
	// ("ring4.kv:...") is undefined in v0 and deliberately does not
	// match.
	capRing4KV = "ring4.kv"

	// capVoiceObserve is the capability a voice plugin needs to put
	// words in front of the identity. Go-minted like ring4.kv: no C
	// voice vocabulary exists yet.
	//
	// It IS a capability, deliberately. An observation is not a return
	// value — speech happens when it happens, and the eight-function
	// BBB surface gives a plugin invoke.call and no way to push — so an
	// utterance reaches the identity the way every other plugin effect
	// does. Making it a capability is also the honest description of
	// what it is: the operator granting one plugin permission to place
	// foreign words into their identity's conversation.
	capVoiceObserve = "voice.observe"
)

// Bounds on what one utterance may carry. Generous for speech and
// obviously insufficient as a payload channel, which is the point: a
// plugin that needs more than this is not describing a thing someone
// said.
const (
	// maxUtteranceBytes is roughly fifteen minutes of continuous speech.
	maxUtteranceBytes = 16 << 10
	// maxSpeakerLabelBytes is long enough for any name a person answers
	// to. The host bounds it again before it reaches prose.
	maxSpeakerLabelBytes = 64

	// Denial reason codes, one per lattice ring — all adopted:
	//  - outside the signed manifest envelope →
	//    CAPABILITY_NOT_IN_STATIC_ENVELOPE (sev_rpc.h:120-121)
	//  - no operator grant → POLICY_DENY (sev_rpc.h:124, the same
	//    audited code the step-3 deny-all stub speaks — the operator's
	//    silence IS the local policy)
	//  - tier ceiling → capability_policy_denied
	//    (SEV_CAPAUTH_AUDIT_REASON_POLICY_DENIED,
	//    sev_capability_authority.h:43; emitted on the wire by
	//    rpc_capability.c:114 and classified as DENIED by
	//    invoke_contract.c:242-243)
	reasonNotInEnvelope = "CAPABILITY_NOT_IN_STATIC_ENVELOPE"
	reasonPolicyDeny    = "POLICY_DENY"
	reasonTierDenied    = "capability_policy_denied"

	// reasonOpNotAllowed answers an operation outside the broker's
	// registry (SEV_OPERATION_REASON_OPERATION_NOT_ALLOWED_FOR_CAPABILITY
	// via rpc_capability.c:97; one of the audited five denial codes,
	// sev_bbb_client.h:29-37).
	reasonOpNotAllowed = "OPERATION_NOT_ALLOWED_FOR_CAPABILITY"

	// Execution-phase codes (result-level, operations.c / sev_operations.h):
	reasonTargetInvalid      = "OPERATION_TARGET_INVALID"            // :133 (NET_TARGET_INVALID aliases it)
	reasonArgumentInvalid    = "OPERATION_ARGUMENT_INVALID"          // audited five, sev_bbb_client.h
	reasonNetUnknownArgument = "NET_UNKNOWN_ARGUMENT"                // :135
	reasonNetResponseTooBig  = "NET_RESPONSE_TOO_LARGE"              // :137
	reasonNetRemoteFailed    = "NET_REMOTE_OUTCOME_FAILED"           // :138
	reasonNetTimeoutLimit    = "NET_TIMEOUT_LIMIT_EXCEEDED"          // :139
	reasonAuthInvalid        = "NET_AUTH_PROFILE_INVALID"            // :140
	reasonAuthUnavailable    = "NET_AUTH_PROFILE_UNAVAILABLE"        // :141
	reasonAuthScopeMismatch  = "NET_AUTH_PROFILE_SCOPE_MISMATCH"     // :142
	reasonAuthSecretMissing  = "NET_AUTH_PROFILE_SECRET_UNAVAILABLE" // :143-144
	reasonAuthNotAdmitted    = "NET_AUTH_PROFILE_NOT_ADMITTED"       // :145
	reasonAuthRequiresHTTPS  = "NET_AUTH_PROFILE_REQUIRES_HTTPS"     // :146
	reasonNetHeaderInvalid   = "NET_HEADER_INVALID"                  // operations.c:875 message's code

	// kv codes are Go-minted in the C reason grammar (recorded finding;
	// nearest C precedents: NET_BODY_TOO_LARGE / TOKEN_RECEIPT_NOT_FOUND).
	reasonKVNotFound      = "KV_NOT_FOUND"
	reasonKVValueTooLarge = "KV_VALUE_TOO_LARGE"
	reasonKVQuotaExceeded = "KV_QUOTA_EXCEEDED"

	// Statuses and phases (sev_json_fields.h:756,779-783 value strings
	// via BBB_V2_AUDIT §6.3; sev_operations.h:174).
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusDenied    = "denied"
	deniedAtCapEval = "capability_evaluation"

	// bearerPrefix is how the credential value rides the request:
	// Authorization: Bearer <secret> (SEV_AUTH_VALUE_BEARER_PREFIX,
	// sev_auth.h:25; SEV_OPERATION_HTTP_HEADER_AUTHORIZATION,
	// sev_operations.h:66).
	bearerPrefix = "Bearer "
)

// Ceilings — config-shaped: fields on Config with these defaults, so an
// operator surface can widen/narrow them later without touching code
// (the 2026-08-18 ruling: ceilings are config grouped with their
// subject; a ceiling in code is the author legislating).
const (
	// DefaultMaxResponseBytes bounds one brokered response body. WHY
	// 768 KiB and not the C daemon's 1 MiB (SEV_OPERATION_HTTP_BODY_MAX_BYTES,
	// sev_operations.h:49): the C cap rides a 16 MB daemon frame
	// (SEV_RPC_MAX_FRAME_SIZE); ours rides the audited 1 MiB PLUGIN-side
	// frame (bbb.MaxControlFrameBytes) inside a JSON result that also
	// carries the receipt — 3/4 of the frame leaves honest envelope
	// headroom instead of poisoning the module at the frame wall.
	DefaultMaxResponseBytes = 768 << 10

	// HTTP timeout bounds — adopted verbatim: default 10 s, max 60 s
	// (SEV_OPERATION_HTTP_TIMEOUT_DEFAULT_MS / _MAX_MS,
	// sev_operations.h:42-43).
	DefaultHTTPTimeout = 10 * time.Second
	MaxHTTPTimeout     = 60 * time.Second

	// DefaultMaxKVKeyBytes mirrors the C scope-name ceiling
	// (SEV_SCOPE_MAX_NAME 256, sev_capability.h): a kv key is a scope
	// name, not content.
	DefaultMaxKVKeyBytes = 256

	// DefaultMaxKVValueBytes: WHY 64 KiB — RING4 is a projection cache
	// for working state (PLUGIN_FRAMEWORK §8 "projection-only"), not a
	// blob store; one value stays far under the 1 MiB frame a get must
	// ride back through.
	DefaultMaxKVValueBytes = 64 << 10

	// DefaultMaxKVKeys: WHY 256 — a working set, not a database; the C
	// stack bounds every per-plugin table the same way (the
	// GRANT_TABLE_FULL precedent, sev_rpc.h:125).
	DefaultMaxKVKeys = 256

	// DefaultMaxKVTotalBytes: WHY 1 MiB — one frame-ceiling's worth of
	// durable projection per plugin; quotas trap at the ceiling instead
	// of letting a plugin turn the identity's store into its disk.
	DefaultMaxKVTotalBytes = 1 << 20
)

// Grant is one plugin's operator-granted authority — the middle ring,
// read from the substrate-protected config (plugins.grants.<id>).
// Listing an entry there is the operator's deliberate act; the zero
// value grants nothing.
type Grant struct {
	// Voice grants the plugin permission to submit heard utterances.
	// Separate from every other grant because it is a different kind of
	// thing: not the plugin reaching OUT, but the plugin placing words
	// IN — foreign text entering the identity's conversation, which the
	// operator should have to say yes to by name.
	Voice bool `json:"voice,omitempty"`
	// KV grants the plugin its scoped RING4 namespace. The TIER decides
	// the scope's lifetime (uncertified = temp, cleared at
	// (de)activation; publisher-proven = persistent) — an intersection,
	// not a second knob.
	KV bool `json:"kv,omitempty"`
	// Hosts lists approved net.outbound scopes, "host", "host:port" or
	// "host:*" — the same host_port scope grammar the manifest envelope
	// uses (capability.c:338), minus the capability prefix.
	Hosts []string `json:"hosts,omitempty"`
	// CredentialHandles lists the auth-profile names this plugin may
	// cite as the http auth_profile argument. The profile — and only
	// the profile — knows which secret rides it.
	CredentialHandles []string `json:"credential_handles,omitempty"`
}

// isEmpty reports whether the operator has granted this plugin nothing
// at all — the deny-all posture, which is an answer rather than an
// absence.
func (g Grant) isEmpty() bool {
	return !g.Voice && !g.KV && len(g.Hosts) == 0 && len(g.CredentialHandles) == 0
}

// AuthProfile is one named credential route, adopted from the C
// daemon's plugins.policy.auth_profiles.<name> table (sev_config.h:
// 294-297: secret_name, host, port). The secret value itself lives
// OUTSIDE the profile: SecretEnv/SecretFile is the config-stored
// reference the broker resolves at use time (the Go stack has no
// keystore organ yet — recorded; the C daemon resolves secret_name
// through its secret provider). The value is injected as the
// Authorization header of the one permitted request and is never
// written to a reply, a receipt, a log, or the store.
type AuthProfile struct {
	// Exactly one of SecretEnv (environment variable name) or
	// SecretFile (path, read at use time) names the credential source.
	SecretEnv  string `json:"secret_env,omitempty"`
	SecretFile string `json:"secret_file,omitempty"`
	// Host and Port pin the ONLY destination this credential may ride
	// to (operations.c: profile host:port must equal the target URL's —
	// NET_AUTH_PROFILE_SCOPE_MISMATCH otherwise).
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Config is the process-wide broker configuration (one Host serves
// every activated plugin; per-plugin identity arrives at Bind).
type Config struct {
	// Store persists kv rows and receipt records. Required.
	Store *store.Store
	// Voice receives heard utterances. Nil on a host with no voice
	// surface, and then voice.observe is refused rather than silently
	// accepted — an admitted effect that goes nowhere is worse than a
	// denial, because the plugin believes it landed.
	Voice VoiceObserver
	// InSAFE reports whether the identity is in SAFE. Nil means "never",
	// which is what a host with no mode has.
	//
	// SAFE IS A REFUSAL, ASKED EVERY TIME. It used to also be a close
	// condition — tear down the audio streams on entry, and hope none
	// reattached in the gap — which made SAFE a pause a plugin could
	// end rather than a state. Nothing is held now, so nothing has to
	// be swept: every voice.observe asks, and none lands while SAFE
	// holds. Operator presence and a T3 signature create no exception.
	// An identity that cannot trust its own record must not be quietly
	// recording a room into it.
	InSAFE func() bool
	// Grants is the operator grant table (config plugins.grants),
	// keyed by plugin id. A plugin with no entry gets NO broker — the
	// harness keeps its deny-all stub, so the zero-config posture is
	// exactly the step-3 quarantine.
	Grants map[string]Grant
	// AuthProfiles is the operator credential-route table (config
	// plugins.auth_profiles).
	AuthProfiles map[string]AuthProfile
	// ObserveFetch receives every URL a brokered request successfully
	// fetched (tools.Registry.NotifyFetch — the H3 provenance seam;
	// plugin fetches earn citations exactly like builtin fetches).
	ObserveFetch func(url string)
	// Guard is the egress policy every request and redirect hop must
	// pass. nil = tools.FetchGuard — the ONE policy web_fetch enforces.
	// Tests inject a narrower guard admitting their loopback fixture
	// server; production wiring leaves it nil.
	Guard func(rawURL string) error
	// Transport overrides the HTTP transport (tests pin their fixture
	// server's TLS certificate); nil = default transport.
	Transport http.RoundTripper

	// Ceilings; zero means the package default above.
	MaxResponseBytes int
	MaxKVKeyBytes    int
	MaxKVValueBytes  int
	MaxKVKeys        int
	MaxKVTotalBytes  int
}

func (c Config) maxResponseBytes() int {
	if c.MaxResponseBytes > 0 {
		return c.MaxResponseBytes
	}
	return DefaultMaxResponseBytes
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

// Host is the shared broker state. One per process, wired by the app;
// bindings are cheap per-plugin views onto it.
type Host struct {
	cfg     Config
	counter atomic.Uint64 // receipt-id mint (C precedent: minted invoke-<ns> ids, rpc.c:1501-1514)

	// policy is the operator's CURRENT answer, replaced whole when
	// configuration reloads.
	//
	// GRANTS AND AUTH PROFILES MOVE TOGETHER because they are one
	// generation of one file. A grant naming a credential handle and the
	// profile that defines it must never come from different reads: half
	// an update is an authority nobody wrote.
	policyMu sync.RWMutex
	policy   policySnapshot
}

// New builds the broker host. cfg.Store must be non-nil — a broker
// that cannot author receipts must not exist (host-authored receipts
// ARE the proof plane; performing effects without them would be the A3
// hole the design closes).
func New(cfg Config) (*Host, error) {
	if cfg.Store == nil {
		return nil, errors.New("broker: refusing to build without a store — effects without host-authored receipts are the A3 hole")
	}
	return &Host{cfg: cfg, policy: policySnapshot{grants: cfg.Grants, profiles: cfg.AuthProfiles}}, nil
}

// Bind scopes the host to one activated plugin. Structural scoping:
// everything identity-shaped (plugin id, tier, signed envelope) enters
// HERE, from the verifier's Result — never from plugin-supplied params.
// Returns nil when the operator granted this plugin nothing (no
// plugins.grants entry): the caller then keeps the worker's deny-all
// stub, so absence-of-grant IS the quarantine posture, unchanged. A nil
// *Host binds nothing for every plugin (no broker configured at all).
func (h *Host) Bind(pluginID string, tier packagefmt.Tier, envelope []string) *Binding {
	if h == nil {
		return nil
	}
	// ALWAYS A BINDING. This returned nil when the operator had granted
	// the plugin nothing, which made "no grant" a different SHAPE rather
	// than a different answer — a nil to check at every call site, a
	// second path to keep correct, and a plugin that could never be
	// granted anything later without being rebound. An empty grant
	// denies every ring on every invocation, which is the same outcome
	// with one fewer thing to get wrong.
	return &Binding{host: h, pluginID: pluginID, tier: tier, envelope: envelope}
}

// Binding is one plugin's live capability seam — a
// pluginworker.HostDispatcher. It holds NO evaluated authority: every
// Dispatch re-runs the three rings ("no retained permission token",
// PLUGIN_FRAMEWORK §6).
type Binding struct {
	host     *Host
	pluginID string
	tier     packagefmt.Tier
	envelope []string
	// closed marks the end of this activation. Set by Close; read by
	// every dispatch.
	closed atomic.Bool
}

// policySnapshot is one generation of the operator's answer.
type policySnapshot struct {
	grants   map[string]Grant
	profiles map[string]AuthProfile
}

// ReplacePolicy installs a new generation of the operator's answer.
//
// THE MAP WAS HANDED OVER ONCE AND NEVER UPDATED. Binding.grant() read
// it on every dispatch, which made the binding live with respect to the
// map — and the map was not live with respect to the operator, because
// config reload replaces App.cfg and told the broker nothing.
//
// The first attempt at this replaced only grants, and hung reactivation
// off a grant FINGERPRINT in plugin convergence. That was two mistakes.
// It was inert, because convergence returns early when no package
// changed, which is every configuration reload. And it contradicted the
// model: authority here is computed per invocation and never retained,
// so a grant change is not a new activation — it is the next invocation
// getting a different answer. Package and signature changes own
// reactivation; grants do not.
// IT RETURNS NOTHING, AND THAT IS THE POINT. It used to return the
// plugins whose VOICE had been withdrawn, diffed under this lock,
// because a withdrawal had to chase down audio streams the grant had
// authorised — and a diff computed outside the lock leaves windows a
// withdrawal can land in. With nothing held, there is nothing to chase:
// the next voice.observe asks grantFor and is refused. The race went
// away with the thing that raced.
func (h *Host) ReplacePolicy(grants map[string]Grant, profiles map[string]AuthProfile) {
	if h == nil {
		return
	}
	h.policyMu.Lock()
	h.policy = policySnapshot{grants: grants, profiles: profiles}
	h.policyMu.Unlock()
}

// grantFor reads one plugin's current grant under the lock.
func (h *Host) grantFor(pluginID string) (Grant, bool) {
	h.policyMu.RLock()
	defer h.policyMu.RUnlock()
	g, ok := h.policy.grants[pluginID]
	return g, ok
}

// profileFor reads one auth profile from the SAME generation as the
// grant that named it.
func (h *Host) profileFor(name string) (AuthProfile, bool) {
	h.policyMu.RLock()
	defer h.policyMu.RUnlock()
	p, ok := h.policy.profiles[name]
	return p, ok
}

// grant reads the operator's current answer, every time it is asked.
//
// This used to be a snapshot taken at Bind, eight lines under a comment
// promising the binding "holds NO evaluated authority" and re-runs the
// rings on every Dispatch. A kept grant IS kept authority, and the
// contradiction was not only on paper: a grant the operator changed did
// not reach a plugin whose package had not changed, so the binding went
// on answering from the answer it was given at activation.
//
// A lookup costs a map read and makes the invariant true. It is also
// the whole of what a withdrawal now needs: nothing holds authority
// between invocations, so removing a grant from config IS removing it,
// with no second mechanism to keep in step.
func (b *Binding) grant() Grant { g, _ := b.host.grantFor(b.pluginID); return g }

// ClearTempScope drops the temp-scoped RING4 rows for this plugin
// without ending the binding — what a FRESH activation does so it cannot
// inherit rows a crashed predecessor never cleared.
//
// This used to be Close, called on a binding about to be used, which is
// why Close could not mean "done" and therefore could not invalidate.
// One name meaning both "start clean" and "you are finished" is a name
// that cannot do either job properly.
func (b *Binding) ClearTempScope() error {
	if b == nil {
		return nil
	}
	return b.host.cfg.Store.PluginKVClearTemp(b.pluginID)
}

// Close ends the binding. Temp-scoped kv rows are cleared (the
// uncertified-tier RING4 lifetime — storage lives exactly one
// activation) and the binding stops answering. Idempotent.
//
// INVALIDATION IS WHAT MAKES A STALE HOLDER HARMLESS. Anything that
// kept this binding past its activation would otherwise go on being
// answered, because every ring it consults would still say yes: the
// plugin id is the same, the envelope is the same, the operator's grant
// is the same. Only the activation is different, and nothing else
// records that.
func (b *Binding) Close() error {
	if b == nil {
		return nil
	}
	b.closed.Store(true)
	return b.host.cfg.Store.PluginKVClearTemp(b.pluginID)
}

// PluginID reports the bound identity (telemetry; the activation log).
func (b *Binding) PluginID() string { return b.pluginID }

// --- wire shapes (BBB_V2_AUDIT §6.3 result superset; §8 error object) ---

type rpcError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *errorData `json:"data,omitempty"`
}

type errorData struct {
	ReasonCode string `json:"reasonCode"`
	DeniedAt   string `json:"denied_at,omitempty"`
}

// invokeParams is the audited invoke.call params surface the broker
// reads (rpc.c:2311-2323). Unknown members are tolerated exactly as the
// daemon tolerates them; `grant` alone is checked-and-refused (retired,
// rpc.c:2325-2328).
type invokeParams struct {
	Operation           string          `json:"operation"`
	PluginOperation     string          `json:"plugin_operation"`
	Target              json.RawMessage `json:"target"`
	Arguments           json.RawMessage `json:"arguments"`
	WorkDoneToken       json.RawMessage `json:"work_done_token"`
	Grant               json.RawMessage `json:"grant"`
	ParentRuntimeCallID string          `json:"parent_runtime_call_id"`
}

// externalReceipt is the host-authored receipt, field-for-field the
// daemon's (rpc.c:1516-1547 via BBB_V2_AUDIT §6.3: success,
// transport_outcome, protocol_status number|null, operation_outcome,
// audit_persisted, id, timestamp ISO-8601 UTC, plugin_id, plus audit
// fields). host_authored:true is what the C contract module demands
// before recording a receipt (invoke_contract.c:328-335) — a receipt
// without it is not proof.
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
	Operation        string `json:"operation"`
	PluginOperation  string `json:"plugin_operation,omitempty"`
	Target           string `json:"target"`
	Detail           string `json:"detail,omitempty"`
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

// outcome is one execution-phase verdict on its way to a result reply
// and its receipt.
type outcome struct {
	status          string // succeeded | failed | denied
	reason          string // reason code (C emits the code in all three reason spellings)
	operationResult json.RawMessage
	transportOK     bool
	protocolStatus  *int
	detail          string // receipt-only human detail (never secret material)
}

// --- dispatch ---

// Dispatch serves one guest-outgoing BBB call (the
// pluginworker.HostDispatcher contract: params in, reply bytes out;
// err reserved for internal failure, which fails the guest's call).
func (b *Binding) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	// NO GRANT MEANS NO BINDING, AND A NIL BINDING DENIES.
	//
	// Bind returns nil for a plugin the operator has not granted, and
	// the activation path checks for it — so this is not reachable
	// today. It is here because Close() already guards nil and this did
	// not, and an inconsistent nil contract on the capability boundary
	// is a crash waiting for the one caller that forgets. A security
	// boundary that panics has stopped being one.
	if b == nil {
		return errorReply(-32000, "this plugin holds no capability binding",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	// THE DENY-ALL POSTURE COMES BEFORE PARSING. A plugin the operator
	// has granted nothing gets the audited denial whatever it sent —
	// telling it instead that its parameters are malformed answers a
	// question it was never entitled to ask, and leaks that the broker
	// would have looked. This was free when Bind returned nil for an
	// ungranted plugin; now that a binding always exists, it is a check.
	if b.grant().isEmpty() {
		return errorReply(-32000,
			fmt.Sprintf("no capability is granted to plugin %s; invoke-call denied", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if b.closed.Load() {
		// The activation this binding belonged to is over. A later
		// activation of the same plugin gets its own binding; this one
		// answers nothing, which is what makes anything still holding it
		// harmless rather than merely unlikely.
		return errorReply(-32000, "this plugin's activation has ended",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	reply, err := b.dispatch(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if len(reply) > bbb.MaxControlFrameBytes {
		// Unreachable by construction (resultReply pre-clamps payloads
		// against the frame budget) — but a broker bug must fail the
		// call honestly, never hand the worker a frame that poisons the
		// module at its own wall.
		return nil, fmt.Errorf("broker: produced a %d-byte reply over the %d-byte plugin frame ceiling", len(reply), bbb.MaxControlFrameBytes)
	}
	return reply, nil
}

func (b *Binding) dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	if method != methodInvokeCall {
		// No session/observe/heartbeat surface exists in step 4; those
		// methods join with later build-order steps. Same audited
		// denial the deny-all stub speaks.
		return errorReply(-32000, fmt.Sprintf("no %s surface in the step-4 broker; denied", method),
			&errorData{ReasonCode: reasonPolicyDeny})
	}

	var p invokeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorReply(-32602, "params must be a JSON object", nil)
	}
	if p.Operation == "" {
		// Verbatim daemon message (rpc.c:2322-2323).
		return errorReply(-32602, "operation (string) required", nil)
	}
	if len(p.Grant) > 0 {
		// Verbatim daemon message (rpc.c:2325-2328).
		return errorReply(-32602, "grant is retired; invoke.call evaluates capability per request", nil)
	}
	if len(p.WorkDoneToken) > 0 {
		// Verbatim daemon message (rpc.c:2063-2068): nothing negotiated
		// rpc.cancel in this harness, so the token cannot be honored.
		return errorReply(-32602, "work_done_token requires negotiated rpc.cancel capability", nil)
	}

	switch p.Operation {
	case opKVPut, opKVGet, opKVDelete:
		return b.dispatchKV(ctx, p)
	case opHTTPGet:
		return b.dispatchHTTPGet(ctx, p)
	case opVoiceObserve:
		return b.dispatchVoiceObserve(ctx, p)
	default:
		// Outside the broker's operation registry — the audited
		// capability-evaluation denial (rpc_capability.c:97).
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

// resultReply authors the receipt for one execution-phase outcome —
// store record first, injected external_receipt mirroring it — and
// marshals the audited result superset. The receipt says what the
// BROKER did, never what the plugin claims (A3).
func (b *Binding) resultReply(operation, pluginOperation, target string, o outcome) ([]byte, error) {
	// Frame-budget pre-clamp: a payload whose JSON form cannot fit the
	// audited 1 MiB plugin-side ceiling (with envelope + receipt
	// headroom) collapses HERE, before the receipt is authored — so the
	// one receipt describes the outcome the guest actually receives,
	// and the worker's frame wall is never the thing that fires. JSON
	// escaping can inflate a binary body several-fold, which is why the
	// body-byte ceiling alone is not sufficient.
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
		// Minted id, the daemon's fallback shape (rpc.c:1501-1514
		// invoke-<ns>): no request id exists on the import path and no
		// work token is negotiable in step 4.
		ID:              fmt.Sprintf("invoke-%d-%d", now.UnixNano(), b.host.counter.Add(1)),
		Timestamp:       now.Format(time.RFC3339Nano), // the store's time discipline
		PluginID:        b.pluginID,
		Operation:       operation,
		PluginOperation: pluginOperation,
		Target:          target,
		Detail:          o.detail,
	}
	recRaw, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("broker: encode receipt: %w", err)
	}
	if serr := b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, operation, target, rec.Success, recRaw); serr != nil {
		// The record did not land: the guest-visible receipt must not
		// claim it did (C parity: write_landed/audit_persisted are
		// stamped by the landing, invoke_contract.c mark_write_landed).
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
		// The daemon emits the same code string in all three reason
		// spellings (rpc.c:2195-2214 via BBB_V2_AUDIT §6.3).
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

// --- RING4 kv ---

// dispatchKV serves the kv triple. The lattice for kv is two rings —
// operator grant ∩ tier ceiling — because no manifest capability
// vocabulary for storage exists in the shared SDK contract (recorded
// finding; when the contract grows one, the envelope ring joins here).
// The namespace is the BINDING's plugin id — a plugin-supplied name
// never selects it.
// VoiceObserver receives one heard utterance. The host implements it;
// the broker only decides whether the plugin may call.
//
// THE SPLIT IS THE SECURITY STORY. The plugin says what it heard and
// what it thinks it heard it from; the host decides what that becomes.
// A plugin cannot write a conversation turn, cannot choose a role, and
// has nowhere to put an is_operator — because operator authority is a
// fact about a CHANNEL and a microphone is not one.
type VoiceObserver interface {
	Observe(VoiceObservation) error
}

// VoiceObservation is one heard utterance as the host receives it.
//
// PROVENANCE IS SET HERE, NOT SENT. PluginID comes from the binding —
// the id in the signed manifest whose key opened this session — and
// there is no argument a plugin can send to influence it. Provenance a
// plugin could write is not provenance, and the whole point of naming
// which plugin proposed an utterance is to be able to disbelieve one.
type VoiceObservation struct {
	// PluginID is which plugin heard it. Broker-set.
	PluginID string
	// Text is what the plugin says was said. Untrusted.
	Text string
	// Speaker is the plugin's LABEL for the voice — session-local
	// ("speaker-1") or an enrolled name — and it is a claim, never a
	// finding of identity. Untrusted.
	Speaker string
	// SpeakerScore is how sure the plugin's model is, 0..1.
	//
	// EVIDENCE, WHICH IS NOT AUTHORITY, and it took two goes to get this
	// right. The first contract refused it alongside is_operator, which
	// was a good rule applied to the wrong noun — a score is a
	// MEASUREMENT the identity may weigh, where is_operator is a
	// decision about authority a microphone is not entitled to make. The
	// second carried it on the wire and then dropped it here, which is
	// the same outcome by omission: a plugin reports how sure it is and
	// nothing that could act on it ever sees the number.
	//
	// Zero means "said nothing", which is what a plugin that does no
	// speaker recognition sends.
	SpeakerScore float64
}

// dispatchVoiceObserve admits one utterance, or refuses.
//
// Two rings, like kv: the signed envelope must declare the capability,
// and the operator must grant it. No tier rule beyond those — a voice
// plugin's trust tier decides what code may run, and this decides
// whether words may enter, which are different questions.
func (b *Binding) voiceRings(capability string) ([]byte, bool) {
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
	if !b.grant().Voice {
		r, _ := errorReply(-32000,
			fmt.Sprintf("voice is not granted to plugin %s; the operator grants it in plugins.grants", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
		return r, false
	}
	return nil, true
}

func (b *Binding) dispatchVoiceObserve(_ context.Context, p invokeParams) ([]byte, error) {
	if r, ok := b.voiceRings(capVoiceObserve); !ok {
		return r, nil
	}
	if b.host.cfg.Voice == nil {
		return errorReply(-32000,
			"this host accepts no voice observations",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	// SAFE REFUSES THE MICROPHONE. This check used to live on
	// stream.attach, where it read as a teardown condition with a
	// reopen race behind it: close every stream on entry, and hope
	// nothing reattaches. Per-invocation is the shape that cannot race
	// — every observation asks, so none lands while SAFE holds.
	// Operator presence and a T3 signature create no exception: an
	// identity that cannot trust its own record must not be quietly
	// recording a room into it.
	if b.host.cfg.InSAFE != nil && b.host.cfg.InSAFE() {
		return errorReply(-32000,
			"this identity is in SAFE; nothing heard is recorded while it holds",
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	var args struct {
		Text    string `json:"text"`
		Speaker string `json:"speaker"`
		// SpeakerScore is EVIDENCE, WHICH IS NOT AUTHORITY. It sat on
		// the stream carrier and not on this one, so the only path that
		// could report how sure a model was is the path that no longer
		// exists — and evidence the identity never sees is evidence that
		// does not exist. A score is a MEASUREMENT the identity may
		// weigh; is_operator is a decision about authority a microphone
		// is not entitled to make. That is the whole difference, and it
		// is why one is carried here and the other is refused by name.
		SpeakerScore float64 `json:"speaker_score"`
	}
	if len(p.Arguments) > 0 {
		// STRICT, SO THAT ASKING IS AN ERROR RATHER THAN A NO-OP.
		//
		// The design's central rule is that a voice plugin has nowhere to
		// put a decision — no is_operator, no trust tier, no confidence
		// that means anything here. A tolerant decoder enforces that by
		// SILENCE: a plugin sends is_operator, the field is dropped, and
		// both sides believe something different about what was agreed.
		// The plugin author learns nothing, and the next reader of their
		// code sees a field that looks honoured.
		//
		// Refusing by name turns the rule into feedback. It is also the
		// cheapest possible detector for a plugin built against the
		// superseded blueprint that DID have is_operator.
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
	// BOUNDED AT THE BOUNDARY, AND REFUSED RATHER THAN TRIMMED. The
	// frame cap admits a megabyte, and every byte of this reaches the
	// identity's conversation. Trimming would silently change what a
	// speaker is recorded as having said, which is worse than refusing:
	// a well-formed plugin never approaches these, so exceeding one is
	// a defect or an attempt, and either deserves an answer.
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
	// Zero means "said nothing", which is what a plugin that does no
	// speaker recognition sends. Anything outside 0..1 is not a
	// confidence, and a number the identity cannot interpret is worse
	// than no number at all.
	if args.SpeakerScore < 0 || args.SpeakerScore > 1 {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("voice.observe speaker_score %v is outside 0..1 — a score is a confidence, and one that is not is not evidence",
				args.SpeakerScore)})
	}
	if err := b.host.cfg.Voice.Observe(VoiceObservation{
		PluginID:     b.pluginID, // from the binding; never from arguments
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

func (b *Binding) dispatchKV(_ context.Context, p invokeParams) ([]byte, error) {
	// Ring order mirrors net: envelope -> operator -> tier. The
	// envelope ring joined 2026-08-19 (operator-approved) once ring4.kv
	// existed to declare — before that, kv ran two rings by necessity
	// (D4-F1). The manifest asks; it never grants.
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
	if !b.grant().KV {
		return errorReply(-32000,
			fmt.Sprintf("kv is not granted to plugin %s; the operator grants storage in plugins.grants", b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
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

	// temp scope = the tier ceiling intersecting the grant: an
	// uncertified tier holds storage for exactly one activation
	// (PLUGIN_FRAMEWORK §3: T0 = temp RING4; T1+ = persistent) —
	// derived from the shared contract data, not restated here.
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

	default: // opKVDelete
		deleted, err := st.PluginKVDelete(b.pluginID, key)
		if err != nil {
			return nil, fmt.Errorf("broker: kv.delete: %w", err)
		}
		or, _ := json.Marshal(map[string]interface{}{"deleted": deleted, "key": key})
		return b.resultReply(p.Operation, p.PluginOperation, key, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})
	}
}

// --- net.outbound (http.get) ---

// hostScope is one parsed host_port scope: exact host, exact port or
// any (the C wildcard-port text "*", sev_capability.h:72-74). No host
// wildcard: a "*" host would be a grant of everywhere, which this MVP
// refuses to represent (fail closed; recorded).
type hostScope struct {
	host    string
	port    int // 0 = any
	anyPort bool
}

func parseHostScope(s string) (hostScope, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return hostScope{}, false
	}
	host, portText := s, ""
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s, "]") { // no IPv6-literal grammar in scopes (C stores plain hosts)
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

// netEnvelopeScopes extracts the net.outbound declarations from the
// signed envelope list. Unparseable entries are SKIPPED, never
// widened: a malformed declaration grants nothing.
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

// dispatchHTTPGet is the full three-ring evaluation plus the bounded
// transport. Ring order is deliberate: the tier ceiling (outermost —
// what the trust contract lets this class of plugin hold at all), the
// signed envelope (what the publisher asked for), the operator grant
// (what this installation approved). The egress guard is NOT a ring —
// it is the substrate's own floor and wins over every grant.
func (b *Binding) dispatchHTTPGet(ctx context.Context, p invokeParams) ([]byte, error) {
	// Ring 1 — tier ceiling, from the shared contract data: network is
	// an external effect; only publisher-proven tiers hold it
	// (PLUGIN_FRAMEWORK §3: T0 = pure compute + temp RING4 ONLY —
	// denied at T0 regardless of grants).
	if !b.tier.PublisherProven() {
		return errorReply(-32000,
			fmt.Sprintf("tier %s holds no %s ceiling (the trust contract backs no external effects below a publisher-proven tier)", b.tier, capNetOutbound),
			&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
	}

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
			status: statusDenied, reason: reasonTargetInvalid, detail: "http.get requires target.url (rpc_capability.c:331-338)"})
	}
	u, err := url.Parse(target.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL"})
	}
	// A port that is present but not a valid 1-65535 integer must fail
	// closed, never silently normalize to 0 (F1 rider, external review 2026-08-17): a
	// zero port would satisfy any anyPort scope in ring 2/3 matching.
	if portText := u.Port(); portText != "" {
		n, serr := strconv.Atoi(portText)
		if serr != nil || n < 1 || n > 65535 {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL port"})
		}
	}
	host, port := urlHostPort(u)

	// Ring 2 — the signed manifest envelope (the publisher's own
	// declared maximum; a scope outside it was never even requested).
	if !anyScopeMatches(netEnvelopeScopes(b.envelope), host, port) {
		return errorReply(-32000,
			fmt.Sprintf("%s:%s:%d is outside the signed capability envelope", capNetOutbound, host, port),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}

	// Ring 3 — the operator grant (widening is the operator's act;
	// their silence is the policy).
	var grantScopes []hostScope
	for _, g := range b.grant().Hosts {
		if sc, ok := parseHostScope(g); ok {
			grantScopes = append(grantScopes, sc)
		}
	}
	if !anyScopeMatches(grantScopes, host, port) {
		return errorReply(-32000,
			fmt.Sprintf("no operator grant covers %s:%s:%d (plugins.grants.%s.hosts)", capNetOutbound, host, port, b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	// Arguments — strict per the daemon's own registry discipline:
	// unknown argument names fail closed (NET_UNKNOWN_ARGUMENT,
	// sev_operations.h:135) rather than silently no-op.
	timeout := DefaultHTTPTimeout
	authProfile := ""
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
		for name, raw := range args {
			switch name {
			case "timeout_ms": // SEV_OPERATION_HTTP_ARG_TIMEOUT_MS
				var ms int
				if err := json.Unmarshal(raw, &ms); err != nil || ms <= 0 {
					return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
						status: statusDenied, reason: reasonArgumentInvalid, detail: "timeout_ms must be a positive integer"})
				}
				if time.Duration(ms)*time.Millisecond > MaxHTTPTimeout {
					return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
						status: statusDenied, reason: reasonNetTimeoutLimit,
						detail: fmt.Sprintf("timeout_ms %d exceeds the %d ms ceiling", ms, MaxHTTPTimeout/time.Millisecond)})
				}
				timeout = time.Duration(ms) * time.Millisecond
			case "auth_profile": // SEV_OPERATION_HTTP_ARG_AUTH_PROFILE
				if err := json.Unmarshal(raw, &authProfile); err != nil || authProfile == "" {
					return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
						status: statusDenied, reason: reasonAuthInvalid, detail: "auth_profile must be a non-empty string"})
				}
			default:
				return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
					status: statusDenied, reason: reasonNetUnknownArgument,
					detail: fmt.Sprintf("argument %q is not supported by this broker (MVP: timeout_ms, auth_profile)", name)})
			}
		}
	}

	// Credential resolution — entirely broker-side. The secret value
	// exists only inside this call frame and the outgoing header; it is
	// never echoed into the reply, the receipt, the store, or any log
	// (this package logs nothing, by rule).
	authHeader := ""
	if authProfile != "" {
		if o := b.resolveAuthProfile(authProfile, u, host, port, &authHeader); o != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, *o)
		}
	}

	// The substrate egress floor: the SAME guard web_fetch enforces,
	// on the first hop here and on every redirect hop inside the shared
	// client. The guard outranks the grant — a granted loopback target
	// stays blocked (A5: no grant may expose substrate-adjacent
	// surfaces).
	guard := b.host.cfg.Guard
	if guard == nil {
		guard = tools.FetchGuard
	}
	if gerr := guard(target.URL); gerr != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonPolicyDeny,
			detail: fmt.Sprintf("egress guard: %v", gerr)})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL"})
	}
	req.Header.Set("User-Agent", "AII-OS/1.0")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	client := tools.GuardedClient(timeout, guard, b.host.cfg.Transport)
	resp, err := client.Do(req)
	if err != nil {
		// A redirect hop the guard refused arrives as a client error
		// carrying the guard's text — that is a policy denial, not a
		// remote failure.
		reason, status := reasonNetRemoteFailed, statusFailed
		if strings.Contains(err.Error(), "redirect blocked") {
			reason, status = reasonPolicyDeny, statusDenied
		}
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: status, reason: reason, detail: fmt.Sprintf("transport: %v", err)})
	}
	defer resp.Body.Close()

	maxBytes := b.host.cfg.maxResponseBytes()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	httpStatus := resp.StatusCode
	if rerr != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			detail: fmt.Sprintf("read: %v", rerr)})
	}
	if len(body) > maxBytes {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusFailed, reason: reasonNetResponseTooBig, transportOK: true, protocolStatus: &httpStatus,
			detail: fmt.Sprintf("response exceeds the %d-byte ceiling", maxBytes)})
	}

	// The fetch REALLY happened: report it into the registry's
	// observation seam so an identity citation of this URL earns
	// provenance exactly like a builtin web_fetch (H3).
	if b.host.cfg.ObserveFetch != nil {
		b.host.cfg.ObserveFetch(target.URL)
	}

	// operation_result, the daemon's shape (rpc.c:2092-2162):
	// http_status always; content_type/location when present; body
	// parsed-JSON-else-string-else-null. The legacy top-level `body`
	// duplicate is deliberately not emitted (recorded divergence — both
	// audited SDK clients read operation_result first).
	or := map[string]interface{}{"http_status": httpStatus}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		or["content_type"] = ct
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		or["location"] = loc
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

	// 2xx/3xx succeed; 4xx/5xx are performed-but-failed remote outcomes
	// (NET_REMOTE_OUTCOME_FAILED) with the evidence attached — chosen
	// classification, recorded (the audit does not pin the C boundary).
	if httpStatus >= 400 {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			operationResult: orRaw})
	}
	return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
		status: statusSucceeded, transportOK: true, protocolStatus: &httpStatus, operationResult: orRaw})
}

// resolveAuthProfile runs the C daemon's auth-profile ladder
// (operations.c sev_operation_context_prepare_http_auth_profile and the
// execution checks at :845-885), in its order, with its reason codes.
// On success it writes the Authorization header value; a non-nil return
// is the denial outcome. The secret value never leaves this function
// except inside authHeader.
func (b *Binding) resolveAuthProfile(name string, u *url.URL, host string, port int, authHeader *string) *outcome {
	// The C floor: auth_profile requires T2 or T3 trust (operations.c:
	// 851-855) — review-proven per the shared contract, not a Go
	// invention. Same code as un-admitted (C uses NOT_ADMITTED for
	// both).
	if !b.tier.ReviewProven() {
		return &outcome{status: statusDenied, reason: reasonAuthNotAdmitted,
			detail: fmt.Sprintf("auth_profile requires a review-proven tier; %s is not", b.tier)}
	}
	admitted := false
	for _, h := range b.grant().CredentialHandles {
		if h == name {
			admitted = true
			break
		}
	}
	if !admitted {
		return &outcome{status: statusDenied, reason: reasonAuthNotAdmitted,
			detail: "auth_profile was not admitted for this plugin (plugins.grants credential_handles)"}
	}
	// HTTPS only — a credential never rides cleartext (operations.c:
	// 596-599 NET_AUTH_PROFILE_REQUIRES_HTTPS).
	if u.Scheme != "https" {
		return &outcome{status: statusDenied, reason: reasonAuthRequiresHTTPS,
			detail: "auth_profile requires an https target"}
	}
	profile, ok := b.host.profileFor(name)
	if !ok {
		return &outcome{status: statusDenied, reason: reasonAuthUnavailable,
			detail: "no such auth profile in plugins.auth_profiles"}
	}
	if profile.Host == "" || profile.Port == 0 {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: "auth profile must pin host and port"}
	}
	// The profile pins the ONLY destination its credential may ride to
	// (operations.c:634-641, :856-861 NET_AUTH_PROFILE_SCOPE_MISMATCH).
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
	// Header sanity, the C shape (operations.c:870-877): bounded by the
	// header-value budget including the Bearer prefix
	// (SEV_OPERATION_HTTP_HEADER_VALUE_BUFSZ 4096) and free of bytes
	// that could split the header. The detail string NEVER carries the
	// value.
	if len(secret)+len(bearerPrefix) > 4096 || strings.ContainsAny(secret, "\x00\r\n") {
		return &outcome{status: statusDenied, reason: reasonNetHeaderInvalid,
			detail: "auth_profile secret is not a valid Bearer credential"}
	}
	*authHeader = bearerPrefix + secret
	return nil
}
