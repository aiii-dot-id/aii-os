package app

import (
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// .
// .
// .
// .
// .

// .
const hostAddrsTTL = time.Minute

var hostAddrsCache struct {
	mu   sync.Mutex
	at   time.Time
	list []net.IP
}

// .
func hostAddrs() []net.IP {
	hostAddrsCache.mu.Lock()
	defer hostAddrsCache.mu.Unlock()
	if time.Since(hostAddrsCache.at) < hostAddrsTTL && hostAddrsCache.list != nil {
		return hostAddrsCache.list
	}
	var out []net.IP
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP != nil {
				out = append(out, n.IP)
			}
		}
	}
	hostAddrsCache.at, hostAddrsCache.list = time.Now(), out
	return out
}

// .
// .
// .
func (a *App) ownPorts() map[int]bool {
	out := map[int]bool{}
	if a.dashboard == nil {
		return out
	}
	if _, p, err := net.SplitHostPort(a.dashboard.BoundAddr()); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			out[n] = true
		}
	}
	return out
}

// .
// .
func (a *App) ownListener(ip net.IP, port int) bool {
	return refuseOwn(ip, port, a.ownPorts(), hostAddrs())
}

// .
// .
func refuseOwn(ip net.IP, port int, ownPorts map[int]bool, addrs []net.IP) bool {
	if !ownPorts[port] {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, own := range addrs {
		if own.Equal(ip) {
			return true
		}
	}
	return false
}

// .
// .
func (a *App) applyLocalFetch(cfg Config, reg interface {
	SetLocalFetch([]string, func(net.IP, int) bool) []string
}) {
	if reg == nil {
		return
	}
	rejected := reg.SetLocalFetch(cfg.Tools.LocalHosts, a.ownListener)
	if len(rejected) > 0 {
		log.Printf("Config: tools.local_hosts entries ignored: %s", strings.Join(rejected, "; "))
	} else if n := len(cfg.Tools.LocalHosts); n > 0 {
		log.Printf("Config: web_fetch may reach %d local device(s) the operator named (tools.local_hosts)", n)
	}
}
