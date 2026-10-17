package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestGeminiKeyServesInference(t *testing.T) {
	if os.Getenv("AII_GEMINI_LIVE") != "1" {
		t.Skip("set AII_GEMINI_LIVE=1 to run (uses the operator's own Gemini key, read from GEMINI_API_KEY)")
	}
	shipped := shippedGemini(t)
	if os.Getenv(shipped.VendorKeyEnv) == "" {
		t.Fatalf("%s is not set: the live run reads the key from the variable the shipped entry names", shipped.VendorKeyEnv)
	}
	models := shipped.Models
	if only := os.Getenv("AII_GEMINI_LIVE_MODELS"); only != "" {
		models = strings.Split(only, ",")
	}

	// The model list, through the entry as shipped: the API's own path and
	// key header, and every model the catalog offers is one Google serves,
	// with the limits the catalog declares.
	t.Run("the catalog is what Google serves", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		entry := shipped
		entry.Speech = nil
		a := resolverApp(t.TempDir(), LLMConfig{})
		listed, meta, err := a.discoverMetaForEntry(ctx, entry, "")
		if err != nil {
			t.Fatalf("model list: %v", err)
		}
		for _, m := range shipped.Models {
			if !slices.Contains(listed, m) {
				t.Errorf("the catalog offers %q and Google's list of %d does not", m, len(listed))
				continue
			}
			if got := meta[m]; got.Context != shipped.ContextLength || got.MaxOut != shipped.MaxOutputTokens {
				t.Errorf("%s: Google says a window of %d and an output limit of %d; the catalog declares %d and %d", m, got.Context, got.MaxOut, shipped.ContextLength, shipped.MaxOutputTokens)
			}
		}
	})

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			client := func(t *testing.T, edit func(*providerEntry)) *llm.Client {
				t.Helper()
				dir := t.TempDir()
				entry := shipped
				entry.Speech, entry.DefaultModel, entry.Models, entry.Default = nil, model, []string{model}, true
				if edit != nil {
					edit(&entry)
				}
				writeTestProviders(t, dir, entry)
				a := New(&Config{LLM: LLMConfig{Provider: entry.Name, Model: model, TimeoutSeconds: 90}, SourcePath: filepath.Join(dir, "config.json")})
				cc, _, err := a.resolveLLM()
				if err != nil {
					t.Fatal(err)
				}
				return llm.New(&cc)
			}
			native := client(t, nil)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()

			if _, err := native.Chat(ctx, []llm.Message{{Role: "user", Content: "Say ok."}}, llm.ChatOptions{}); err != nil {
				var refusal *llm.HTTPError
				if errors.As(err, &refusal) && refusal.StatusCode == 429 {
					t.Skipf("this key has no quota for %s (429, not retryable): the model was not asked", model)
				}
				t.Fatalf("the first call: %v", err)
			}

			// A plain turn: the request shape, the stream, usage that adds
			// up with thinking counted (a usage Google reports that this
			// build calls inconsistent fails the call, and would fail every
			// call), and readable reasoning where the model gives any.
			t.Run("a plain turn, its usage and its reasoning", func(t *testing.T) {
				resp, err := native.Chat(ctx, []llm.Message{
					{Role: "system", Content: "You answer in one short sentence."},
					{Role: "user", Content: "A farmer has 17 sheep and all but 9 run away. How many are left? Think it through, then answer."},
				}, llm.ChatOptions{})
				if err != nil {
					t.Fatal(err)
				}
				choice := resp.Choices[0]
				if strings.TrimSpace(choice.Message.Content) == "" || choice.FinishReason != "stop" {
					t.Errorf("reply %q finished %q", choice.Message.Content, choice.FinishReason)
				}
				if !resp.Usage.Reported || resp.Usage.PromptTokens == 0 || resp.Usage.CompletionTokens == 0 {
					t.Errorf("usage: %+v", resp.Usage)
				}
				t.Logf("usage %+v; readable reasoning: %d characters", resp.Usage, len(choice.ReasoningAccount))
			})

			// The schema that uses the whole of the host's subset, and so
			// every keyword any plugin's schema can carry: Google takes
			// it. Then the turn's second step, which is where a Gemini 3
			// model's signature must come back exactly; then a turn after
			// it, with the first still in the conversation.
			tool := llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
				Name: "pl_org_example_probe_record", Description: "Record one probe. Call it with name set to tide_gauge and kind set to probe.", Parameters: wholeSubsetSchema(),
			}}
			twoSteps := func(t *testing.T, c *llm.Client, forget bool) {
				t.Helper()
				convo := []llm.Message{{Role: "user", Content: "Record a probe named tide_gauge now, then tell me in one sentence what the tool answered."}}
				for turn := range 2 {
					resp, err := c.Chat(ctx, convo, llm.ChatOptions{Tools: []llm.ToolDefinition{tool}, RequireTool: true})
					if err != nil {
						t.Fatalf("turn %d, the call: %v", turn+1, err)
					}
					msg := resp.Choices[0].Message
					if len(msg.ToolCalls) == 0 || msg.ToolCalls[0].Function.Name != tool.Function.Name {
						t.Fatalf("turn %d: no call to the tool: %+v", turn+1, resp.Choices[0])
					}
					var args map[string]any
					if err := json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args); err != nil {
						t.Fatalf("turn %d: arguments %q: %v", turn+1, msg.ToolCalls[0].Function.Arguments, err)
					}
					if forget {
						// As a call rebuilt from the record, or made by
						// another model: no parts of Gemini's, so no
						// signature but the one Google documents for this.
						msg.NativeContent, msg.NativeDialect = nil, ""
					}
					convo = append(convo, msg)
					for _, tc := range msg.ToolCalls {
						convo = append(convo, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: `{"recorded":true,"id":"p-7"}`})
					}
					// Words of the user's after the tool's answer, as a
					// note steered into a running turn arrives: they ride
					// in the same content as the answer.
					convo = append(convo, llm.Message{Role: "user", Content: "Keep it to one sentence."})
					resp, err = c.Chat(ctx, convo, llm.ChatOptions{Tools: []llm.ToolDefinition{tool}, DisableTools: true})
					if err != nil {
						t.Fatalf("turn %d, the step after the tool's answer: %v", turn+1, err)
					}
					if strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
						t.Fatalf("turn %d: nothing said after the tool's answer: %+v", turn+1, resp.Choices[0])
					}
					convo = append(convo, resp.Choices[0].Message, llm.Message{Role: "user", Content: "Record one more probe, named tide_gauge again, and tell me what the tool answered."})
				}
			}
			t.Run("the whole-subset schema, and a signed call returned", func(t *testing.T) { twoSteps(t, native, false) })
			t.Run("a call that carries no signature of Gemini's", func(t *testing.T) { twoSteps(t, native, true) })

			t.Run("an entry as an earlier release wrote it", func(t *testing.T) {
				path := writeProvidersJSON(t, strings.Replace(formerGeminiEntry, `"api_key":"typed-key",`, "", 1))
				a := New(&Config{LLM: LLMConfig{Provider: shipped.Name, Model: model, TimeoutSeconds: 90}, SourcePath: filepath.Join(filepath.Dir(path), "config.json")})
				cc, _, err := a.resolveLLM()
				if err != nil || cc.Provider != "gemini" {
					t.Fatalf("the former entry resolves to dialect %q: %v", cc.Provider, err)
				}
				twoSteps(t, llm.New(&cc), false)
			})

			t.Run("a pattern with a Unicode class", func(t *testing.T) {
				legacy := llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: "label_set", Description: "Set a label.",
					Parameters: map[string]any{"type": "object", "additionalProperties": false, "required": []any{"label"},
						"properties": map[string]any{"label": map[string]any{"type": "string", "pattern": `^[^\p{Cc}\p{Cf}\p{Zl}\p{Zp}]+$`}}}}}
				if _, err := native.Chat(ctx, []llm.Message{{Role: "user", Content: "Set the label to harbour."}}, llm.ChatOptions{Tools: []llm.ToolDefinition{legacy}}); err != nil {
					t.Errorf("a schema whose pattern uses a Unicode class: %v", err)
				}
			})

			// Google itself as the judge of what the schema rule claims:
			// every definition the app builds, in the requests that
			// carry them, and every input schema in the directories a
			// run names (AII_TOOL_SCHEMA_DIRS, as the schema test reads
			// it). A refused request names what Google would not take.
			t.Run("every definition the app builds", func(t *testing.T) {
				ask := []llm.Message{{Role: "user", Content: "Say ok."}}
				requests, _ := toolDefinitionsTheAppBuilds(t)
				for _, dir := range filepath.SplitList(os.Getenv("AII_TOOL_SCHEMA_DIRS")) {
					defs, _ := pluginInputSchemas(t, dir)
					requests = append(requests, defs)
				}
				sent := 0
				for _, defs := range requests {
					sent += len(defs)
					if _, err := native.Chat(ctx, ask, llm.ChatOptions{Tools: defs}); err != nil {
						t.Errorf("a request of %d definitions, the first %s: %v", len(defs), defs[0].Function.Name, err)
					}
				}
				t.Logf("%d definitions in %d requests", sent, len(requests))
			})

			t.Run("a definition Google refuses", func(t *testing.T) {
				ask := []llm.Message{{Role: "user", Content: "Say ok."}}
				with := func(name string, params map[string]any) llm.ChatOptions {
					return llm.ChatOptions{Tools: []llm.ToolDefinition{tool, {Type: "function", Function: llm.ToolFunction{Name: name, Description: "A tool.", Parameters: params}}}}
				}
				_, err := native.Chat(ctx, ask, with("9lives", map[string]any{"type": "object"}))
				var refused *llm.ToolSchemaRefusedError
				if !errors.As(err, &refused) || refused.Tool != "9lives" {
					t.Errorf("a name Google does not take was refused as: %v", err)
				}
				if _, err := native.Chat(ctx, ask, with("deep", schemaNestedTo(geminiSchemaDepth))); err != nil {
					t.Errorf("a schema nested %d deep was refused: %v; Google took it on 2026-10-13", geminiSchemaDepth, err)
				}
				for what, params := range map[string]map[string]any{
					"a schema nested one level past the bound": schemaNestedTo(geminiSchemaDepth + 1),
					"a required name with no property":         {"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []any{"x"}},
				} {
					_, err := native.Chat(ctx, ask, with("unplaced", params))
					var placed *llm.ToolSchemaRefusedError
					var answered *llm.HTTPError
					if errors.As(err, &placed) && placed.Tool == "unplaced" {
						t.Logf("%s: Google now says which declaration: %v", what, err)
					} else if !errors.As(err, &answered) || answered.StatusCode != 400 {
						t.Errorf("%s was answered: %v; Google refused it with a 400 on 2026-10-13", what, err)
					}
				}
			})

			// A key Google does not accept reads as the credential's
			// failure, on the wire as in the fixture.
			t.Run("a refused key is the credential's failure", func(t *testing.T) {
				bad := client(t, func(e *providerEntry) { e.APIKey = "not-a-key" })
				_, err := bad.Chat(ctx, []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{})
				var refusal *llm.HTTPError
				if !errors.As(err, &refusal) || !refusal.AuthFailure() {
					t.Errorf("a key that is no key was refused as: %v", err)
				}
			})
		})
	}

	// Embeddings, when a run names the model to ask (the shipped entry
	// names none): AII_GEMINI_LIVE_EMBEDDINGS=gemini-embedding-2.
	if model := os.Getenv("AII_GEMINI_LIVE_EMBEDDINGS"); model != "" {
		t.Run("embeddings from "+model, func(t *testing.T) {
			dir := t.TempDir()
			entry := shipped
			entry.Speech, entry.Default = nil, true
			writeTestProviders(t, dir, entry)
			cc, _, err := New(&Config{LLM: LLMConfig{Provider: entry.Name}, SourcePath: filepath.Join(dir, "config.json")}).resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			vectors, err := llm.New(&cc).Embed(ctx, model, []string{"the tide gauge", "a harbour wall"})
			if err != nil || len(vectors) != 2 || len(vectors[0]) == 0 || len(vectors[0]) != len(vectors[1]) {
				t.Fatalf("%d vector(s), %v", len(vectors), err)
			}
		})
	}
}
