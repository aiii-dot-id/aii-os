package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func readAnthropicStream(r io.Reader) (anthResponse, error) {
	var result anthResponse
	message := map[string]json.RawMessage{}
	usage := map[string]json.RawMessage{}
	var blocks []json.RawMessage
	var block map[string]json.RawMessage
	var text, thinking, signature, input strings.Builder
	sawText, sawThinking, sawSignature, sawInput := false, false, false, false
	started, stopped := false, false
	event := func(data []byte) error {
		var e struct {
			Type    string                     `json:"type"`
			Index   *int                       `json:"index"`
			Message map[string]json.RawMessage `json:"message"`
			Block   map[string]json.RawMessage `json:"content_block"`
			Delta   map[string]json.RawMessage `json:"delta"`
			Usage   map[string]json.RawMessage `json:"usage"`
			Error   json.RawMessage            `json:"error"`
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("invalid stream event: %w", err)
		}
		if stopped {
			return fmt.Errorf("event after message_stop")
		}
		if e.Type == "ping" {
			return nil
		}
		if e.Type == "error" {
			return fmt.Errorf("Anthropic stream error: %s", e.Error)
		}
		if e.Type != "message_start" && !started {
			return fmt.Errorf("%s before message_start", e.Type)
		}
		switch e.Type {
		case "message_start":
			if started || e.Message == nil {
				return fmt.Errorf("invalid or duplicate message_start")
			}
			started = true
			message = e.Message
			if v := message["usage"]; len(v) > 0 && string(v) != "null" {
				if err := json.Unmarshal(v, &usage); err != nil {
					return err
				}
			}
			var initial []json.RawMessage
			if v := message["content"]; len(v) > 0 {
				if err := json.Unmarshal(v, &initial); err != nil {
					return err
				}
			}
			if len(initial) > 0 {
				return fmt.Errorf("message_start contains nonempty content")
			}
		case "content_block_start":
			if block != nil || e.Index == nil || *e.Index != len(blocks) || e.Block == nil {
				return fmt.Errorf("invalid content_block_start index/state")
			}
			block = e.Block
			text.Reset()
			thinking.Reset()
			signature.Reset()
			input.Reset()
			sawText = false
			sawThinking = false
			sawSignature = false
			sawInput = false
			for key, b := range map[string]*strings.Builder{"text": &text, "thinking": &thinking, "signature": &signature} {
				if raw, ok := block[key]; ok {
					var s string
					if err := json.Unmarshal(raw, &s); err != nil {
						return err
					}
					b.WriteString(s)
				}
			}
		case "content_block_delta":
			if block == nil || e.Index == nil || *e.Index != len(blocks) {
				return fmt.Errorf("invalid content_block_delta index/state")
			}
			var kind string
			if err := json.Unmarshal(e.Delta["type"], &kind); err != nil {
				return err
			}
			appendString := func(key string, b *strings.Builder) error {
				var s string
				if err := json.Unmarshal(e.Delta[key], &s); err != nil {
					return err
				}
				b.WriteString(s)
				return nil
			}
			switch kind {
			case "text_delta":
				sawText = true
				return appendString("text", &text)
			case "thinking_delta":
				sawThinking = true
				return appendString("thinking", &thinking)
			case "signature_delta":
				sawSignature = true
				return appendString("signature", &signature)
			case "input_json_delta":
				sawInput = true
				return appendString("partial_json", &input)
			case "citations_delta":
				var citations []json.RawMessage
				if raw := block["citations"]; len(raw) > 0 {
					if err := json.Unmarshal(raw, &citations); err != nil {
						return err
					}
				}
				citation := e.Delta["citation"]
				if len(citation) == 0 {
					return fmt.Errorf("citation delta without citation")
				}
				citations = append(citations, citation)
				block["citations"], _ = json.Marshal(citations)
			default:
				return fmt.Errorf("unsupported Anthropic stream delta %q", kind)
			}
		case "content_block_stop":
			if block == nil || e.Index == nil || *e.Index != len(blocks) {
				return fmt.Errorf("invalid content_block_stop index/state")
			}
			if sawText {
				block["text"], _ = json.Marshal(text.String())
			}
			if sawThinking {
				block["thinking"], _ = json.Marshal(thinking.String())
			}
			if sawSignature {
				block["signature"], _ = json.Marshal(signature.String())
			}
			if sawInput && input.Len() > 0 {
				raw := []byte(input.String())
				if !json.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
					return fmt.Errorf("invalid streamed tool input")
				}
				block["input"] = raw
			}
			raw, err := json.Marshal(block)
			if err != nil {
				return err
			}
			blocks = append(blocks, raw)
			block = nil
		case "message_delta":
			if block != nil {
				return fmt.Errorf("message_delta before content_block_stop")
			}
			for k, v := range e.Delta {
				message[k] = v
			}
			for k, v := range e.Usage {
				usage[k] = v
			}
		case "message_stop":
			if block != nil {
				return fmt.Errorf("message_stop before content_block_stop")
			}
			stopped = true
		default:
			return fmt.Errorf("unsupported Anthropic stream event %q", e.Type)
		}
		return nil
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	var data []byte
	total := 0
	dispatch := func() error {
		if len(data) == 0 {
			return nil
		}
		err := event(data)
		data = nil
		return err
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		total += len(line) + 1
		if total > maxResponseBytes {
			return result, fmt.Errorf("Anthropic stream exceeds %d bytes", maxResponseBytes)
		}
		if len(line) == 0 {
			if err := dispatch(); err != nil {
				return result, err
			}
			if stopped {
				break
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			v := bytes.TrimPrefix(line, []byte("data:"))
			v = bytes.TrimPrefix(v, []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, v...)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if err := dispatch(); err != nil {
		return result, err
	}
	if !started || !stopped {
		return result, fmt.Errorf("incomplete Anthropic stream: message_stop missing")
	}
	var stop string
	if err := json.Unmarshal(message["stop_reason"], &stop); err != nil || stop == "" {
		return result, fmt.Errorf("Anthropic stream has no terminal stop_reason")
	}
	message["content"], _ = json.Marshal(blocks)
	if len(usage) > 0 {
		message["usage"], _ = json.Marshal(usage)
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}
