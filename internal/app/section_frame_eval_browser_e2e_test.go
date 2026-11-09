//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// inSession sends one debugger command to a session (the page's own when
// session is empty) and returns its answer. A frame the browser attaches on the
// way is noted in attached, its session.
func (c *cdp) inSession(session, method string, params map[string]any, attached map[string]bool, budget time.Duration) (json.RawMessage, error) {
	c.seq++
	id := c.seq
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	msg := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		msg["sessionId"] = session
	}
	req, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, req); err != nil {
		return nil, fmt.Errorf("cdp write: %w", err)
	}
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("cdp read: %w", err)
		}
		var resp struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &resp) != nil {
			continue
		}
		if resp.Method == "Target.attachedToTarget" && attached != nil {
			var a struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(resp.Params, &a) == nil {
				attached[a.SessionID] = true
			}
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("cdp: %s", resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// evalInFrame evaluates expr inside the frame whose address holds part, once
// that frame is there. A page's own script cannot read a sandboxed section's
// frame; a browser's debugger can. Chrome runs such a frame in a process of its
// own, so it is attached to as its own target, and this reads what the operator
// sees and touches nothing of the section.
func (c *cdp) evalInFrame(part, expr string, budget time.Duration) (string, error) {
	deadline := time.Now().Add(budget)
	attached := map[string]bool{}
	session := ""
	for session == "" {
		if _, err := c.inSession("", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}, attached, 5*time.Second); err != nil {
			return "", err
		}
		// A frame attached before it navigated has no address on its target:
		// it is asked.
		for s := range attached {
			res, err := c.inSession(s, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, attached, 5*time.Second)
			if err == nil && strings.Contains(string(res), part) {
				session = s
			}
		}
		if session != "" {
			break
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no frame at %q in %s (attached: %v)", part, budget, attached)
		}
		time.Sleep(100 * time.Millisecond)
	}
	res, err := c.inSession(session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}, nil, time.Until(deadline)+5*time.Second)
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if out.ExceptionDetails != nil {
		return "", fmt.Errorf("frame threw: %s", out.ExceptionDetails.Text)
	}
	return out.Result.Value, nil
}
