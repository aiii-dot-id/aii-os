package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
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

const (
	// .
	routeDirect = "direct"
	// .
	routeRelay = "relay"
	// .
	// .
	routeDisabled = "disabled"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	routeRenewOf = 3

	// .
	// .
	// .
	// .
	// .
	routeDebounce = 15 * time.Second
	// .
	routeRetry = time.Minute
	routeIdle  = 10 * time.Minute
)

// .
// .
// .
type routeState struct {
	Mode       string
	Records    []certs.Record
	Generation int64
	Managed    bool
	Alias      string
	LeaseUntil time.Time
	// .
	// .
	// .
	// .
	Delivered bool
	LastError string
	CheckedAt time.Time
}

// .
// .
// .
// .
func routeMode(cfg Config) (string, error) {
	switch m := strings.TrimSpace(cfg.Certificate.RouteMode); m {
	case "":
		return routeDirect, nil
	case routeDirect, routeRelay, routeDisabled:
		return m, nil
	default:
		return "", fmt.Errorf("route_mode %q is not direct, relay or disabled", m)
	}
}

// .
// .
// .
func (a *App) signalRoute() {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	stop := true
	if mode, err := routeMode(a.configSnapshot()); err == nil && mode == routeRelay {
		stop = false
	}
	if stop {
		a.stopRelay()
	}
	a.pn.mu.Lock()
	ch := a.pn.routeWake
	// .
	// .
	a.pn.routeRev++
	cancel := a.pn.routePassCancel
	a.pn.routePassCancel = nil
	a.pn.mu.Unlock()
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

// .
// .
// .
// .
func (a *App) startRouteOwner() bool {
	a.pn.mu.Lock()
	if a.pn.routeOwner {
		a.pn.mu.Unlock()
		return false
	}
	a.pn.routeOwner = true
	a.pn.routeWake = make(chan struct{}, 1)
	wake := a.pn.routeWake
	a.pn.mu.Unlock()

	a.runBackground(func() {
		last := ""
		for {
			ctx := a.bgCtx
			if ctx == nil {
				return
			}
			next, err := a.reconcileRoute(ctx)
			if err != nil && errors.Is(err, context.Canceled) {
				// .
				// .
				err = nil
			}
			if err != nil {
				// .
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
			if floor := a.routeFloor(); floor.After(deadline) {
				deadline = floor
			}
			// .
			// .
			for {
				wait := time.Until(deadline)
				if wait <= 0 {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-wake:
					// .
					// .
					soonest := time.Now().Add(routeDebounce)
					if floor := a.routeFloor(); floor.After(soonest) {
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

// .
// .
// .
// .
// .
func (a *App) serviceDelay(err error, fallback time.Duration) time.Duration {
	var rse *certs.RecordSetError
	if errors.As(err, &rse) && rse.RetryAfter > 0 {
		a.pn.mu.Lock()
		a.pn.routeNotBefore = time.Now().Add(rse.RetryAfter)
		a.pn.mu.Unlock()
		return rse.RetryAfter
	}
	return fallback
}

// .
// .
func (a *App) routeFloor() time.Time {
	a.pn.mu.Lock()
	defer a.pn.mu.Unlock()
	return a.pn.routeNotBefore
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) reconcileRoute(ctx context.Context) (time.Duration, error) {
	a.pn.mu.Lock()
	pub, name := a.pn.publisher, ""
	if a.pn.name != nil {
		name = a.pn.name.Name
	}
	a.pn.mu.Unlock()
	if pub == nil || name == "" {
		return routeIdle, nil
	}
	cfg := a.configSnapshot()
	mode, err := routeMode(cfg)
	if err != nil {
		a.setRouteError(err.Error())
		return routeIdle, err
	}

	// .
	// .
	// .
	// .
	// .
	// .
	if mode != routeRelay {
		a.stopRelay()
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// .
	// .
	a.pn.mu.Lock()
	rev := a.pn.routeRev
	a.pn.routePassCancel = cancel
	a.pn.mu.Unlock()
	defer func() {
		a.pn.mu.Lock()
		if a.pn.routeRev == rev {
			a.pn.routePassCancel = nil
		}
		a.pn.mu.Unlock()
	}()

	st, err := pub.ServiceStatus(ctx)
	if err != nil {
		// .
		// .
		// .
		// .
		if ctx.Err() != nil {
			return routeRetry, ctx.Err()
		}
		a.setRouteError("certificate service: " + err.Error())
		return a.serviceDelay(err, routeRetry), fmt.Errorf("the certificate service could not be reached: %w", err)
	}
	if !st.Supports(certs.CapabilityRecordSets) {
		msg := "the certificate service does not offer record sets; this identity publishes no route until it does"
		a.setRouteError(msg)
		return routeIdle, errors.New(msg)
	}
	lease := st.RouteLease()
	if perHour, ok := routeSustainable(st); !ok {
		// .
		// .
		// .
		// .
		msg := fmt.Sprintf("the certificate service publishes for at most %s, which needs about %d route writes an hour against a limit of %d — this identity cannot hold a route at that lifetime", lease, perHour, st.MaxPublishesPerHour)
		a.setRouteError(msg)
		return routeIdle, errors.New(msg)
	}

	desired, err := a.desiredRoute(cfg, mode, st)
	if err != nil {
		// .
		// .
		a.stopRelay()
		a.setRouteError(err.Error())
		return routeIdle, err
	}

	current, err := pub.ReadRecords(ctx, name, lease)
	if err != nil {
		if ctx.Err() != nil {
			return routeRetry, ctx.Err()
		}
		// .
		// .
		// .
		// .
		a.setRouteError("reading the route: " + err.Error())
		return a.serviceDelay(err, routeRetry), fmt.Errorf("the route could not be read: %w", err)
	}
	a.observeRoute(mode, current)

	// .
	// .
	// .
	if sameRoute(current.Records, desired) && !routeNeedsRenewal(current, lease) {
		a.afterRoute(cfg, mode, name, current, st, rev)
		return routeSettleDelay(current, lease), nil
	}

	set, err := pub.ReplaceRecords(ctx, name, desired, current.Generation, lease)
	switch {
	case err == nil:
	case errors.Is(err, certs.ErrRecordSetConflict):
		// .
		// .
		// .
		a.setRouteError("another writer moved the route; recomputing")
		return routeDebounce, nil
	case errors.Is(err, certs.ErrRecordSetAmbiguous):
		// .
		// .
		// .
		a.setRouteError("the route write may or may not have committed; reading it back")
		return routeDebounce, fmt.Errorf("the route write was not acknowledged: %w", err)
	default:
		if ctx.Err() != nil {
			return routeRetry, ctx.Err()
		}
		if wait := a.serviceDelay(err, 0); wait > 0 {
			a.setRouteError("the service asked us to wait: " + err.Error())
			return wait, nil
		}
		a.setRouteError("publishing the route: " + err.Error())
		return routeRetry, fmt.Errorf("the route could not be published: %w", err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	a.observeRoute(mode, set)
	what := "withdrawn"
	if len(desired) > 0 {
		what = routeSummary(desired)
	}
	told := fmt.Sprintf("%s|%s|%t", mode, what, set.Delivered)
	a.pn.mu.Lock()
	changed := a.pn.routeTold != told
	if changed {
		a.pn.routeTold = told
	}
	a.pn.mu.Unlock()
	if !changed {
		// .
		// .
		logsink.Tick("route.renewal", "renewals unchanged", 0)
	}
	if changed {
		if set.Delivered {
			logsink.Info("route.start", "%s is %s (generation %d)", name, what, set.Generation)
		} else {
			logsink.Info("route.decision", "%s is %s at generation %d — committed, and the service has not finished delivering it; it reconciles that itself", name, what, set.Generation)
		}
	}
	a.afterRoute(cfg, mode, name, set, st, rev)
	return routeSettleDelay(set, lease), nil
}

// .
// .
func (a *App) afterRoute(cfg Config, mode, name string, set certs.RecordSet, st certs.ServiceStatus, rev uint64) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.pn.mu.Lock()
	stale := a.pn.routeRev != rev
	a.pn.mu.Unlock()
	if now, err := routeMode(a.configSnapshot()); stale || err != nil || now != mode {
		a.stopRelay()
		return
	}
	if mode != routeRelay {
		a.stopRelay()
		return
	}
	target, ok := set.CNAME()
	if !ok {
		// .
		// .
		a.stopRelay()
		return
	}
	endpoint, ok := st.Relay(target)
	if !ok {
		a.stopRelay()
		return
	}
	a.startRelay(cfg, name, endpoint)
}

// .
// .
func routeNeedsRenewal(set certs.RecordSet, lease time.Duration) bool {
	if len(set.Records) == 0 {
		// .
		// .
		// .
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

// .
// .
// .
// .
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
		// .
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
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if hard > 0 && due > hard {
		due = hard
	}
	if due < time.Second {
		due = time.Second
	}
	return due
}

// .
// .
// .
// .
// .
func routeSustainable(st certs.ServiceStatus) (int, bool) {
	lease := st.RouteLease()
	if lease <= 0 {
		return 0, false
	}
	perHour := int(time.Hour/(lease-lease/routeRenewOf)) + 1
	budget := st.MaxPublishesPerHour
	if budget <= 0 {
		return perHour, true
	}
	// .
	// .
	// .
	return perHour, perHour+4 <= budget
}

// .
func (a *App) desiredRoute(cfg Config, mode string, st certs.ServiceStatus) ([]certs.Record, error) {
	switch mode {
	case routeDisabled:
		return []certs.Record{}, nil
	case routeRelay:
		if !st.Supports(certs.CapabilityRelayV2) || !st.AcceptsRecordType(certs.RecordCNAME) {
			return nil, errors.New("relay mode was chosen, and the certificate service does not offer relays")
		}
		choice := strings.TrimSpace(cfg.Certificate.RelayEndpoint)
		if choice == "" {
			return nil, errors.New("relay mode needs certificate.relay_endpoint set to one the service advertises")
		}
		host, port := choice, ""
		if h, p, err := net.SplitHostPort(choice); err == nil {
			host, port = h, p
		}
		endpoint, ok := st.Relay(host)
		if !ok {
			return nil, fmt.Errorf("the certificate service does not advertise a relay called %q; discovery offers choices and never enables one", host)
		}
		// .
		// .
		// .
		// .
		if want := a.originPort(cfg); want != "" && want != fmt.Sprint(endpoint.Port) {
			return nil, fmt.Errorf("relay %s serves port %d and this identity is reached on %s; a CNAME cannot bridge that, so either choose a relay on %s or change the origin deliberately", endpoint.Name, endpoint.Port, want, want)
		}
		if port != "" && port != fmt.Sprint(endpoint.Port) {
			return nil, fmt.Errorf("relay_endpoint names port %s and the service advertises %d for %s", port, endpoint.Port, endpoint.Name)
		}
		return []certs.Record{{Type: certs.RecordCNAME, Value: endpoint.Name}}, nil
	default:
		records, err := directRoute(cfg)
		if err != nil {
			return nil, err
		}
		// .
		// .
		// .
		for _, r := range records {
			if !st.AcceptsRecordType(r.Type) {
				return nil, fmt.Errorf("this machine is reached at %s and the certificate service does not accept %s in an identity record set", r.Value, r.Type)
			}
		}
		return records, nil
	}
}

// .
func (a *App) originPort(cfg Config) string {
	a.pn.mu.Lock()
	origin := a.pn.origin
	a.pn.mu.Unlock()
	if cfg.Dashboard.Origin != "" {
		origin = cfg.Dashboard.Origin
	}
	if origin == "" {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return ""
}

// .
// .
// .
// .
// .
// .
// .
func directRoute(cfg Config) ([]certs.Record, error) {
	if explicit := cfg.Certificate.DirectAddresses; explicit != nil {
		// .
		// .
		// .
		out := make([]certs.Record, 0, len(*explicit))
		for _, s := range *explicit {
			ip := net.ParseIP(strings.TrimSpace(s))
			if ip == nil {
				return nil, fmt.Errorf("certificate.direct_addresses carries %q, which is not an IP address", s)
			}
			kind := certs.RecordAAAA
			if ip.To4() != nil {
				kind = certs.RecordA
			}
			out = append(out, certs.Record{Type: kind, Value: ip.String()})
		}
		return out, nil
	}
	host := cfg.Dashboard.Host
	if dashboard.IsLoopback(host) {
		// .
		// .
		return []certs.Record{}, nil
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return []certs.Record{addressRecord(ip)}, nil
	}
	if cfg.Dashboard.Origin != "" {
		if u, err := url.Parse(cfg.Dashboard.Origin); err == nil {
			if ip := net.ParseIP(u.Hostname()); ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
				return []certs.Record{addressRecord(ip)}, nil
			}
		}
	}
	addrs, err := interfaceAddrs()
	if err != nil {
		// .
		// .
		// .
		return nil, fmt.Errorf("this machine's addresses could not be read: %w", err)
	}
	var v4, v6 []certs.Record
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || !n.IP.IsGlobalUnicast() {
			continue
		}
		if n.IP.IsLinkLocalUnicast() {
			// .
			continue
		}
		r := addressRecord(n.IP)
		if r.Type == certs.RecordA {
			v4 = append(v4, r)
		} else {
			v6 = append(v6, r)
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
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

// .
// .
var interfaceAddrs = net.InterfaceAddrs

func addressRecord(ip net.IP) certs.Record {
	if v4 := ip.To4(); v4 != nil {
		return certs.Record{Type: certs.RecordA, Value: v4.String()}
	}
	return certs.Record{Type: certs.RecordAAAA, Value: ip.String()}
}

// .
// .
// .
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

// .
// .
func (a *App) observeRoute(mode string, set certs.RecordSet) {
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
	a.pn.mu.Lock()
	defer a.pn.mu.Unlock()
	a.pn.route = routeState{
		Mode: mode, Records: records, Generation: set.Generation, Managed: set.Managed,
		Alias: set.Alias, LeaseUntil: lease, Delivered: set.Delivered, CheckedAt: time.Now(),
	}
	// .
	a.pn.address = routeSummary(records)
	if set.Alias != "" {
		a.pn.alias = set.Alias
	}
}

func (a *App) setRouteError(msg string) {
	a.pn.mu.Lock()
	a.pn.route.LastError = msg
	// .
	a.pn.routeTold = ""
	a.pn.lastError = "route: " + msg
	a.pn.mu.Unlock()
}

// .
// .
func (a *App) stopRelay() {
	a.pn.mu.Lock()
	cancel := a.pn.relayCancel
	a.pn.relayCancel, a.pn.relay = nil, nil
	a.pn.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
