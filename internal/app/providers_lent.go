package app

import (
	"bytes"
	"cmp"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

var choiceFields = []string{"name", "api_type", "url", "credential", "local", "chat", "default", "default_model", "api_key"}

var lentByRule = []string{"catalogue_author", "effort_levels", "capability_list", "capability_catalogue", "vendor_key_env", "formerly", "credential_options", "oauth", "speech"}

var lentFactFields = []string{
	"subscribe_url", "context_length", "max_output_tokens", "models", "cache", "claude_code",
	"credential_option_formats", "embeddings_model", "reasoning_effort", "thinking_budget",
	"thinking_mode", "thinking_display", "replay_reasoning", "temperature", "top_p", "extra",
	"api_key_env",
}

var entryFieldIndex = func() map[string]int {
	t := reflect.TypeOf(providerEntry{})
	out := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			out[name] = i
		}
	}
	return out
}()

func entryField(e *providerEntry, name string) reflect.Value {
	return reflect.ValueOf(e).Elem().Field(entryFieldIndex[name])
}

func fillEmbeddedFacts(reg *providerRegistry) {
	for i := range reg.Providers {
		e := &reg.Providers[i]
		shipped := entryNamed(embeddedRegistry(), e.Name)
		if shipped == nil || !atShippedAddress(*e, *shipped) {
			continue
		}
		for _, name := range lentFactFields {
			own, theirs := entryField(e, name), entryField(shipped, name)
			if !own.IsZero() || theirs.IsZero() {
				continue
			}
			raw, err := json.Marshal(theirs.Interface())
			if err != nil || json.Unmarshal(raw, own.Addr().Interface()) != nil {
				own.SetZero()
				continue
			}
			l := reg.lentTo(e.Name)
			if l.facts == nil {
				l.facts = map[string]json.RawMessage{}
			}
			l.facts[name] = raw
		}
	}
}

func atShippedAddress(e, shipped providerEntry) bool {
	dialect := func(t string) string {
		if t == "responses" {
			return "openai"
		}
		return cmp.Or(t, "openai")
	}
	return dialect(e.APIType) == dialect(shipped.APIType) && strings.TrimRight(e.URL, "/") == strings.TrimRight(shipped.URL, "/")
}

func takeBackFacts(e *providerEntry, l *lentFacts) {
	for name, lent := range l.facts {
		field := entryField(e, name)
		if raw, err := json.Marshal(field.Interface()); err == nil && bytes.Equal(raw, lent) {
			field.SetZero()
		}
	}
}

func entryChoices(e providerEntry) providerEntry {
	var out providerEntry
	for _, name := range choiceFields {
		if raw, err := json.Marshal(entryField(&e, name).Interface()); err == nil {
			_ = json.Unmarshal(raw, entryField(&out, name).Addr().Interface())
		}
	}
	return out
}

func isChoiceField(name string) bool { return slices.Contains(choiceFields, name) }
