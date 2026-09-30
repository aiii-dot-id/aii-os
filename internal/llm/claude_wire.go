package llm

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/cespare/xxhash/v2"
	"github.com/google/uuid"
)

type ClaudeCodeProfile struct {
	Enabled              bool              `json:"enabled"`
	RequestPath          string            `json:"request_path"`
	Stream               bool              `json:"stream"`
	EntryPoint           string            `json:"entrypoint"`
	BillingTemplate      string            `json:"billing_template"`
	Headers              map[string]string `json:"headers"`
	OSNames              map[string]string `json:"os_names"`
	ArchNames            map[string]string `json:"arch_names"`
	FingerprintSalt      string            `json:"fingerprint_salt"`
	FingerprintPositions []int             `json:"fingerprint_positions"`
	FingerprintLength    int               `json:"fingerprint_length"`
	Checksum             ClaudeChecksum    `json:"checksum"`
	AccountUUID          string            `json:"account_uuid"`
	DeviceID             string            `json:"device_id"`
	Metadata             bool              `json:"metadata"`
	Diagnostics          bool              `json:"diagnostics"`
	EmptyTools           bool              `json:"empty_tools"`
	ThinkingDisplay      string            `json:"thinking_display"`
	ContextManagement    json.RawMessage   `json:"context_management"`
	OutputConfig         *anthOutputConfig `json:"output_config"`

	Version string `json:"-"`
}

type ClaudeChecksum struct {
	Seed               string   `json:"seed"`
	Marker             string   `json:"marker"`
	HexLength          int      `json:"hex_length"`
	EmptyStringKeys    []string `json:"empty_string_keys"`
	OmitKeys           []string `json:"omit_keys"`
	TrailingGroupComma bool     `json:"trailing_group_comma"`
}

func (p *ClaudeCodeProfile) Validate() error {
	if p == nil || !p.Enabled {
		return nil
	}
	u, err := url.Parse(p.RequestPath)
	if err != nil || !strings.HasPrefix(p.RequestPath, "/") || strings.HasPrefix(p.RequestPath, "//") || u.Host != "" || u.Fragment != "" {
		return fmt.Errorf("Claude request_path must be an absolute path on the configured provider")
	}
	if _, err := strconv.ParseUint(p.Checksum.Seed, 0, 64); err != nil {
		return fmt.Errorf("Claude checksum seed: %w", err)
	}
	if p.Checksum.HexLength < 1 || p.Checksum.HexLength > 16 || p.FingerprintLength < 1 || p.FingerprintLength > 64 {
		return fmt.Errorf("Claude hash lengths outside supported range")
	}
	if p.Checksum.Marker == "" || p.Version == "" || !strings.Contains(p.BillingTemplate, "{checksum}") {
		return fmt.Errorf("Claude profile requires version, checksum marker and billing checksum placeholder")
	}
	for _, i := range p.FingerprintPositions {
		if i < 0 {
			return fmt.Errorf("negative Claude fingerprint position")
		}
	}
	if p.DeviceID != "" {
		b, e := hex.DecodeString(p.DeviceID)
		if e != nil || len(b) != 32 {
			return fmt.Errorf("Claude device_id must be 64 hexadecimal characters")
		}
	}
	if p.AccountUUID != "" {
		if _, err := uuid.Parse(p.AccountUUID); err != nil {
			return fmt.Errorf("Claude account_uuid: %w", err)
		}
	}
	if len(p.ContextManagement) > 0 && string(p.ContextManagement) != "null" {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(p.ContextManagement, &object); err != nil || object == nil {
			return fmt.Errorf("Claude context_management must be an object")
		}
	}
	for key := range p.Headers {
		switch strings.ToLower(key) {
		case "authorization", "x-api-key", "host", "content-length", "transfer-encoding", "content-type":
			return fmt.Errorf("Claude profile cannot override protected header %q", key)
		}
	}
	return nil
}

func cloneClaudeProfile(p *ClaudeCodeProfile) *ClaudeCodeProfile {
	if p == nil || !p.Enabled {
		return nil
	}
	q := *p
	q.Headers = maps.Clone(p.Headers)
	q.OSNames = maps.Clone(p.OSNames)
	q.ArchNames = maps.Clone(p.ArchNames)
	q.FingerprintPositions = slices.Clone(p.FingerprintPositions)
	q.Checksum.EmptyStringKeys = slices.Clone(p.Checksum.EmptyStringKeys)
	q.Checksum.OmitKeys = slices.Clone(p.Checksum.OmitKeys)
	q.ContextManagement = bytes.Clone(p.ContextManagement)
	if p.OutputConfig != nil {
		v := *p.OutputConfig
		q.OutputConfig = &v
	}
	return &q
}

type anthMetadata struct {
	UserID string `json:"user_id"`
}

func claudeFingerprint(text string, p *ClaudeCodeProfile) string {
	units := utf16.Encode([]rune(text))
	sample := make([]uint16, 0, len(p.FingerprintPositions))
	for _, i := range p.FingerprintPositions {
		if i < len(units) {
			sample = append(sample, units[i])
		} else {
			sample = append(sample, '0')
		}
	}
	sum := sha256.Sum256([]byte(p.FingerprintSalt + string(utf16.Decode(sample)) + p.Version))
	return hex.EncodeToString(sum[:])[:p.FingerprintLength]
}

func (c *Client) claudeBilling(messages []Message, promptID string) string {
	text := ""
	for _, m := range messages {
		if m.Role == "user" {
			text = m.Content
			break
		}
	}
	if len(messages) == 1 && messages[0].Role == "system" {
		text = messages[0].Content
	}
	p := c.claudeCode
	return strings.NewReplacer("{client_version}", p.Version, "{fingerprint}", claudeFingerprint(text, p), "{entrypoint}", p.EntryPoint, "{checksum}", strings.Repeat("0", p.Checksum.HexLength), "{prompt_id}", promptID).Replace(p.BillingTemplate)
}

func (c *Client) claudeHeaders(promptID, requestID string, attempt int) map[string]string {
	p := c.claudeCode
	osName := runtime.GOOS
	arch := runtime.GOARCH
	if v, ok := p.OSNames[osName]; ok {
		osName = v
	}
	if v, ok := p.ArchNames[arch]; ok {
		arch = v
	}
	r := strings.NewReplacer("{client_version}", p.Version, "{entrypoint}", p.EntryPoint, "{session_id}", c.claudeSession, "{prompt_id}", promptID, "{request_id}", requestID, "{retry_count}", strconv.Itoa(attempt), "{timeout_seconds}", strconv.FormatFloat(c.idle.Seconds(), 'f', -1, 64), "{os}", osName, "{arch}", arch)
	h := make(map[string]string, len(p.Headers))
	for k, v := range p.Headers {
		h[k] = r.Replace(v)
	}
	return h
}

func (c *Client) prepareClaudeRequest(req *anthRequest, messages []Message) (string, error) {
	if err := c.claudeCode.Validate(); err != nil {
		return "", err
	}
	c.claudeOnce.Do(func() {
		id, err := uuid.NewRandom()
		if err != nil {
			c.claudeInitErr = err
			return
		}
		c.claudeSession = id.String()
		c.claudeDevice = c.claudeCode.DeviceID
		if c.claudeDevice == "" {
			var b [32]byte
			if _, err := rand.Read(b[:]); err != nil {
				c.claudeInitErr = err
				return
			}
			c.claudeDevice = hex.EncodeToString(b[:])
		}
	})
	if c.claudeInitErr != nil {
		return "", fmt.Errorf("Claude request identity: %w", c.claudeInitErr)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("Claude prompt id: %w", err)
	}
	promptID := id.String()
	billing := anthContent{Type: "text", Text: c.claudeBilling(messages, promptID)}
	if c.oauthBillingText != "" && len(req.System) > 0 {
		req.System[0] = billing
	} else {
		req.System = append([]anthContent{billing}, req.System...)
	}
	metadata, _ := json.Marshal(struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}{c.claudeDevice, c.claudeCode.AccountUUID, c.claudeSession})
	if c.claudeCode.Metadata {
		req.Metadata = &anthMetadata{UserID: string(metadata)}
	}
	req.Stream = c.claudeCode.Stream
	if req.Diagnostics == nil && c.claudeCode.Diagnostics {
		req.Diagnostics = &anthDiagnostics{}
	}
	if req.Thinking != nil {
		if req.Thinking.Display == "" && c.thinkingDisplay == "" {
			req.Thinking.Display = c.claudeCode.ThinkingDisplay
		}
		req.ContextManagement = c.claudeCode.ContextManagement
	}
	if req.OutputConfig == nil && c.reasoningEffort == "" && c.claudeCode.OutputConfig != nil && slices.Contains(c.effortLevels, c.claudeCode.OutputConfig.Effort) {
		req.OutputConfig = c.claudeCode.OutputConfig
	}
	return promptID, nil
}

func marshalAnthropic(req anthRequest, profile *ClaudeCodeProfile, credential bool) ([]byte, error) {
	if profile == nil || !credential {
		return json.Marshal(req)
	}

	tools := req.Tools
	if tools == nil {
		tools = []anthTool{}
	}
	var body []byte
	var err error
	if !profile.EmptyTools {
		body, err = json.Marshal(req)
	} else {
		body, err = json.Marshal(struct {
			anthRequest
			Tools []anthTool `json:"tools"`
		}{req, tools})
	}
	if err != nil {
		return nil, err
	}
	return signClaudeBody(body, profile.Checksum)
}

func signClaudeBody(body []byte, policy ClaudeChecksum) ([]byte, error) {

	system, systemOffset, err := claudeJSONMember(body, "system")
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(system))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("Claude system must be an array")
	}
	var first json.RawMessage
	if err := d.Decode(&first); err != nil {
		return nil, fmt.Errorf("Claude first system block: %w", err)
	}
	firstOffset := systemOffset + int(d.InputOffset()) - len(first)
	encoded, textOffset, err := claudeJSONMember(first, "text")
	if err != nil {
		return nil, err
	}
	offset := firstOffset + textOffset
	digits := bytes.Index(encoded, []byte(policy.Marker+strings.Repeat("0", policy.HexLength)))
	if offset < 0 || digits < 0 {
		return nil, fmt.Errorf("Claude billing checksum placeholder missing")
	}
	normalized, err := normalizeClaudeHash(body, policy)
	if err != nil {
		return nil, err
	}
	seed, err := strconv.ParseUint(policy.Seed, 0, 64)
	if err != nil {
		return nil, err
	}
	h := xxhash.NewWithSeed(seed)
	_, _ = h.Write(normalized)
	result := bytes.Clone(body)
	hash := fmt.Sprintf("%016x", h.Sum64())
	copy(result[offset+digits+len(policy.Marker):], hash[len(hash)-policy.HexLength:])
	return result, nil
}

func claudeJSONMember(raw []byte, name string) (json.RawMessage, int, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, 0, fmt.Errorf("Claude JSON member %q requires an object", name)
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, 0, err
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, 0, err
		}
		if key == name {
			return value, int(d.InputOffset()) - len(value), nil
		}
	}
	return nil, 0, fmt.Errorf("Claude JSON member %q missing", name)
}

func normalizeClaudeHash(raw []byte, policy ClaudeChecksum) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return bytes.Clone(raw), nil
	}
	if delim != '{' && delim != '[' {
		return nil, fmt.Errorf("invalid Claude hash JSON")
	}
	out := []byte{byte(delim)}
	kept := 0
	trailing := 0
	for d.More() {
		var key string
		var encodedKey []byte
		if delim == '{' {
			start := int(d.InputOffset())
			t, e := d.Token()
			if e != nil {
				return nil, e
			}
			key = t.(string)
			encodedKey = bytes.TrimLeft(raw[start:int(d.InputOffset())], ", \n\t\r")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		keyLiteral, _ := json.Marshal(key)
		literalKey := bytes.Equal(keyLiteral, encodedKey)
		excluded := delim == '{' && literalKey && slices.Contains(policy.OmitKeys, key)
		if excluded {
			trailing++
			continue
		}
		trailing = 0
		if literalKey && slices.Contains(policy.EmptyStringKeys, key) && len(value) > 0 && value[0] == '"' {
			value = json.RawMessage(`""`)
		} else {
			value, err = normalizeClaudeHash(value, policy)
			if err != nil {
				return nil, err
			}
		}
		if kept > 0 {
			out = append(out, ',')
		}
		kept++
		if delim == '{' {
			out = append(out, encodedKey...)
			out = append(out, ':')
		}
		out = append(out, value...)
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if policy.TrailingGroupComma && trailing > 1 && kept > 0 {
		out = append(out, ',')
	}
	if delim == '{' {
		out = append(out, '}')
	} else {
		out = append(out, ']')
	}
	return out, nil
}
