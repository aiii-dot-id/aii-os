package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/retryafter"
)

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
		p, err := strconv.Atoi(portText)
		if err != nil || p < 1 || p > 65535 {
			return hostScope{}, false
		}
		sc.port = p
	}
	return sc, true
}

func (s hostScope) matches(host string, port int) bool {
	return s.host == strings.ToLower(host) && (s.anyPort || s.port == port)
}

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
			status: statusDenied, reason: reasonTargetInvalid, detail: p.Operation + " requires target.url", method: method})
	}
	u, err := url.Parse(target.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonTargetInvalid, detail: "malformed URL"})
	}

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
	localScopes, _ := firewall.LocalScopes(grant.Local)

	localCapable := b.envelopeHas(capNetLocal)
	local := localCapable && localTarget(host, port, localScopes)
	if !localCapable {
		localScopes = nil
	}

	opScope := b.scope.Load()
	if denied, r, err := b.httpRings(p.Operation, host, port, local, grantScopes); denied {
		return r, err
	}

	call, refused := b.readHTTPCall(p, method)
	if refused != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, *refused)
	}
	timeout, authProfile := call.timeout, call.authProfile

	var auth resolvedAuth
	if authProfile != "" {
		if o := b.resolveAuthProfile(ctx, pol, authProfile, u, target.URL, host, port, &auth, local, grant.PlaintextCredentials); o != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, *o)
		}
	} else if strings.Contains(target.URL, CredentialPlaceholder) {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
			status: statusDenied, reason: reasonArgumentInvalid, method: method,
			detail: fmt.Sprintf("the URL names %s but no auth_profile rides this call", CredentialPlaceholder)})
	}

	authHeader := auth.header
	dialURL := target.URL
	if auth.pathSecret != "" {
		if call.stream {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonAuthInvalid, method: method,
				detail: "a credential in the URL path rides non-streaming calls only"})
		}
		dialURL = strings.Replace(target.URL, CredentialPlaceholder, url.PathEscape(auth.pathSecret), 1)
	}

	baseGuard := b.host.cfg.Guard
	if baseGuard == nil {
		baseGuard = firewall.FetchGuard
	}

	lg := firewall.GuardAdmitting(baseGuard, localScopes, b.host.cfg.OwnListener)

	hops := hopPolicy{
		guard: lg.Guard, envelope: netEnvelopeScopes(b.envelope), grant: grantScopes,
		localCapable: localCapable, localScopes: localScopes,
		oauth: auth.source != nil, oauthHosts: auth.hosts, profile: auth.profile,
		host: host, port: port, local: local, plaintextOK: grant.PlaintextCredentials,
	}
	if opScope != nil && opScope.Declared {
		hops.declared = true
		hops.declaredScopes = netEnvelopeScopes(opScope.Capabilities)
		hops.declaredLocal = slices.Contains(opScope.Capabilities, capNetLocal)
	}
	hopGuard := hops.admit
	if gerr := hopGuard(ctx, dialURL); gerr != nil {
		detail := auth.echo.scrub(fmt.Sprintf("egress guard: %v", gerr))
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
	req, err := http.NewRequestWithContext(firewall.WithPins(ctx, lg), method, dialURL, bodyReader)
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
	for name, value := range auth.headers {
		req.Header.Set(name, value)
	}

	client := firewall.GuardedClient(timeout, hopGuard, b.host.cfg.Transport)
	if !call.followRedirects {

		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	attempts := 1
	if call.idempotent {
		attempts = 2
	}

	var hold *streamHold
	if call.stream {
		var taken int
		if hold, taken = b.holdStream(); hold == nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, outcome{
				status: statusDenied, reason: reasonNetStreamLimit, method: method,
				detail: fmt.Sprintf("%d streams are open or being opened; the ceiling is %d — close one and ask again. Nothing was sent", taken, MaxOpenStreams)})
		}
		defer hold.release()
	}
	x := &httpExchange{client: client, req: req, body: call.body, attempts: attempts}
	x.send()

	if x.err == nil && x.resp.StatusCode == http.StatusUnauthorized && auth.source != nil {
		if refused := b.renewRejectedToken(ctx, x, &auth, method); refused != nil {
			return b.resultReply(p.Operation, p.PluginOperation, target.URL, *refused)
		}
	}
	if x.err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target.URL, x.failure(method, auth.echo))
	}
	return b.httpAnswer(p, target.URL, method, hold, x.resp, auth.echo)
}

type streamHold struct {
	b    *Binding
	done bool
}

func (b *Binding) holdStream() (*streamHold, int) {
	b.streamsMu.Lock()
	defer b.streamsMu.Unlock()
	if taken := len(b.streams) + b.streamsHeld; taken >= MaxOpenStreams {
		return nil, taken
	}
	b.streamsHeld++
	return &streamHold{b: b}, 0
}

func (h *streamHold) release() {
	h.b.streamsMu.Lock()
	defer h.b.streamsMu.Unlock()
	if !h.done {
		h.done = true
		h.b.streamsHeld--
	}
}

type httpExchange struct {
	client   *http.Client
	req      *http.Request
	body     []byte
	attempts int
	resp     *http.Response
	err      error

	wrote, lost bool
}

func (x *httpExchange) send() {
	for attempt := 1; attempt <= x.attempts; attempt++ {
		x.wrote = false
		trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { x.wrote = true }}

		attemptReq := x.req.Clone(httptrace.WithClientTrace(x.req.Context(), trace))
		if x.body != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(x.body))
		}
		x.resp, x.err = x.client.Do(attemptReq)
		if x.err == nil || errors.Is(x.err, firewall.ErrEgressBlocked) || attempt == x.attempts {
			break
		}
		x.lost = x.lost || x.wrote
	}
}

func (b *Binding) renewRejectedToken(ctx context.Context, x *httpExchange, auth *resolvedAuth, method string) *outcome {
	x.resp.Body.Close()
	cred, rerr := auth.source.ForceRefresh(ctx, auth.gen)
	if rerr != nil || cred.Refreshed {
		b.receiptAuthRefresh(auth.profile, rerr)
	}
	var refused *outcome
	switch {
	case rerr != nil:
		refused = oauthDenial(auth.profile, rerr)
	case len(cred.Token)+len(bearerPrefix) > 4096 || strings.ContainsAny(cred.Token, "\x00\r\n"):
		refused = &outcome{status: statusDenied, reason: reasonNetHeaderInvalid, detail: "invalid refreshed credential"}
	}
	if refused != nil && x.lost {

		refused = &outcome{status: statusFailed, reason: reasonNetEffectUnknown, method: method, effect: EffectUnknown,
			detail: refused.detail + " — after an earlier attempt of this call was written and answered nothing"}
	}
	if refused != nil {
		return refused
	}
	x.req.Header.Set("Authorization", bearerPrefix+cred.Token)
	auth.echo.add(cred.Token)
	for name := range auth.headers {
		x.req.Header.Del(name)
	}
	for name, value := range cred.Headers {
		x.req.Header.Set(name, value)
	}
	x.send()
	return nil
}

func (x *httpExchange) failure(method string, echo credentialEcho) outcome {

	reason, status, effect := reasonNetRemoteFailed, statusFailed, EffectNotPerformed
	var protocolStatus *int
	switch {
	case x.wrote && errors.Is(x.err, firewall.ErrEgressBlocked):
		effect = EffectPerformed
		if x.resp != nil {
			code := x.resp.StatusCode
			protocolStatus = &code
		}
	case x.wrote || x.lost:
		reason, effect = reasonNetEffectUnknown, EffectUnknown
	case errors.Is(x.err, firewall.ErrEgressBlocked):
		reason, status = reasonPolicyDeny, statusDenied
	}
	return outcome{
		status: status, reason: reason, detail: echo.scrub(fmt.Sprintf("transport: %v", x.err)), method: method, effect: effect,
		transportOK: protocolStatus != nil, protocolStatus: protocolStatus}
}

func (b *Binding) httpRings(op, host string, port int, local bool, grantScopes []hostScope) (denied bool, reply []byte, err error) {
	if local {

		if r, denied := b.scopeDenies(op, ""); denied {
			return true, r, nil
		}
		if r, denied := b.scopeDeniesLocal(); denied {
			return true, r, nil
		}
	} else {

		if !b.tier.PublisherProven() {
			r, err := errorReply(rpcForbidden,
				fmt.Sprintf("tier %s holds no %s ceiling (the trust contract backs no external effects below a publisher-proven tier)", b.tier, capNetOutbound),
				&errorData{ReasonCode: reasonTierDenied, DeniedAt: deniedAtCapEval})
			return true, r, err
		}

		if !anyScopeMatches(netEnvelopeScopes(b.envelope), host, port) {
			r, err := errorReply(rpcForbidden,
				fmt.Sprintf("%s:%s:%d is outside the signed capability envelope", capNetOutbound, host, port),
				&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
			return true, r, err
		}

		if !anyScopeMatches(grantScopes, host, port) {
			r, err := errorReply(rpcForbidden,
				fmt.Sprintf("no operator grant covers %s:%s:%d (plugins.grants.%s.hosts)", capNetOutbound, host, port, b.pluginID),
				&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
			return true, r, err
		}

		if r, denied := b.scopeDenies(op, ""); denied {
			return true, r, nil
		}
		if r, denied := b.scopeDeniesNet(host, port); denied {
			return true, r, nil
		}
	}
	return false, nil, nil
}

func (b *Binding) readHTTPCall(p invokeParams, method string) (httpCall, *outcome) {
	call := httpCall{method: method, timeout: DefaultHTTPTimeout, followRedirects: method == http.MethodGet, idempotent: method == http.MethodGet}
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return httpCall{}, &outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object", method: method}
		}
		deny := func(reason, detail string) (httpCall, *outcome) {
			return httpCall{}, &outcome{status: statusDenied, reason: reason, detail: detail, method: method}
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
		return httpCall{}, &outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "body requires content_type", method: method}
	}
	if call.body == nil && call.contentType != "" {
		return httpCall{}, &outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "content_type belongs to a body", method: method}
	}
	return call, nil
}

func (b *Binding) httpAnswer(p invokeParams, target, method string, hold *streamHold, resp *http.Response, echo credentialEcho) ([]byte, error) {
	if hold != nil && resp.StatusCode < 400 {

		return b.openStream(p, target, method, hold, resp, echo)
	}
	defer resp.Body.Close()

	maxBytes := b.host.cfg.maxResponseBytes()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	httpStatus := resp.StatusCode
	if rerr != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			detail: echo.scrub(fmt.Sprintf("read: %v", rerr)), method: method, effect: EffectPerformed})
	}
	if len(body) > maxBytes {
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
			status: statusFailed, reason: reasonNetResponseTooBig, transportOK: true, protocolStatus: &httpStatus,
			detail: fmt.Sprintf("response exceeds the %d-byte ceiling", maxBytes), method: method, effect: EffectPerformed})
	}

	if method == http.MethodGet && b.host.cfg.ObserveFetch != nil {
		b.host.cfg.ObserveFetch(target)
	}

	or := map[string]any{"http_status": httpStatus}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		or["content_type"] = echo.scrub(ct)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		or["location"] = echo.scrub(loc)
	}

	var hintDetail string
	if wait, ok := retryafter.Parse(resp.Header.Get("Retry-After"), time.Now()); ok {
		or["retry_after_s"] = int64(wait / time.Second)
	} else if httpStatus == http.StatusTooManyRequests || httpStatus == http.StatusServiceUnavailable {
		hintDetail = retryHintUnavailable
	}
	body = echo.body(body)
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

	if httpStatus >= 400 {
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
			status: statusFailed, reason: reasonNetRemoteFailed, transportOK: true, protocolStatus: &httpStatus,
			operationResult: orRaw, method: method, effect: EffectPerformed, detail: hintDetail})
	}
	return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
		status: statusSucceeded, transportOK: true, protocolStatus: &httpStatus, operationResult: orRaw, method: method, effect: EffectPerformed})
}

type hopPolicy struct {
	guard           func(context.Context, string) error
	envelope, grant []hostScope
	localCapable    bool
	localScopes     []firewall.LocalScope

	oauth      bool
	oauthHosts []string
	profile    string

	host        string
	port        int
	local       bool
	plaintextOK bool

	declared       bool
	declaredScopes []hostScope
	declaredLocal  bool
}

func (h hopPolicy) admit(ctx context.Context, rawURL string) error {
	if err := h.guard(ctx, rawURL); err != nil {
		return err
	}
	hu, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: unparseable redirect target", firewall.ErrEgressBlocked)
	}
	hh, hp := urlHostPort(hu)
	if h.oauth {
		pinned := false
		for _, allowed := range h.oauthHosts {
			if strings.EqualFold(allowed, fmt.Sprintf("%s:%d", hh, hp)) {
				pinned = true
				break
			}
		}
		if hu.Scheme != "https" || !pinned {
			return fmt.Errorf("%w: redirect is outside the OAuth profile hosts", firewall.ErrEgressBlocked)
		}
	} else if h.profile != "" {

		plaintext := hu.Scheme == "http" && h.local && h.plaintextOK
		if !strings.EqualFold(hh, h.host) || hp != h.port || (hu.Scheme != "https" && !plaintext) {
			return fmt.Errorf("%w: redirect violates the auth profile host:port or HTTPS requirement", firewall.ErrEgressBlocked)
		}
	}
	if h.localCapable && localTarget(hh, hp, h.localScopes) {

		if h.declared && !h.declaredLocal {
			return fmt.Errorf("%w: %s:%d is on the local network, and the operation did not declare %s among its capabilities", firewall.ErrEgressBlocked, hh, hp, capNetLocal)
		}
		return nil
	}
	if !anyScopeMatches(h.envelope, hh, hp) || !anyScopeMatches(h.grant, hh, hp) {
		return fmt.Errorf("%w: %s:%d is outside the plugin's granted hosts", firewall.ErrEgressBlocked, hh, hp)
	}
	if h.declared && !anyScopeMatches(h.declaredScopes, hh, hp) {
		return fmt.Errorf("%w: %s:%d is outside the operation's declared capabilities", firewall.ErrEgressBlocked, hh, hp)
	}
	return nil
}

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
	echo            credentialEcho
	held            []byte
}

func (s *httpStream) pass(chunk []byte, last bool) []byte {
	if len(s.echo) == 0 {
		return chunk
	}
	data := []byte(s.echo.scrub(string(s.held) + string(chunk)))
	keep := 0
	if !last {
		keep = s.echo.partial(data)
	}
	s.held = data[len(data)-keep:]
	return data[:len(data)-keep]
}

func (b *Binding) openStream(p invokeParams, target, method string, hold *streamHold, resp *http.Response, echo credentialEcho) ([]byte, error) {
	b.streamsMu.Lock()
	if !hold.done {
		hold.done = true
		b.streamsHeld--
	}
	b.streamSeq++
	s := &httpStream{id: fmt.Sprintf("s%d", b.streamSeq), operation: p.Operation, pluginOperation: p.PluginOperation, target: target, method: method, resp: resp, cap: b.host.cfg.maxStreamBytes(), echo: echo}
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
	or := map[string]any{"stream_id": s.id, "http_status": resp.StatusCode, "chunk_max_bytes": MaxStreamChunkBytes, "stream_max_bytes": s.cap}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		or["content_type"] = echo.scrub(ct)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		or["location"] = echo.scrub(loc)
	}
	orRaw, err := json.Marshal(or)
	if err != nil {
		return nil, fmt.Errorf("broker: encode stream head: %w", err)
	}

	return json.Marshal(successResult{Success: true, OK: true, Status: statusSucceeded, OperationResult: orRaw})
}

func (b *Binding) takeStream(id string, remove bool) *httpStream {
	b.streamsMu.Lock()
	defer b.streamsMu.Unlock()
	s := b.streams[id]
	if s != nil && remove {
		delete(b.streams, id)
	}
	return s
}

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
		return errorReply(rpcForbidden, fmt.Sprintf("no open stream %s in this activation", target.StreamID),
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
		data := s.pass(buf[:n], true)
		or, _ := json.Marshal(map[string]any{"stream_id": s.id, "seq": s.seq, "data_b64": base64.StdEncoding.EncodeToString(data), "bytes": len(data), "done": true, "total_bytes": s.total})
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
		return terminal(statusFailed, reasonNetRemoteFailed, s.echo.scrub(fmt.Sprintf("stream %s ended after %d bytes: %v", s.id, s.total, rerr)))
	}
	data := s.pass(buf[:n], false)
	or, err := json.Marshal(map[string]any{"stream_id": s.id, "seq": s.seq, "data_b64": base64.StdEncoding.EncodeToString(data), "bytes": len(data), "done": false, "total_bytes": s.total})
	if err != nil {
		return nil, fmt.Errorf("broker: encode chunk: %w", err)
	}
	return json.Marshal(successResult{Success: true, OK: true, Status: statusSucceeded, OperationResult: or})
}

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
		return errorReply(rpcForbidden, fmt.Sprintf("no open stream %s in this activation", target.StreamID),
			&errorData{ReasonCode: reasonTargetInvalid, DeniedAt: deniedAtCapEval})
	}
	s.resp.Body.Close()
	httpStatus := s.resp.StatusCode
	or, _ := json.Marshal(map[string]any{"stream_id": s.id, "closed": true, "total_bytes": s.total, "chunks": s.seq})
	return b.resultReply(s.operation, s.pluginOperation, s.target, outcome{
		status: statusSucceeded, transportOK: true, protocolStatus: &httpStatus, method: s.method, effect: EffectPerformed,
		operationResult: or, detail: fmt.Sprintf("stream %s closed by the plugin after %d bytes in %d chunks", s.id, s.total, s.seq)})
}
