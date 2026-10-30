package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
)

var modelsDevProvider = map[string]string{
	"Claude (Max/Pro)":     "anthropic",
	"Anthropic":            "anthropic",
	"ChatGPT (Plus/Pro)":   "openai",
	"OpenAI":               "openai",
	"zAI":                  "zai",
	"Moonshot (Kimi)":      "moonshotai",
	"Qwen (Alibaba Cloud)": "alibaba",
	"Google Gemini":        "google",
	"DeepSeek":             "deepseek",
	"Groq":                 "groq",
	"OpenRouter":           "openrouter",
}

var (
	rowInputKinds  = []string{"image", "pdf", "audio", "video"}
	rowOutputKinds = []string{"text", "image", "audio", "video"}
)

type modelsDevModel struct {
	ToolCall   *bool `json:"tool_call"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

var asOfDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func runCapabilities(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	fs.SetOutput(stderr)
	modelsDev := fs.String("models-dev", "", "models.dev api.json, downloaded by the release owner (required)")
	asOf := fs.String("as-of", "", "the date it was downloaded, YYYY-MM-DD (required)")
	providers := fs.String("providers", "config/providers.json", "the shipped registry, rewritten in place")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *modelsDev == "" || !asOfDate.MatchString(*asOf) {
		fs.Usage()
		return 2
	}
	catalogue, err := os.ReadFile(*modelsDev)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	registry, err := os.ReadFile(*providers)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	out, err := generateCapabilities(registry, catalogue, *asOf, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*providers, out, 0o644); err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	return 0
}

func generateCapabilities(registry, catalogue []byte, asOf string, report io.Writer) ([]byte, error) {
	var md map[string]struct {
		Models map[string]modelsDevModel `json:"models"`
	}
	if err := json.Unmarshal(catalogue, &md); err != nil {
		return nil, fmt.Errorf("models.dev api.json: %w", err)
	}
	var reg struct {
		Providers []struct {
			Name   string   `json:"name"`
			Models []string `json:"models"`
		} `json:"providers"`
		ModelCapabilities json.RawMessage `json:"model_capabilities"`
	}
	if err := json.Unmarshal(registry, &reg); err != nil {
		return nil, fmt.Errorf("providers.json: %w", err)
	}
	rows, err := decodeOrdered(reg.ModelCapabilities)
	if err != nil {
		return nil, fmt.Errorf("providers.json model_capabilities: %w", err)
	}
	from := map[string]string{}
	for _, entry := range reg.Providers {
		provider, generated := modelsDevProvider[entry.Name]
		if !generated {
			continue
		}
		for _, model := range entry.Models {
			if earlier, done := from[model]; done {
				if earlier != provider {
					fmt.Fprintf(report, "%s: seeded by %s too; read from models.dev (%s) only\n", model, entry.Name, earlier)
				}
				continue
			}
			m, listed := md[provider].Models[model]
			if !listed {
				fmt.Fprintf(report, "%s: uncatalogued: models.dev (%s) does not list it\n", model, provider)
				from[model] = provider
				continue
			}
			from[model] = provider
			row, err := rows.object(model)
			if err != nil {
				return nil, fmt.Errorf("model_capabilities[%q]: %w", model, err)
			}
			if err := fillRow(row, model, provider, asOf, m, report); err != nil {
				return nil, err
			}
		}
	}
	rendered, err := rows.render()
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, rendered, "  ", "  "); err != nil {
		return nil, err
	}
	return replaceTopLevel(registry, "model_capabilities", indented.Bytes())
}

func fillRow(row *orderedObject, model, provider, asOf string, m modelsDevModel, report io.Writer) error {
	filled := false
	for _, side := range []struct {
		name  string
		kinds []string
		said  []string
	}{{"input", rowInputKinds, m.Modalities.Input}, {"output", rowOutputKinds, m.Modalities.Output}} {
		if len(side.said) == 0 {
			continue
		}
		have, err := row.object(side.name)
		if err != nil {
			return fmt.Errorf("model_capabilities[%q].%s: %w", model, side.name, err)
		}
		if side.name == "input" {

			if v, ok := row.get("images"); ok && !have.has("image") {
				have.set("image", v)
				row.remove("images")
			}
		}
		for _, kind := range side.kinds {
			want := slices.Contains(side.said, kind)
			if f, err := filledBool(have, kind, want, model+" "+side.name+"."+kind, report); err != nil {
				return err
			} else {
				filled = filled || f
			}
		}
	}
	if m.ToolCall != nil {
		f, err := filledBool(row, "tools", *m.ToolCall, model+" tools", report)
		if err != nil {
			return err
		}
		filled = filled || f
	}
	for _, limit := range []struct {
		key  string
		want int
	}{{"context_length", m.Limit.Context}, {"max_output_tokens", m.Limit.Output}} {
		if limit.want <= 0 {
			continue
		}
		raw, ok := row.get(limit.key)
		if !ok {
			row.set(limit.key, json.RawMessage(fmt.Sprint(limit.want)))
			filled = true
			continue
		}
		var have int
		if err := json.Unmarshal(raw, &have); err != nil {
			return fmt.Errorf("model_capabilities[%q].%s: %w", model, limit.key, err)
		}
		if have != limit.want {
			fmt.Fprintf(report, "%s %s: the row says %d, models.dev (%s) says %d; kept\n", model, limit.key, have, provider, limit.want)
		}
	}
	source := "models.dev (" + provider + ")"
	if raw, ok := row.get("source"); ok {
		var have string
		if err := json.Unmarshal(raw, &have); err != nil {
			return fmt.Errorf("model_capabilities[%q].source: %w", model, err)
		}
		if have != source {
			fmt.Fprintf(report, "%s source: the row says %q; kept\n", model, have)
		}
	} else {
		row.set("source", mustJSON(source))
	}
	row.set("as_of", mustJSON(asOf))
	if filled {
		fmt.Fprintf(report, "%s: filled from models.dev (%s)\n", model, provider)
	}
	return nil
}

func filledBool(obj *orderedObject, key string, want bool, what string, report io.Writer) (bool, error) {
	raw, ok := obj.get(key)
	if !ok {
		obj.set(key, mustJSON(want))
		return true, nil
	}
	var have bool
	if err := json.Unmarshal(raw, &have); err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	if have != want {
		fmt.Fprintf(report, "%s: the row says %t, models.dev says %t; kept\n", what, have, want)
	}
	return false, nil
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

type orderedObject struct {
	keys   []string
	values map[string]json.RawMessage
	opened map[string]*orderedObject
}

func decodeOrdered(raw json.RawMessage) (*orderedObject, error) {
	o := &orderedObject{values: map[string]json.RawMessage{}, opened: map[string]*orderedObject{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if _, dup := o.values[key]; !dup {
			o.keys = append(o.keys, key)
		}
		o.values[key] = value
	}
	return o, nil
}

func (o *orderedObject) has(key string) bool {
	_, ok := o.values[key]
	return ok
}

func (o *orderedObject) get(key string) (json.RawMessage, bool) {
	if sub, ok := o.opened[key]; ok {
		raw, err := sub.render()
		return raw, err == nil
	}
	v, ok := o.values[key]
	return v, ok
}

func (o *orderedObject) set(key string, value json.RawMessage) {
	if !o.has(key) {
		o.keys = append(o.keys, key)
	}
	delete(o.opened, key)
	o.values[key] = value
}

func (o *orderedObject) remove(key string) {
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
	delete(o.values, key)
	delete(o.opened, key)
}

func (o *orderedObject) object(key string) (*orderedObject, error) {
	if sub, ok := o.opened[key]; ok {
		return sub, nil
	}
	sub, err := decodeOrdered(o.values[key])
	if err != nil {
		return nil, err
	}
	if !o.has(key) {
		o.keys = append(o.keys, key)
		o.values[key] = nil
	}
	o.opened[key] = sub
	return sub, nil
}

func (o *orderedObject) render() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, key := range o.keys {
		value := o.values[key]
		if sub, ok := o.opened[key]; ok {
			if len(sub.keys) == 0 {
				continue
			}
			var err error
			if value, err = sub.render(); err != nil {
				return nil, err
			}
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(mustJSON(key))
		b.WriteByte(':')
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		b.Write(compact.Bytes())
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func replaceTopLevel(data []byte, key string, value []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("providers.json is not an object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var skipped json.RawMessage
		if name, _ := tok.(string); name != key {
			if err := dec.Decode(&skipped); err != nil {
				return nil, err
			}
			continue
		}

		start := int(dec.InputOffset())
		for start < len(data) && (data[start] == ':' || data[start] == ' ' || data[start] == '\t' || data[start] == '\n' || data[start] == '\r') {
			start++
		}
		if err := dec.Decode(&skipped); err != nil {
			return nil, err
		}
		end := int(dec.InputOffset())
		return slices.Concat(data[:start], value, data[end:]), nil
	}
	return nil, fmt.Errorf("providers.json has no %s", key)
}
