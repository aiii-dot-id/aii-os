package llm

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type ToolSchemaRefusedError struct {
	Tool string
	Err  error
}

func (e *ToolSchemaRefusedError) Error() string {
	return fmt.Sprintf("the model provider would not accept the definition of the tool %q: %v", e.Tool, e.Err)
}

func (e *ToolSchemaRefusedError) Unwrap() error { return e.Err }

func (e *HTTPError) refusedToolIndex(d Dialect) (int, bool) {
	if e.StatusCode != http.StatusBadRequest {
		return 0, false
	}
	if d == DialectGemini {
		return geminiRefusedDeclaration(e.Body)
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &body) != nil {
		return 0, false
	}
	if d == DialectAnthropic {
		if body.Error.Type != "invalid_request_error" {
			return 0, false
		}
		n, field, ok := leadingIndex(body.Error.Message, "tools.", ".")
		if !ok {
			return 0, false
		}
		if namesAMember(strings.TrimPrefix(field, "custom."), "input_schema", "name", "description") {
			return n, true
		}
		return 0, false
	}
	if body.Error.Code != "invalid_function_parameters" {
		return 0, false
	}
	n, _, ok := leadingIndex(body.Error.Param, "tools[", "]")
	return n, ok
}

func geminiRefusedDeclaration(body string) (int, bool) {
	var status struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				FieldViolations []struct {
					Field string `json:"field"`
				} `json:"fieldViolations"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &status) != nil || status.Error.Status != "INVALID_ARGUMENT" {
		return 0, false
	}
	const declarations = "tools[0].function_declarations["
	members := []string{".name", ".description", ".parameters", ".parameters_json_schema"}
	for _, d := range status.Error.Details {
		for _, v := range d.FieldViolations {
			if n, member, ok := leadingIndex(v.Field, declarations, "]"); ok && namesAMember(member, members...) {
				return n, true
			}
		}
	}
	if n, member, ok := leadingIndex(status.Error.Message, "* GenerateContentRequest."+declarations, "]"); ok && namesAMember(member, members...) {
		return n, true
	}
	return 0, false
}

func namesAMember(field string, members ...string) bool {
	for _, of := range members {
		if rest, is := strings.CutPrefix(field, of); is && (rest == "" || rest[0] == ':' || rest[0] == '.' || rest[0] == ' ') {
			return true
		}
	}
	return false
}

func leadingIndex(s, open, close string) (n int, after string, ok bool) {
	rest, ok := strings.CutPrefix(s, open)
	if !ok {
		return 0, "", false
	}
	digits, after, ok := strings.Cut(rest, close)
	if !ok || digits == "" || len(digits) > 4 || strings.Trim(digits, "0123456789") != "" {
		return 0, "", false
	}
	n, err := strconv.Atoi(digits)
	return n, after, err == nil
}

type ToolRefusals struct {
	mu      sync.Mutex
	refused map[string]map[[sha256.Size]byte]bool
}

func NewToolRefusals() *ToolRefusals { return &ToolRefusals{} }

func (r *ToolRefusals) note(key string, digest [sha256.Size]byte) (known bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refused == nil {
		r.refused = map[string]map[[sha256.Size]byte]bool{}
	}
	if r.refused[key] == nil {
		r.refused[key] = map[[sha256.Size]byte]bool{}
	}
	known = r.refused[key][digest]
	r.refused[key][digest] = true
	return known
}

func (r *ToolRefusals) of(key string, fn func(map[[sha256.Size]byte]bool)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r.refused[key])
}

func toolDigest(t ToolDefinition) [sha256.Size]byte {
	raw, _ := json.Marshal(t)
	return sha256.Sum256(raw)
}

func (c *Client) refusedTool(err error, tools []ToolDefinition) error {
	var answered *HTTPError
	if !errors.As(err, &answered) {
		return err
	}
	i, ok := answered.refusedToolIndex(c.dialect)
	if !ok || i >= len(tools) {
		return err
	}
	name := tools[i].Function.Name
	if known := c.refusals.note(c.refusalKey, toolDigest(tools[i])); !known {
		logsink.Warn("llm.refusal", "%s %s would not accept the definition of the tool %q (HTTP %d) — it is left out of this provider's requests until its definition changes or this process restarts; the provider said: %s",
			providerLabel(c.dialect), c.model, name, answered.StatusCode, clip(answered.Body, 300))
	}
	return &ToolSchemaRefusedError{Tool: name, Err: err}
}

func (c *Client) WithheldTools(defs []ToolDefinition) (offered []ToolDefinition, withheld []string) {
	offered = defs
	c.refusals.of(c.refusalKey, func(refused map[[sha256.Size]byte]bool) {
		if len(refused) == 0 {
			return
		}
		offered = make([]ToolDefinition, 0, len(defs))
		for _, d := range defs {
			if refused[toolDigest(d)] {
				withheld = append(withheld, d.Function.Name)
				continue
			}
			offered = append(offered, d)
		}
	})
	return offered, withheld
}
