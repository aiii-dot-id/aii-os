package relay

import "time"

func (c *Client) MarkConnectedForTest(connected bool) {
	c.mu.Lock()
	c.connected = connected
	c.mu.Unlock()
}

func (c *Client) WokenForTest() bool {
	select {
	case <-c.woken():
		return true
	default:
		return false
	}
}

func (c *Client) HoldServersForTest(servers []SessionServer, expire time.Time) {
	c.holdServers(Message{SessionServers: servers, SessionServersExpireAt: expire.UTC().Format(time.RFC3339)})
}
