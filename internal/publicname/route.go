package publicname

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const (
	RouteDirect = "direct"

	RouteRelay = "relay"

	RouteDisabled = "disabled"

	routeRenewOf = 3

	routeDebounce = 15 * time.Second

	routeRetry = time.Minute
	routeIdle  = 10 * time.Minute

	routeLeaseSpreadOf = 10
)

type routeState struct {
	Mode       string
	Records    []certs.Record
	Generation int64
	Managed    bool
	Alias      string
	LeaseUntil time.Time

	Delivered bool
	LastError string
	CheckedAt time.Time
}

func RouteMode(c CertificateConfig) (string, error) {
	switch m := strings.TrimSpace(c.RouteMode); m {
	case "":
		return RouteDirect, nil
	case RouteDirect, RouteRelay, RouteDisabled:
		return m, nil
	default:
		return "", fmt.Errorf("route_mode %q is not direct, relay or disabled", m)
	}
}

func (r *Runtime) Signal() {

	r.applyNameURL()

	r.orderCertificateIfWanted()

	stop := true
	if mode, err := RouteMode(r.w.Settings().Certificate); err == nil && mode == RouteRelay {
		stop = false
	}
	if stop {
		r.stopRelay()
	}
	r.mu.Lock()
	ch := r.routeWake

	r.routeRev++
	cancel := r.routePassCancel
	r.routePassCancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (r *Runtime) startRouteOwner() bool {
	r.mu.Lock()
	if r.routeOwner {
		r.mu.Unlock()
		return false
	}
	r.routeOwner = true
	r.routeWake = make(chan struct{}, 1)
	wake := r.routeWake
	r.mu.Unlock()

	r.w.Go(func() {
		last := ""
		for {
			ctx := r.w.Ctx()
			if ctx == nil {
				return
			}
			next, err := r.lookAtRoute(ctx)
			if err != nil && errors.Is(err, context.Canceled) {

				err = nil
			}
			if err != nil {

				if err.Error() != last {
					logsink.Warn("route.error", "%v", err)
					last = err.Error()
				}
				if next <= 0 {
					next = routeRetry
				}
			} else {
				last = ""
			}
			if next <= 0 {
				next = routeIdle
			}
			deadline := time.Now().Add(next)
			if floor := r.routeFloor(); floor.After(deadline) {
				deadline = floor
			}

			for {
				wait := time.Until(deadline)
				if wait <= 0 {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-wake:

					soonest := time.Now().Add(routeDebounce)
					if floor := r.routeFloor(); floor.After(soonest) {
						soonest = floor
					}
					if soonest.Before(deadline) {
						deadline = soonest
					}
				case <-time.After(wait):
				}
			}
		}
	})
	return true
}

func (r *Runtime) lookAtRoute(ctx context.Context) (time.Duration, error) {
	if next, quiet := r.routeQuiet(); quiet {
		return next, nil
	}
	return r.reconcileRoute(ctx)
}

func (r *Runtime) serviceDelay(err error, fallback time.Duration) time.Duration {
	var rse *certs.RecordSetError
	if errors.As(err, &rse) && rse.RetryAfter > 0 {
		r.mu.Lock()
		r.routeNotBefore = time.Now().Add(rse.RetryAfter)
		r.mu.Unlock()
		return rse.RetryAfter
	}
	return fallback
}

func (r *Runtime) routeFloor() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.routeNotBefore
}

func (r *Runtime) reconcileRoute(ctx context.Context) (time.Duration, error) {
	r.mu.Lock()
	pub, name := r.publisher, ""
	if r.name != nil {
		name = r.name.Name
	}
	r.mu.Unlock()
	if pub == nil || name == "" {
		return routeIdle, nil
	}
	cfg := r.w.Settings()
	mode, err := RouteMode(cfg.Certificate)
	if err != nil {
		r.setRouteError(err.Error())
		return routeIdle, err
	}

	if mode != RouteRelay {
		r.stopRelay()
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	r.mu.Lock()
	rev := r.routeRev
	r.routePassCancel = cancel

	r.routeSettled = false
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.routeRev == rev {
			r.routePassCancel = nil
		}
		r.mu.Unlock()
	}()

	st, err := pub.ServiceStatus(ctx)
	if err != nil {

		if ctx.Err() != nil {
			return routeRetry, ctx.Err()
		}
		r.setRouteError("certificate service: " + err.Error())
		return r.serviceDelay(err, routeRetry), fmt.Errorf("the certificate service could not be reached: %w", err)
	}
	r.mu.Lock()
	r.service, r.serviceAt = &st, time.Now()
	r.mu.Unlock()
	if !st.Supports(certs.CapabilityRecordSets) {
		msg := "the certificate service does not offer record sets; this identity publishes no route until it does"
		r.setRouteError(msg)
		return routeIdle, errors.New(msg)
	}

	lease := st.RouteLease()
	hold := lease
	if mode == RouteRelay {
		hold = st.RelayRouteLease()
	}
	if perHour, ok := routeSustainable(st, hold, hold > lease); !ok {

		msg := fmt.Sprintf("the certificate service publishes for at most %s, which needs about %d route writes an hour against a limit of %d — this identity cannot hold a route at that lifetime", hold, perHour, st.MaxPublishesPerHour)
		r.setRouteError(msg)
		return routeIdle, errors.New(msg)
	}

	desired, err := r.desiredRoute(cfg, mode, st)
	if err != nil {

		r.stopRelay()
		r.setRouteError(err.Error())
		return routeIdle, err
	}

	current, err := pub.ReadRecords(ctx, name, lease)
	if err != nil {
		if ctx.Err() != nil {
			return routeRetry, ctx.Err()
		}

		r.setRouteError("reading the route: " + err.Error())
		return r.serviceDelay(err, routeRetry), fmt.Errorf("the route could not be read: %w", err)
	}
	r.observeRoute(mode, current)

	if sameRoute(current.Records, desired) && !routeNeedsRenewal(current, hold) {
		r.afterRoute(cfg, mode, name, current, st, rev)
		r.routeSettledAt(rev, hold, len(desired) == 0)
		return routeSettleDelay(current, hold), nil
	}

	sign := hold
	if hold > lease {
		sign -= time.Duration(r.draw() * float64(hold/routeLeaseSpreadOf))
	}
	set, err := pub.ReplaceRecords(ctx, name, desired, current.Generation, sign)
	switch {
	case err == nil:
	case errors.Is(err, certs.ErrRecordSetConflict):

		r.setRouteError("another writer moved the route; recomputing")
		return routeDebounce, nil
	case errors.Is(err, certs.ErrRecordSetAmbiguous):

		r.setRouteError("the route write may or may not have committed; reading it back")
		return routeDebounce, fmt.Errorf("the route write was not acknowledged: %w", err)
	default:
		if ctx.Err() != nil {
			return routeRetry, ctx.Err()
		}
		if wait := r.serviceDelay(err, 0); wait > 0 {
			r.setRouteError("the service asked us to wait: " + err.Error())
			return wait, nil
		}
		r.setRouteError("publishing the route: " + err.Error())
		return routeRetry, fmt.Errorf("the route could not be published: %w", err)
	}

	r.observeRoute(mode, set)
	what := "withdrawn"
	if len(desired) > 0 {
		what = routeSummary(desired)
	}
	told := fmt.Sprintf("%s|%s|%t", mode, what, set.Delivered)
	r.mu.Lock()
	changed := r.routeTold != told
	if changed {
		r.routeTold = told
	}
	r.mu.Unlock()
	if !changed {

		logsink.Tick("route.renewal", "renewals unchanged", 0)
	}
	if changed {
		if set.Delivered {
			logsink.Info("route.start", "%s is %s (generation %d)", name, what, set.Generation)
		} else {
			logsink.Info("route.decision", "%s is %s at generation %d — committed, and the service has not finished delivering it; it reconciles that itself", name, what, set.Generation)
		}
	}
	r.afterRoute(cfg, mode, name, set, st, rev)
	r.routeSettledAt(rev, hold, len(desired) == 0)
	return routeSettleDelay(set, hold), nil
}

func (r *Runtime) routeSettledAt(rev uint64, hold time.Duration, empty bool) {
	r.mu.Lock()
	r.routeSettled, r.routeSettledRev, r.routeHold, r.routeSettledEmpty = true, rev, hold, empty
	r.mu.Unlock()
}

func (r *Runtime) routeQuiet() (time.Duration, bool) {
	cfg := r.w.Settings()
	mode, err := RouteMode(cfg.Certificate)

	nothing := err == nil && wantsNothing(cfg, mode)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StatusPrepared {
		return routeIdle, true
	}
	if err != nil {
		return 0, false
	}
	if mode != RouteRelay {

		if r.routeSettled && r.routeSettledRev == r.routeRev && r.routeSettledEmpty && nothing {
			return routeIdle, true
		}
		return 0, false
	}
	if !r.routeSettled || r.routeSettledRev != r.routeRev || r.routeHold <= 0 {
		return 0, false
	}
	if r.relay == nil || !r.relay.State().Connected {
		return 0, false
	}
	rt := r.route
	if rt.Mode != RouteRelay || rt.LeaseUntil.IsZero() {
		return 0, false
	}
	left := time.Until(rt.LeaseUntil) - r.routeHold/routeRenewOf
	if left <= 0 {
		return 0, false
	}
	if left > routeIdle {
		left = routeIdle
	}
	return left, true
}

func wantsNothing(cfg Settings, mode string) bool {
	switch mode {
	case RouteDisabled:
		return true
	case RouteDirect:
		records, err := directRoute(cfg)
		return err == nil && len(records) == 0
	}
	return false
}

func (r *Runtime) draw() float64 {
	if r.random != nil {
		return r.random()
	}
	return mathrand.Float64()
}

func (r *Runtime) afterRoute(cfg Settings, mode, name string, set certs.RecordSet, st certs.ServiceStatus, rev uint64) {

	r.mu.Lock()
	stale := r.routeRev != rev
	r.mu.Unlock()
	if now, err := RouteMode(r.w.Settings().Certificate); stale || err != nil || now != mode {
		r.stopRelay()
		return
	}
	if mode != RouteRelay {
		r.stopRelay()
		return
	}
	target, ok := set.CNAME()
	if !ok {

		r.stopRelay()
		return
	}
	endpoint, ok := st.Relay(target)
	if !ok {
		r.stopRelay()
		return
	}
	r.startRelay(cfg, name, endpoint)
}

func routeNeedsRenewal(set certs.RecordSet, lease time.Duration) bool {
	if len(set.Records) == 0 {

		return !set.Managed
	}
	earliest := time.Time{}
	for _, r := range set.Records {
		when, err := time.Parse(time.RFC3339, r.ExpiresAt)
		if err != nil {
			return true
		}
		if earliest.IsZero() || when.Before(earliest) {
			earliest = when
		}
	}
	if earliest.IsZero() {
		return true
	}
	return time.Until(earliest) <= lease/routeRenewOf
}

func routeSettleDelay(set certs.RecordSet, lease time.Duration) time.Duration {
	earliest := time.Time{}
	for _, r := range set.Records {
		when, err := time.Parse(time.RFC3339, r.ExpiresAt)
		if err != nil {
			return routeDebounce
		}
		if earliest.IsZero() || when.Before(earliest) {
			earliest = when
		}
	}
	if earliest.IsZero() {

		return routeIdle
	}
	hard := time.Until(earliest)
	due := hard - lease/routeRenewOf
	if due < routeDebounce {
		due = routeDebounce
	}
	if due > routeIdle {
		due = routeIdle
	}

	if hard > 0 && due > hard {
		due = hard
	}
	if due < time.Second {
		due = time.Second
	}
	return due
}

func routeSustainable(st certs.ServiceStatus, lease time.Duration, spread bool) (int, bool) {
	if lease <= 0 {
		return 0, false
	}
	interval := lease - lease/routeRenewOf
	if spread {
		interval -= lease/routeLeaseSpreadOf + time.Second
	}
	if interval <= 0 {
		return 0, false
	}
	perHour := int(time.Hour/interval) + 1
	budget := st.MaxPublishesPerHour
	if budget <= 0 {
		return perHour, true
	}

	return perHour, perHour+4 <= budget
}

func (r *Runtime) desiredRoute(cfg Settings, mode string, st certs.ServiceStatus) ([]certs.Record, error) {
	switch mode {
	case RouteDisabled:
		return []certs.Record{}, nil
	case RouteRelay:
		if !serviceOffersRelays(st) {
			return nil, errors.New("relay mode was chosen, and the certificate service does not offer relays")
		}
		host, port := relayChoice(cfg.Certificate)
		if host == "" {
			return nil, errors.New("relay mode needs certificate.relay_endpoint set to one the service advertises")
		}
		endpoint, ok := st.Relay(host)
		if !ok {
			return nil, fmt.Errorf("the certificate service does not advertise a relay called %q; discovery offers choices and never enables one", host)
		}
		if port != "" && port != fmt.Sprint(endpoint.Port) {
			return nil, fmt.Errorf("relay_endpoint names port %s and the service advertises %d for %s", port, endpoint.Port, endpoint.Name)
		}

		if bound := r.boundPort(); port == "" && bound != "" && bound != fmt.Sprint(endpoint.Port) {
			name := r.claimedName()
			return nil, &RelayPortUnnamed{Relay: endpoint.Name, Port: endpoint.Port, From: publicOrigin(name, bound), To: publicOrigin(name, fmt.Sprint(endpoint.Port)), FromPort: bound}
		}
		return []certs.Record{{Type: certs.RecordCNAME, Value: endpoint.Name}}, nil
	default:
		records, err := directRoute(cfg)
		if err != nil {
			return nil, err
		}

		for _, r := range records {
			if !st.AcceptsRecordType(r.Type) {
				return nil, fmt.Errorf("this machine is reached at %s and the certificate service does not accept %s in an identity record set", r.Value, r.Type)
			}
		}
		return records, nil
	}
}

func serviceOffersRelays(st certs.ServiceStatus) bool {
	return st.Supports(certs.CapabilityRelayV2) && st.AcceptsRecordType(certs.RecordCNAME)
}

func relayChoice(c CertificateConfig) (host, port string) {
	choice := strings.TrimSpace(c.RelayEndpoint)
	if h, p, err := net.SplitHostPort(choice); err == nil {
		return h, p
	}
	return choice, ""
}

func suggestedRelay(name, held string, relays []certs.RelayEndpoint) (certs.RelayEndpoint, bool) {
	var best certs.RelayEndpoint
	var top [sha256.Size]byte
	found := false
	for _, e := range relays {
		if held != "" && e.Name == held {
			return e, true
		}
		rank := sha256.Sum256([]byte(name + "\x00" + e.Name))
		if c := bytes.Compare(rank[:], top[:]); !found || c > 0 || (c == 0 && e.Name < best.Name) {
			best, top, found = e, rank, true
		}
	}
	return best, found
}

func namePort(cfg Settings, bound string) string {
	if mode, err := RouteMode(cfg.Certificate); err == nil && mode == RouteRelay {
		if _, port := relayChoice(cfg.Certificate); port != "" {
			return port
		}
	}
	return bound
}

type RelayPortUnnamed struct {
	Relay    string
	Port     int
	From, To string
	FromPort string
}

func (e *RelayPortUnnamed) Error() string {
	return fmt.Sprintf("relay %s serves port %d and the name is reached on port %s (%s); choosing it moves the address to %s, and a CNAME cannot bridge a port, so the choice must name the port to accept that: certificate.relay_endpoint = %s:%d", e.Relay, e.Port, e.FromPort, e.From, e.To, e.Relay, e.Port)
}

func (r *Runtime) boundPort() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bound
}

func (r *Runtime) claimedName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.name == nil {
		return ""
	}
	return r.name.Name
}

func (r *Runtime) applyNameURL() { r.applyNameURLFor(r.w.Settings()) }

func (r *Runtime) applyNameURLFor(cfg Settings) string {
	dash := r.w.Dashboard()
	r.mu.Lock()
	if r.name == nil {
		r.mu.Unlock()
		return ""
	}
	name, bound := r.name.Name, r.bound
	port := namePort(cfg, bound)
	origin := publicOrigin(name, port)
	r.origin = origin
	r.mu.Unlock()
	if dash == nil {
		return origin
	}
	for _, p := range []string{bound, port} {
		if p != "" {
			dash.AllowHost(net.JoinHostPort(name, p))
		}
		if p == "443" {

			dash.AllowHost(name)
		}
	}
	if cfg.Origin == "" {
		dash.SetOrigin(origin)
	}
	return origin
}

func directRoute(cfg Settings) ([]certs.Record, error) {
	if explicit := cfg.Certificate.DirectAddresses; explicit != nil {

		if err := CheckDirectAddresses(*explicit); err != nil {
			return nil, err
		}
		out := make([]certs.Record, 0, len(*explicit))
		for _, s := range *explicit {
			ip := net.ParseIP(strings.TrimSpace(s))
			kind := certs.RecordAAAA
			if ip.To4() != nil {
				kind = certs.RecordA
			}
			out = append(out, certs.Record{Type: kind, Value: ip.String()})
		}
		return out, nil
	}
	host := cfg.Host
	if dashboard.IsLoopback(host) {

		return []certs.Record{}, nil
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return []certs.Record{addressRecord(ip)}, nil
	}
	if cfg.Origin != "" {
		if u, err := url.Parse(cfg.Origin); err == nil {
			if ip := net.ParseIP(u.Hostname()); ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
				return []certs.Record{addressRecord(ip)}, nil
			}
		}
	}
	addrs, err := interfaceAddrs()
	if err != nil {

		return nil, fmt.Errorf("this machine's addresses could not be read: %w", err)
	}
	var v4, v6 []certs.Record
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || !n.IP.IsGlobalUnicast() {
			continue
		}
		if n.IP.IsLinkLocalUnicast() {

			continue
		}
		r := addressRecord(n.IP)
		if r.Type == certs.RecordA {
			v4 = append(v4, r)
		} else {
			v6 = append(v6, r)
		}
	}

	out := []certs.Record{}
	for family, found := range map[string][]certs.Record{"IPv4": v4, "IPv6": v6} {
		switch len(found) {
		case 0:
		case 1:
			out = append(out, found[0])
		default:
			values := make([]string, 0, len(found))
			for _, r := range found {
				values = append(values, r.Value)
			}
			sort.Strings(values)
			return nil, fmt.Errorf("this machine has more than one %s address (%s) and the dashboard binds every interface; name the ones to publish in certificate.direct_addresses", family, strings.Join(values, ", "))
		}
	}
	return out, nil
}

func CheckDirectAddresses(list []string) error {
	for _, s := range list {
		if net.ParseIP(strings.TrimSpace(s)) == nil {
			return fmt.Errorf("certificate.direct_addresses carries %q, which is not an IP address", s)
		}
	}
	return nil
}

func (r *Runtime) DirectChoiceRefusal(now Settings) error {
	if mode, err := RouteMode(now.Certificate); err != nil || mode != RouteDirect {
		return nil
	}
	if _, err := directRoute(now); err != nil {
		return fmt.Errorf("certificate.direct_addresses: %w", err)
	}
	return nil
}

func machineAddresses() []string {
	addrs, err := interfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || !n.IP.IsGlobalUnicast() || n.IP.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, addressRecord(n.IP).Value)
	}
	sort.Strings(out)
	return out
}

var interfaceAddrs = net.InterfaceAddrs

func addressRecord(ip net.IP) certs.Record {
	if v4 := ip.To4(); v4 != nil {
		return certs.Record{Type: certs.RecordA, Value: v4.String()}
	}
	return certs.Record{Type: certs.RecordAAAA, Value: ip.String()}
}

func sameRoute(have []certs.LeasedRecord, want []certs.Record) bool {
	mine := make([]certs.Record, 0, len(have))
	for _, r := range have {
		mine = append(mine, certs.Record{Type: r.Type, Value: r.Value})
	}
	left, err := certs.CanonicalRecords(mine)
	if err != nil {
		return false
	}
	right, err := certs.CanonicalRecords(want)
	if err != nil {
		return false
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func routeSummary(records []certs.Record) string {
	if len(records) == 0 {
		return "withdrawn"
	}
	if len(records) == 1 && records[0].Type == certs.RecordCNAME {
		return "relayed through " + records[0].Value
	}
	values := make([]string, 0, len(records))
	for _, r := range records {
		values = append(values, r.Value)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

func (r *Runtime) observeRoute(mode string, set certs.RecordSet) {
	lease := time.Time{}
	for _, r := range set.Records {
		if when, err := time.Parse(time.RFC3339, r.ExpiresAt); err == nil {
			if lease.IsZero() || when.Before(lease) {
				lease = when
			}
		}
	}
	records := make([]certs.Record, 0, len(set.Records))
	for _, r := range set.Records {
		records = append(records, certs.Record{Type: r.Type, Value: r.Value})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.route = routeState{
		Mode: mode, Records: records, Generation: set.Generation, Managed: set.Managed,
		Alias: set.Alias, LeaseUntil: lease, Delivered: set.Delivered, CheckedAt: time.Now(),
	}

	r.address = routeSummary(records)
	if set.Alias != "" {
		r.alias = set.Alias
	}
}

func (r *Runtime) setRouteError(msg string) {
	r.mu.Lock()
	r.route.LastError = msg

	r.routeTold = ""
	r.lastError = "route: " + msg
	r.mu.Unlock()
}

func (r *Runtime) stopRelay() <-chan struct{} {
	r.mu.Lock()
	cancel := r.relayCancel
	done := r.relayDone

	if done == nil {
		r.relayCancel, r.relay = nil, nil
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return done
}
