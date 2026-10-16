package wire

type ResponseFormat struct {
	Type       string            `json:"type"`
	JSONSchema *JSONSchemaFormat `json:"json_schema,omitempty"`
}

type JSONSchemaFormat struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}
