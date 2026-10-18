package llm

import (
	"encoding/json"
	"regexp"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
)

var responseFormatName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type ResponseFormatError struct {
	Code   string
	Reason string
}

func (e *ResponseFormatError) Error() string { return e.Code + ": " + e.Reason }
func formatError(code, reason string) error  { return &ResponseFormatError{Code: code, Reason: reason} }

func ValidateResponseFormat(format *ResponseFormat) error {
	if format == nil {
		return nil
	}
	if format.Type != "json_schema" || format.JSONSchema == nil {
		return formatError("RESPONSE_FORMAT_INVALID", "requires json_schema")
	}
	js := format.JSONSchema
	if !responseFormatName.MatchString(js.Name) {
		return formatError("RESPONSE_FORMAT_INVALID", "name must contain 1..64 ASCII letters, digits, underscores or hyphens")
	}
	if !js.Strict {
		return formatError("RESPONSE_FORMAT_INVALID", "strict must be true")
	}
	if js.Schema == nil {
		return formatError("RESPONSE_FORMAT_INVALID", "schema is required")
	}
	encoded, err := json.Marshal(js.Schema)
	if err != nil {
		return formatError("RESPONSE_FORMAT_INVALID", err.Error())
	}
	var schema map[string]any
	if err := canonicaljson.DecodeStrict(encoded, &schema); err != nil {
		return formatError("RESPONSE_FORMAT_INVALID", err.Error())
	}
	if _, err := jsonschema.Compile(schema); err != nil {
		return formatError("RESPONSE_FORMAT_INVALID", err.Error())
	}
	return nil
}

func CloneResponseFormat(format *ResponseFormat) (*ResponseFormat, error) {
	if err := ValidateResponseFormat(format); err != nil {
		return nil, err
	}
	if format == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(format)
	if err != nil {
		return nil, err
	}
	var out ResponseFormat
	if err := canonicaljson.DecodeStrict(encoded, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func ValidateStructuredOutput(format *ResponseFormat, text string) error {
	if format == nil {
		return nil
	}
	frozen, err := CloneResponseFormat(format)
	if err != nil {
		return err
	}
	var value any
	if err := canonicaljson.DecodeStrict([]byte(text), &value); err != nil {
		return formatError("RESPONSE_FORMAT_VIOLATION", err.Error())
	}
	schema, err := jsonschema.Compile(frozen.JSONSchema.Schema)
	if err != nil {
		return err
	}
	if err := schema.Validate(value); err != nil {
		return formatError("RESPONSE_FORMAT_VIOLATION", err.Error())
	}
	return nil
}
