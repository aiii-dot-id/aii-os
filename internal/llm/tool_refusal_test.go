package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAProvidersRefusalOfOneToolsDefinitionNamesTheTool(t *testing.T) {
	const openAI = `{"error":{"message":"Invalid schema for function 'pl_id_aiii_voice_vocabulary_correct': '^[^\\p{Cc}\\p{Cf}\\p{Zl}\\p{Zp}]*$' is not a 'regex'.","type":"invalid_request_error","param":"tools[4].parameters","code":"invalid_function_parameters"}}`
	const anthropic = `{"type":"error","error":{"type":"invalid_request_error","message":"tools.4.custom.input_schema: JSON schema is invalid. It must match JSON Schema draft 2020-12"}}`
	tools := make([]ToolDefinition, 6)
	for i, name := range []string{"shell", "read", "write", "recall", "pl_id_aiii_voice_vocabulary_correct", "work"} {
		tools[i] = ToolDefinition{Type: "function", Function: ToolFunction{Name: name, Parameters: map[string]any{"type": "object"}}}
	}

	const gemini = `{"error":{"code":400,"message":"* GenerateContentRequest.tools[0].function_declarations[4].name: Invalid function name. Must start with a letter or an underscore. Must be alphameric (a-z, A-Z, 0-9), underscores (_), dots (.), colons (:), or dashes (-), with a maximum length of 128.\n","status":"INVALID_ARGUMENT"}}`
	for _, c := range []struct{ provider, body string }{{"openai", openAI}, {"chatgpt", openAI}, {"anthropic", anthropic}, {"gemini", gemini}} {
		t.Run(c.provider, func(t *testing.T) {
			var asked atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(c.body))
			}))
			t.Cleanup(srv.Close)
			client := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "fixture", Provider: c.provider, Retries: -1, TimeoutSeconds: 5})
			_, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "go"}}, ChatOptions{Tools: tools})
			// ONE REQUEST LEARNS IT. The chat-completions dialect asks a
			// refused request again without the fields a stream adds, twice;
			// a refusal that names a tool is not one of those, and asked
			// again it cost three requests for every tool left out.
			if n := asked.Load(); n != 1 {
				t.Fatalf("THE REFUSAL OF ONE TOOL'S DEFINITION WAS ASKED FOR %d TIMES; the first answer names the tool", n)
			}
			var refused *ToolSchemaRefusedError
			var answered *HTTPError
			if !errors.As(err, &refused) || refused.Tool != "pl_id_aiii_voice_vocabulary_correct" || !errors.As(err, &answered) || answered.StatusCode != http.StatusBadRequest {
				t.Fatalf("want the typed refusal naming the fifth tool, the provider's 400 beneath it: %v", err)
			}
			offered, withheld := client.WithheldTools(tools)
			if len(offered) != 5 || len(withheld) != 1 || withheld[0] != refused.Tool {
				t.Fatalf("the client offers the other five and withholds the one refused: offered %d, withheld %v", len(offered), withheld)
			}
			// The same name with another definition is another definition.
			changed := append([]ToolDefinition(nil), tools...)
			changed[4].Function.Description = "fixed"
			if offered, withheld := client.WithheldTools(changed); len(offered) != 6 || len(withheld) != 0 {
				t.Fatalf("a changed definition is offered again: offered %d, withheld %v", len(offered), withheld)
			}
			// And another client, which is another provider entry or model, knows nothing of it.
			other := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "another", Provider: c.provider, Retries: -1})
			if offered, withheld := other.WithheldTools(tools); len(offered) != 6 || len(withheld) != 0 {
				t.Fatalf("a refusal by one provider's model was held against another's: withheld %v", withheld)
			}
		})
	}
}

const (
	googleRefusedName   = `{"error":{"code":400,"message":"* GenerateContentRequest.tools[0].function_declarations[1].name: Invalid function name. Must start with a letter or an underscore. Must be alphameric (a-z, A-Z, 0-9), underscores (_), dots (.), colons (:), or dashes (-), with a maximum length of 128.\n","status":"INVALID_ARGUMENT"}}`
	googleRefusedMember = `{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"additionalProperties\" at 'tools[0].function_declarations[1].parameters': Cannot find field.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"tools[0].function_declarations[1].parameters","description":"Invalid JSON payload received. Unknown name \"additionalProperties\" at 'tools[0].function_declarations[1].parameters': Cannot find field."}]}]}}`
	googleRefusedSchema = `{"error":{"code":400,"message":"unrecognized type 'float' at properties.n","status":"INVALID_ARGUMENT"}}`
	googleRefusedNeeds  = `{"error":{"code":400,"message":"schema at top-level requires unspecified property 'x'","status":"INVALID_ARGUMENT"}}`
	googleRefusedDepth  = `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT"}}`
)

func TestOnlyADefinitionRefusedInTheDialectsOwnShapeNamesATool(t *testing.T) {
	openAI := func(code, param, message string) string {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": code, "param": param, "message": message}})
		return string(b)
	}
	messages := func(kind, message string) string {
		b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": message}})
		return string(b)
	}
	for _, c := range []struct {
		what    string
		dialect Dialect
		status  int
		body    string
		want    int // the place named, or -1 for none
	}{
		{"openai: the parameters of the fifth", DialectOpenAI, 400, openAI("invalid_function_parameters", "tools[4].parameters", "Invalid schema"), 4},
		{"openai: the fifth, no field", DialectOpenAI, 400, openAI("invalid_function_parameters", "tools[4]", "Invalid schema"), 4},
		{"chatgpt: the same shape", DialectResponses, 400, openAI("invalid_function_parameters", "tools[0].parameters", "Invalid schema"), 0},
		{"openai: no param", DialectOpenAI, 400, openAI("invalid_function_parameters", "", "Invalid schema for function 'x'"), -1},
		{"openai: another list", DialectOpenAI, 400, openAI("invalid_function_parameters", "input[2]", "bad"), -1},
		{"openai: not a number", DialectOpenAI, 400, openAI("invalid_function_parameters", "tools[x]", "bad"), -1},
		{"openai: a negative place", DialectOpenAI, 400, openAI("invalid_function_parameters", "tools[-1]", "bad"), -1},
		{"openai: a signed place", DialectOpenAI, 400, openAI("invalid_function_parameters", "tools[+3]", "bad"), -1},
		{"openai: another code, the tool's place in its param", DialectOpenAI, 400, openAI("invalid_value", "tools[4].parameters", "bad"), -1},
		{"openai: another code, a Messages path in its sentence", DialectOpenAI, 400, openAI("invalid_value", "", "tools.2.custom.input_schema: bad"), -1},
		{"openai: a Messages body", DialectOpenAI, 400, messages("invalid_request_error", "tools.2.custom.input_schema: bad"), -1},
		{"openai: the right body, another status", DialectOpenAI, 422, openAI("invalid_function_parameters", "tools[4].parameters", "bad"), -1},
		{"openai: the right body, a server's fault", DialectOpenAI, 500, openAI("invalid_function_parameters", "tools[4].parameters", "bad"), -1},
		{"openai: not JSON", DialectOpenAI, 400, "<html>tools[0]</html>", -1},
		{"messages: the schema of the fifth", DialectAnthropic, 400, messages("invalid_request_error", "tools.4.custom.input_schema: JSON schema is invalid"), 4},
		{"messages: without custom", DialectAnthropic, 400, messages("invalid_request_error", "tools.4.input_schema.properties.x: bad"), 4},
		{"messages: the name", DialectAnthropic, 400, messages("invalid_request_error", "tools.1.custom.name: String should match pattern"), 1},
		{"messages: the cache marker this client put there", DialectAnthropic, 400, messages("invalid_request_error", "tools.5.cache_control.ttl: bad"), -1},
		{"messages: a field that only begins like one", DialectAnthropic, 400, messages("invalid_request_error", "tools.5.name_of_something: bad"), -1},
		{"messages: the path in the middle of a sentence", DialectAnthropic, 400, messages("invalid_request_error", "messages.0: see tools.3.custom.input_schema: bad"), -1},
		{"messages: another type of error", DialectAnthropic, 400, messages("not_found_error", "tools.3.custom.input_schema: bad"), -1},
		{"messages: a negative place", DialectAnthropic, 400, messages("invalid_request_error", "tools.-1.custom.input_schema: bad"), -1},
		{"messages: an OpenAI body", DialectAnthropic, 400, openAI("invalid_function_parameters", "tools[4].parameters", "bad"), -1},
		{"messages: the right body, another status", DialectAnthropic, 404, messages("invalid_request_error", "tools.4.custom.input_schema: bad"), -1},

		{"gemini: a name it does not take", DialectGemini, 400, googleRefusedName, 1},
		{"gemini: a member it does not know under a declaration", DialectGemini, 400, googleRefusedMember, 1},
		{"gemini: a schema it refuses, which it does not place", DialectGemini, 400, googleRefusedSchema, -1},
		{"gemini: a required name with no property, which it does not place", DialectGemini, 400, googleRefusedNeeds, -1},
		{"gemini: a schema nested too deep, which it does not place", DialectGemini, 400, googleRefusedDepth, -1},
		{"gemini: a key it refuses", DialectGemini, 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`, -1},
		{"gemini: another list's place", DialectGemini, 400, `{"error":{"code":400,"message":"* GenerateContentRequest.contents[1].parts[0].data: required oneof field 'data' must have one initialized field\n","status":"INVALID_ARGUMENT"}}`, -1},
		{"gemini: a second tools element is not this client's", DialectGemini, 400, `{"error":{"code":400,"message":"* GenerateContentRequest.tools[1].function_declarations[0].name: Invalid function name.\n","status":"INVALID_ARGUMENT"}}`, -1},
		{"gemini: the path later in the sentence", DialectGemini, 400, `{"error":{"code":400,"message":"Request refused, see * GenerateContentRequest.tools[0].function_declarations[2].name: bad","status":"INVALID_ARGUMENT"}}`, -1},
		{"gemini: a violation at the declaration itself, no member of it", DialectGemini, 400, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"tools[0].function_declarations[1]","description":"bad"}]}]}}`, -1},
		{"gemini: a violation at a member that is not the definition's", DialectGemini, 400, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"tools[0].function_declarations[1].behavior","description":"bad"}]}]}}`, -1},
		{"gemini: a violation at a member that only begins like one", DialectGemini, 400, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"tools[0].function_declarations[1].named","description":"bad"}]}]}}`, -1},
		{"gemini: a violation inside the schema of the third", DialectGemini, 400, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"tools[0].function_declarations[2].parameters_json_schema.properties[0].value","description":"bad"}]}]}}`, 2},
		{"gemini: a message at the declaration itself, no member of it", DialectGemini, 400, `{"error":{"code":400,"message":"* GenerateContentRequest.tools[0].function_declarations[1]: bad\n","status":"INVALID_ARGUMENT"}}`, -1},
		{"gemini: the declarations list itself, no place", DialectGemini, 400, `{"error":{"code":400,"message":"* GenerateContentRequest.tools[0].function_declarations: at most 512 are taken\n","status":"INVALID_ARGUMENT"}}`, -1},
		{"gemini: the right body, another status word", DialectGemini, 400, strings.Replace(googleRefusedName, "INVALID_ARGUMENT", "FAILED_PRECONDITION", 1), -1},
		{"gemini: the right body, a server's fault", DialectGemini, 500, googleRefusedName, -1},
		{"gemini: an OpenAI body", DialectGemini, 400, openAI("invalid_function_parameters", "tools[4].parameters", "bad"), -1},
		{"openai: a Google body", DialectOpenAI, 400, googleRefusedName, -1},
	} {
		got, ok := (&HTTPError{StatusCode: c.status, Body: c.body}).refusedToolIndex(c.dialect)
		if !ok {
			got = -1
		}
		if got != c.want {
			t.Errorf("%s: read as the tool at place %d; want %d (-1 is no tool)", c.what, got, c.want)
		}
	}
}

// WHAT ONE CLIENT WAS REFUSED, EVERY CLIENT OF THAT ENTRY AND MODEL KNOWS,
// where the host gave them one memory: a client is made anew when a
// provider is edited, for a role routed to another model, and for every
// spawned run, and each asked again for what the last was refused. Another
// model's client knows nothing of it, nor does another entry's that reaches
// the same endpoint and model (its key and options are its own, and the
// provider may answer it otherwise), nor a client given no memory to share.
func TestClientsOfOneEntryAndModelShareWhatWasRefused(t *testing.T) {
	const refusal = `{"error":{"message":"Invalid schema","type":"invalid_request_error","param":"tools[1].parameters","code":"invalid_function_parameters"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(refusal))
	}))
	t.Cleanup(srv.Close)
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunction{Name: "read", Parameters: map[string]any{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "correct", Parameters: map[string]any{"type": "object"}}},
	}
	shared := NewToolRefusals()
	made := func(entry, model string, memory *ToolRefusals) *Client {
		return New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: model, Provider: "openai", Retries: -1, TimeoutSeconds: 5, ToolRefusals: memory, Entry: entry})
	}
	client := func(model string, memory *ToolRefusals) *Client { return made("an entry", model, memory) }
	first := client("fixture", shared)
	if _, err := first.Chat(context.Background(), []Message{{Role: "user", Content: "go"}}, ChatOptions{Tools: tools}); err == nil {
		t.Fatal("rig: the provider answered")
	}
	for _, c := range []struct {
		what     string
		c        *Client
		withheld int
	}{
		{"the client that was refused", first, 1},
		{"a client made anew for the same entry and model", client("fixture", shared), 1},
		{"a client of another model", client("another", shared), 0},
		{"a client of another entry that reaches the same endpoint and model", made("another entry", "fixture", shared), 0},
		{"a client given no memory to share", client("fixture", nil), 0},
	} {
		if _, withheld := c.c.WithheldTools(tools); len(withheld) != c.withheld {
			t.Errorf("%s withholds %v; want %d tool(s)", c.what, withheld, c.withheld)
		}
	}
}
