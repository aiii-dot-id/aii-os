package dashboard

import (
	"fmt"
	"strings"
)

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

const (
	DefaultDirectWaitMS, MinDirectWaitMS, MaxDirectWaitMS                         = 3000, 500, 30000
	DefaultDirectReadyAllowanceMS, MinDirectReadyAllowanceMS, MaxReadyAllowanceMS = 1500, 100, 10000
	DefaultChannelSessions, MinChannelSessions, MaxChannelSessions                = 16, 1, 256
	DefaultChannelQueueBytes, MinChannelQueueBytes, MaxChannelQueueBytes          = 4 << 20, 64 << 10, 64 << 20
	DefaultChannelStallMS, MinChannelStallMS, MaxChannelStallMS                   = 10000, 1000, 120000
)

type ChannelSettings struct {
	Off              bool
	WaitMS           int
	ReadyAllowanceMS int
	Sessions         int
	QueueBytes       int
	StallMS          int
	ICEServers       []ICEServer
	ICEServersNamed  bool
}

func (c ChannelSettings) InForce() ChannelSettings { return c.held() }

func CheckICEServers(servers []ICEServer) error {
	for i, sv := range servers {
		if len(sv.URLs) == 0 {
			return fmt.Errorf("dashboard.ice_servers: server %d names no urls", i+1)
		}
		for _, u := range sv.URLs {
			scheme, rest, ok := strings.Cut(u, ":")
			if !ok || rest == "" || (scheme != "stun" && scheme != "stuns" && scheme != "turn" && scheme != "turns") {
				return fmt.Errorf("dashboard.ice_servers: %q is not a stun:, turn: or turns: address", u)
			}
			if strings.Contains(rest, "@") {
				return fmt.Errorf("dashboard.ice_servers: an address carries a name or password inside it; they go in username and credential")
			}
		}
	}
	return nil
}

type carriedKey struct{}

func within(n, unset, least, most int) int {
	switch {
	case n <= 0:
		return unset
	case n < least:
		return least
	case n > most:
		return most
	}
	return n
}
