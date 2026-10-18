package relay

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
