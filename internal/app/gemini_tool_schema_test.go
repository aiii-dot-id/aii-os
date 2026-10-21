package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// geminiReads are the JSON Schema keywords the API documents as
// supported: "only the following properties are supported: $id, $defs,
// $ref, $anchor, type, format, title, description, enum (for strings and
// numbers), items, prefixItems, minItems, maxItems, minimum, maximum,
// anyOf, oneOf (interpreted the same as anyOf), properties,
// additionalProperties, required. The non-standard propertyOrdering
// property may also be set" (the discovery document).
var geminiReads = set("$id", "$defs", "$ref", "$anchor", "type", "format", "title", "description", "enum", "items", "prefixItems",
	"minItems", "maxItems", "minimum", "maximum", "anyOf", "oneOf", "properties", "additionalProperties", "required", "propertyOrdering")

// geminiPassesOver are keywords of the host's schema subset that are not
// in that list. They are JSON Schema, so a schema that carries them is
// still one the API takes ("While the full JSON Schema may be sent, not
// all features are supported"; "The model ignores unsupported
// properties", the structured output guide). The model is not held to
// them. The host is: it validates every plugin call against the
// operation's compiled schema before the plugin sees it, so what Gemini
// passes over is refused, with its path, when a call breaks it.
var geminiPassesOver = set("const", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern",
	"uniqueItems", "minProperties", "maxProperties", "$schema", "default", "examples", "deprecated")

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var geminiFunctionName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_:.\-]{0,127}$`)

var geminiTypeNames = set("string", "number", "integer", "boolean", "object", "array", "null")

const geminiSchemaDepth = 30

// jsonDepth is how many objects and lists deep a decoded JSON value
// nests.
func jsonDepth(v any) int {
	deepest := 0
	switch node := v.(type) {
	case map[string]any:
		for _, child := range node {
			deepest = max(deepest, jsonDepth(child))
		}
	case []any:
		for _, child := range node {
			deepest = max(deepest, jsonDepth(child))
		}
	default:
		return 0
	}
	return deepest + 1
}

// schemaNestedTo is parameters whose JSON nests exactly depth levels: one
// property, a list of lists of strings. The host admits it well past
// Gemini's bound.
func schemaNestedTo(depth int) map[string]any {
	leaf := map[string]any{"type": "string"}
	for range depth - 3 {
		leaf = map[string]any{"type": "array", "items": leaf}
	}
	return map[string]any{"type": "object", "properties": map[string]any{"p": leaf}}
}

// geminiToolFaults names everything in a tool definition that Google's
// documentation says the Gemini API refuses, that Google was seen to
// refuse, or that neither it nor the host's schema subset defines. The schema is read as the wire carries
// it.
func geminiToolFaults(def llm.ToolDefinition) []string {
	var faults []string
	fault := func(format string, args ...any) { faults = append(faults, fmt.Sprintf(format, args...)) }
	if !geminiFunctionName.MatchString(def.Function.Name) {
		fault("name %q: a function's name starts with a letter or an underscore and is letters, digits, underscores, colons, dots and dashes, at most 128", def.Function.Name)
	}
	if strings.TrimSpace(def.Function.Description) == "" {
		fault("description: required (\"Required. A brief description of the function.\")")
	}
	if def.Function.Parameters == nil {
		return faults // a function may declare no parameters
	}
	raw, err := json.Marshal(def.Function.Parameters)
	if err != nil {
		return append(faults, "parameters: not JSON: "+err.Error())
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return append(faults, "parameters: not a JSON object: "+err.Error())
	}
	// "The schema must describe an object where the properties are the
	// parameters to the function."
	if schema["type"] != "object" {
		fault("parameters: type is %v; the schema must describe an object", schema["type"])
	}
	var walk func(path string, node map[string]any)
	sub := func(path string, v any) {
		if m, ok := v.(map[string]any); ok {
			walk(path, m)
		} else if _, isBool := v.(bool); !isBool || !strings.HasSuffix(path, "additionalProperties") {
			fault("%s: a schema is an object, not %T", path, v)
		}
	}
	walk = func(path string, node map[string]any) {
		for _, key := range slices.Sorted(maps.Keys(node)) {
			if !geminiReads[key] && !geminiPassesOver[key] {
				fault("%s: keyword %q is in neither Gemini's documented list nor the host's schema subset", path, key)
			}
		}
		if _, ref := node["$ref"]; ref {
			// "If $ref is set on a sub-schema, no other properties, except
			// for than those starting as a $, may be set."
			for _, key := range slices.Sorted(maps.Keys(node)) {
				if !strings.HasPrefix(key, "$") {
					fault("%s: %q beside $ref; only members that start with $ may stand beside it", path, key)
				}
			}
		}
		switch t := node["type"].(type) {
		case nil:
		case string:
			if !geminiTypeNames[t] {
				fault("%s: type %q is not a JSON Schema type", path, t)
			}
		case []any:
			var listed []string
			for _, name := range t {
				s, ok := name.(string)
				if !ok || !geminiTypeNames[s] {
					fault("%s: type %v is not a JSON Schema type", path, name)
				} else if slices.Contains(listed, s) {
					// "list of types at properties.a contains duplicates".
					fault("%s: type lists %q twice", path, s)
				}
				listed = append(listed, s)
			}
		default:
			fault("%s: type is %T, not a name or a list of names", path, t)
		}
		if enum, ok := node["enum"]; ok {
			values, isList := enum.([]any)
			if !isList {
				fault("%s: enum is %T, not a list", path, enum)
			}
			for _, v := range values {
				// "enum (for strings and numbers)".
				switch v.(type) {
				case string, float64:
				default:
					fault("%s: enum value %v (%T); Gemini reads an enum of strings and numbers", path, v, v)
				}
			}
		}
		// "schema at top-level requires unspecified property 'x'";
		// "duplicate elements in 'required' at top-level are forbidden".
		if required, ok := node["required"].([]any); ok {
			props, _ := node["properties"].(map[string]any)
			var named []string
			for _, r := range required {
				name, _ := r.(string)
				if _, declared := props[name]; !declared {
					fault("%s: required names %q, which is not among its properties", path, name)
				} else if slices.Contains(named, name) {
					fault("%s: required names %q twice", path, name)
				}
				named = append(named, name)
			}
		} else if node["required"] != nil {
			fault("%s: required is %T, not a list of names", path, node["required"])
		}
		// "value at properties.a must be a string".
		for _, key := range []string{"description", "title", "format", "$id"} {
			if v, stated := node[key]; stated {
				if _, isString := v.(string); !isString {
					fault("%s: %s is %T, not a string", path, key, v)
				}
			}
		}
		for _, key := range []string{"properties", "$defs"} {
			if props, ok := node[key].(map[string]any); ok {
				for _, name := range slices.Sorted(maps.Keys(props)) {
					sub(path+"/"+key+"/"+name, props[name])
				}
			} else if node[key] != nil {
				fault("%s/%s: %T, not an object of schemas", path, key, node[key])
			}
		}
		for _, key := range []string{"items", "additionalProperties"} {
			if v, ok := node[key]; ok {
				sub(path+"/"+key, v)
			}
		}
		for _, key := range []string{"anyOf", "oneOf", "prefixItems"} {
			if list, ok := node[key].([]any); ok {
				for i, v := range list {
					sub(fmt.Sprintf("%s/%s/%d", path, key, i), v)
				}
			} else if node[key] != nil {
				fault("%s/%s: %T, not a list of schemas", path, key, node[key])
			}
		}
	}
	walk("parameters", schema)
	if depth := jsonDepth(schema); depth > geminiSchemaDepth {
		fault("parameters: the schema's JSON nests %d deep; Gemini refuses the whole request past %d and does not say which tool", depth, geminiSchemaDepth)
	}
	return faults
}

// wholeSubsetSchema uses every keyword the host's schema subset admits
// (internal/jsonschema's package comment is the list), in every form the
// subset gives it: a type as a name and as a list, additionalProperties
// as true and as false. A plugin's schema is some part of this.
func wholeSubsetSchema() map[string]any {
	var schema map[string]any
	if err := json.Unmarshal([]byte(wholeSubsetSchemaJSON), &schema); err != nil {
		panic(err)
	}
	return schema
}

const wholeSubsetSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "urn:aii-os:test:whole-subset",
  "title": "Probe", "description": "Record one probe.", "type": "object",
  "additionalProperties": false, "minProperties": 1, "maxProperties": 8, "required": ["name"],
  "properties": {
    "name": {"type": "string", "minLength": 1, "maxLength": 64, "pattern": "^[a-z][a-z0-9_]*$",
      "description": "The probe's name.", "default": "probe", "examples": ["tide_gauge"], "format": "hostname", "deprecated": false},
    "mode": {"type": "string", "enum": ["fast", "slow"]},
    "kind": {"const": "probe"},
    "count": {"type": "integer", "minimum": 0, "maximum": 10, "multipleOf": 1},
    "ratio": {"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 1},
    "flag": {"type": ["boolean", "null"]},
    "tags": {"type": "array", "items": {"type": "string"}, "minItems": 0, "maxItems": 4, "uniqueItems": true},
    "extra": {"type": "object", "additionalProperties": true, "properties": {}}
  }
}`

// hostSubsetKeywords is the host's schema subset, by keyword: its
// assertions and the annotations it reads and ignores.
var hostSubsetKeywords = []string{
	"type", "properties", "required", "additionalProperties", "items", "enum", "const",
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties",
	"$schema", "$id", "title", "description", "default", "examples", "deprecated", "format",
}

// A PLUGIN'S SCHEMA IS ADMITTED ONLY INSIDE THE HOST'S SUBSET, so what
// holds for the whole subset holds for every plugin there is or will be.
// The schema that uses all of it is one the host admits and one with no
// fault under Gemini's rules; each keyword of the subset is either one
// Gemini reads or one it passes over and the host holds the call to; and
// the name the host gives a plugin's operation is inside Gemini's name
// rule at the host's own ceiling. What the subset admits and Gemini does
// not take is named below, each with the schema that shows it.
func TestTheHostsSchemaSubsetIsOneGeminiTakes(t *testing.T) {
	schema := wholeSubsetSchema()
	if _, err := jsonschema.Compile(schema); err != nil {
		t.Fatalf("the whole-subset schema is not one the host admits: %v", err)
	}
	used := map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				used[k] = true
				if k == "properties" {
					for _, p := range child.(map[string]any) {
						collect(p)
					}
					continue
				}
				collect(child)
			}
		}
	}
	collect(schema)
	for _, k := range hostSubsetKeywords {
		if !used[k] {
			t.Errorf("the whole-subset schema does not use %q", k)
		}
		if geminiReads[k] == geminiPassesOver[k] {
			t.Errorf("keyword %q of the host's subset must be exactly one of: read by Gemini, passed over by it", k)
		}
	}
	for k := range geminiPassesOver {
		if !slices.Contains(hostSubsetKeywords, k) {
			t.Errorf("%q is listed as passed over and is not in the host's subset: nothing holds a call to it", k)
		}
	}
	def := llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: "pl_org_example_probe_record", Description: "Record one probe.", Parameters: schema}}
	if faults := geminiToolFaults(def); len(faults) != 0 {
		t.Errorf("the whole of the host's subset, under Gemini's rules:\n  %s", strings.Join(faults, "\n  "))
	}
	// What the host refuses, Gemini is never sent: the subset's own wall.
	for _, outside := range []string{"$ref", "anyOf", "oneOf", "prefixItems", "$defs", "patternProperties", "if"} {
		if _, err := jsonschema.Compile(map[string]any{"type": "object", outside: map[string]any{}}); err == nil {
			t.Errorf("the host admitted %q; this file's proof covers only the subset it lists", outside)
		}
	}
	// WHAT THE HOST ADMITS AND GEMINI DOES NOT. Both halves of each are
	// held, so whichever moves is seen: the host admits the schema, and
	// this rule faults it, once. Google's answer for each names no
	// declaration (this file's header), so nothing sooner than a refused
	// turn would say it.
	of := func(prop map[string]any, more map[string]any) map[string]any {
		schema := map[string]any{"type": "object", "properties": map[string]any{"a": prop}}
		maps.Copy(schema, more)
		return schema
	}
	str := map[string]any{"type": "string"}
	for _, gap := range []struct {
		name, want string
		schema     map[string]any
	}{
		{"JSON nested one level past the bound", fmt.Sprintf("nests %d deep", geminiSchemaDepth+1), schemaNestedTo(geminiSchemaDepth + 1)},
		{"a required name with no property", `required names "x", which is not among its properties`, of(str, map[string]any{"required": []any{"x"}})},
		{"a required name twice", `required names "a" twice`, of(str, map[string]any{"required": []any{"a", "a"}})},
		{"a type listed twice", `type lists "string" twice`, of(map[string]any{"type": []any{"string", "string"}}, nil)},
		{"a description that is a number", "description is float64", of(map[string]any{"type": "string", "description": 5}, nil)},
		{"a title that is a number", "title is float64", of(map[string]any{"type": "string", "title": 5}, nil)},
		{"a format that is a number", "format is float64", of(map[string]any{"type": "string", "format": 7}, nil)},
		{"an $id that is a number", "$id is float64", of(str, map[string]any{"$id": 5})},
	} {
		if _, err := jsonschema.Compile(gap.schema); err != nil {
			t.Errorf("%s: the host no longer admits it (%v): this gap has closed, say so here", gap.name, err)
		}
		faults := geminiToolFaults(llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: "gap", Description: "d", Parameters: gap.schema}})
		if len(faults) != 1 || !strings.Contains(faults[0], gap.want) {
			t.Errorf("%s: faults %q, want one saying %q", gap.name, faults, gap.want)
		}
	}
	atTheBound := schemaNestedTo(geminiSchemaDepth)
	if faults := geminiToolFaults(llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: "deep", Description: "d", Parameters: atTheBound}}); len(faults) != 0 || jsonDepth(atTheBound) != geminiSchemaDepth {
		t.Errorf("JSON nested %d deep, which Google takes: depth %d, faults %q", geminiSchemaDepth, jsonDepth(atTheBound), faults)
	}
	// A plugin operation's name: the host's charset is inside Gemini's,
	// and the host's ceiling is under Gemini's.
	longest := "pl_" + strings.Repeat("a-Z_9", 12) + "x" // 64 bytes of the host's charset
	if len(longest) != 64 || !geminiFunctionName.MatchString(longest) {
		t.Errorf("a plugin operation's longest name (%d bytes) is outside Gemini's name rule", len(longest))
	}
}

// THE RULE CAN FAIL. Each definition here breaks one thing Google's
// documentation refuses, and is named for it.
func TestGeminiToolRuleNamesWhatGoogleRefuses(t *testing.T) {
	object := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	for _, tc := range []struct {
		name, want string
		def        llm.ToolFunction
	}{
		{"a name with a space", "a function's name", llm.ToolFunction{Name: "do thing", Description: "d", Parameters: object(nil)}},
		{"a name of 129 bytes", "a function's name", llm.ToolFunction{Name: strings.Repeat("n", 129), Description: "d", Parameters: object(nil)}},
		{"a name that opens with a digit", "a function's name", llm.ToolFunction{Name: "9lives", Description: "d", Parameters: object(nil)}},
		{"no description", "description: required", llm.ToolFunction{Name: "f", Parameters: object(nil)}},
		{"parameters that are not an object", "must describe an object", llm.ToolFunction{Name: "f", Description: "d", Parameters: map[string]any{"type": "string"}}},
		{"an enum of a boolean", "enum of strings and numbers", llm.ToolFunction{Name: "f", Description: "d", Parameters: object(map[string]any{"on": map[string]any{"enum": []any{true}}})}},
		{"an enum holding null", "enum of strings and numbers", llm.ToolFunction{Name: "f", Description: "d", Parameters: object(map[string]any{"on": map[string]any{"enum": []any{"a", nil}}})}},
		{"a type that is no type", "not a JSON Schema type", llm.ToolFunction{Name: "f", Description: "d", Parameters: object(map[string]any{"n": map[string]any{"type": "float"}})}},
		{"a member beside $ref", "beside $ref", llm.ToolFunction{Name: "f", Description: "d", Parameters: object(map[string]any{"n": map[string]any{"$ref": "#/$defs/n", "description": "x"}})}},
		{"a keyword nobody defines", `keyword "strict"`, llm.ToolFunction{Name: "f", Description: "d", Parameters: map[string]any{"type": "object", "strict": true}}},
		{"a keyword nobody defines, nested", `parameters/properties/n/items: keyword "nullable"`, llm.ToolFunction{Name: "f", Description: "d", Parameters: object(map[string]any{"n": map[string]any{"type": "array", "items": map[string]any{"type": "string", "nullable": true}}})}},
	} {
		faults := geminiToolFaults(llm.ToolDefinition{Type: "function", Function: tc.def})
		if len(faults) != 1 || !strings.Contains(faults[0], tc.want) {
			t.Errorf("%s: faults %q, want one saying %q", tc.name, faults, tc.want)
		}
	}
	if faults := geminiToolFaults(llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: "f", Description: "d"}}); len(faults) != 0 {
		t.Errorf("a function that declares no parameters: %q", faults)
	}
}

func TestTheToolDefinitionsTheAppBuildsAreOnesGeminiTakes(t *testing.T) {
	requests, kinds := toolDefinitionsTheAppBuilds(t)
	swept := 0
	for _, defs := range requests {
		for _, def := range defs {
			swept++
			if faults := geminiToolFaults(def); len(faults) != 0 {
				t.Errorf("tool %s:\n  %s", def.Function.Name, strings.Join(faults, "\n  "))
			}
		}
	}
	// The sweep saw what it claims to have swept.
	for _, kind := range []string{"a primary plugin operation", "an identity verb", "a host tool", "the self-model's commit", "the substrate check's tool"} {
		if kinds[kind] == 0 {
			t.Errorf("the definitions swept (%d) hold no %s", swept, kind)
		}
	}
}

// toolDefinitionsTheAppBuilds births and starts an identity with one
// plugin and returns the definitions it would send, grouped as the
// requests that carry them (a turn's; the self-model's; the substrate
// check's), and how many of each kind were seen.
func toolDefinitionsTheAppBuilds(t *testing.T) (requests [][]llm.ToolDefinition, kinds map[string]int) {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SchemaTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	// Only the operation its author marked primary contributes a provider
	// definition; the rest are reached through tools action=call.
	const plugin = "org.example.schema"
	installPluginDir(t, dir, plugin, buildResponderPkgWithPrimaries(t, dir, plugin, []string{"memory.store", "memory.search", "memory.recent", "memory.reflect", "probe.ping"}, "probe.ping"))
	t.Chdir(dir)
	app := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	turn := app.buildToolDefinitions()
	kinds = map[string]int{}
	for _, def := range turn {
		switch name := def.Function.Name; {
		case strings.HasPrefix(name, "pl_"):
			kinds["a primary plugin operation"]++
		case name == "recall", name == "work":
			kinds["an identity verb"]++
		case name == "read", name == "tools":
			kinds["a host tool"]++
		}
	}
	// The self-model's view of commit is another schema than the verb's.
	kinds["the self-model's commit"]++
	// The substrate check's tool, as the check itself hands it to a client.
	var asked schemaProbeClient
	app.probeToolSelection(t.Context(), &asked, providerEntry{Name: "probe"}, "m")
	kinds["the substrate check's tool"] += len(asked.tools)
	return [][]llm.ToolDefinition{turn, {selfModelCommitter{}.Definition()}, asked.tools}, kinds
}

// schemaProbeClient keeps the tools a caller offers and answers nothing.
type schemaProbeClient struct{ tools []llm.ToolDefinition }

func (c *schemaProbeClient) Chat(_ context.Context, _ []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	c.tools = append(c.tools, opts.Tools...)
	return nil, errors.New("the schema sweep's client answers nothing")
}

// THE PLUGINS' OWN SCHEMA FILES, where a run says where they are:
// AII_TOOL_SCHEMA_DIRS names directories (separated as PATH is), and
// every operation's input schema under them is held to Gemini's rules as
// the tool's parameters it becomes. An input schema is a file named as
// the plugins name one (<operation>_in.json, <operation>.input.json)
// that the host's subset admits. Every other .json file is counted and
// not judged: an operation's result schema is shown to the identity as
// text and is never a provider's to check, and a manifest or a vector
// file is no schema. The repositories of the plugins are not this one's
// to read at test time, so without the variable this is skipped, and
// says so.
func TestThePluginSchemasNamedAreOnesGeminiTakes(t *testing.T) {
	dirs := filepath.SplitList(os.Getenv("AII_TOOL_SCHEMA_DIRS"))
	if len(dirs) == 0 {
		t.Skip("set AII_TOOL_SCHEMA_DIRS to the plugins' schema directories to hold their files to Gemini's rules")
	}
	for _, dir := range dirs {
		defs, passedBy := pluginInputSchemas(t, dir)
		for _, def := range defs {
			if faults := geminiToolFaults(def); len(faults) != 0 {
				t.Errorf("%s, %s:\n  %s", dir, def.Function.Description, strings.Join(faults, "\n  "))
			}
		}
		t.Logf("%s: %d input schema(s) held to Gemini's rules, %d other .json file(s) passed by", dir, len(defs), passedBy)
	}
}

// pluginInputSchemas reads a directory's input schemas as the tool
// definitions they become, each described by its file's path, and counts
// the .json files it passed by.
func pluginInputSchemas(t *testing.T, dir string) (defs []llm.ToolDefinition, passedBy int) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		if !strings.HasSuffix(path, "_in.json") && !strings.HasSuffix(path, ".input.json") {
			passedBy++
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var schema map[string]any
		if json.Unmarshal(raw, &schema) != nil {
			passedBy++
			return nil
		}
		if _, err := jsonschema.Compile(schema); err != nil {
			passedBy++
			return nil
		}
		defs = append(defs, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: fmt.Sprintf("pl_schema_file_%d", len(defs)), Description: path, Parameters: schema}})
		return nil
	})
	if err != nil {
		t.Errorf("%s: %v", dir, err)
	}
	if len(defs) == 0 {
		t.Errorf("%s: no input schema the host admits was found there", dir)
	}
	return defs, passedBy
}
