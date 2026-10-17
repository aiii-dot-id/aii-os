package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

type SchemaError struct {
	Operation string
	File      string
	Err       error

	Unread bool
}

func (e *SchemaError) Error() string {
	if e.Operation == "" {
		return fmt.Sprintf("pluginhost: interface descriptor %s: %v", e.File, e.Err)
	}
	return fmt.Sprintf("pluginhost: operation %q: schema %s: %v", e.Operation, e.File, e.Err)
}

func (e *SchemaError) Unwrap() error { return e.Err }

func (e *SchemaError) WaitsOnPackage() bool { return !e.Unread }

func loadDescriptors(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest) (*descriptorSet, error) {
	decls := append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...)
	held = walkMembers(pkgPath, res, held, namedSchemas(decls, held)...)
	set := &descriptorSet{ops: make(map[string]*opDescriptor), files: make(map[string][]byte)}
	contextOperation := ""
	for _, decl := range decls {
		schemaRel := descriptorFile(decl)
		if _, listed := res.FileDigests[schemaRel]; !listed {
			continue
		}
		descBytes, err := loadVerifiedMember(pkgPath, res, held, schemaRel)
		if err != nil {
			return nil, &SchemaError{File: schemaRel, Err: err, Unread: true}
		}
		set.files[schemaRel] = descBytes
		var ops []struct {
			ID           string          `json:"id"`
			Summary      string          `json:"summary"`
			Input        string          `json:"input"`
			Output       string          `json:"output"`
			Effects      string          `json:"effects"`
			Capabilities json.RawMessage `json:"capabilities"`
			MaxResult    int             `json:"max_result_bytes"`

			Family   string   `json:"family"`
			Keywords []string `json:"keywords"`
			Examples []string `json:"examples"`
			Primary  bool     `json:"primary"`
			Context  bool     `json:"context"`

			OperatorConfirms bool `json:"operator_confirms"`
		}
		if err := json.Unmarshal(descBytes, &ops); err != nil {

			if json.Valid(descBytes) && strings.TrimSpace(string(descBytes))[0] != '[' {
				continue
			}
			return nil, &SchemaError{File: schemaRel, Err: err}
		}
		for _, op := range ops {
			if !broker.KnownEffects(op.Effects) {
				return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("effects %q is not a declared class (read.internal, read.external, write.local, write.external, exec)", op.Effects)}
			}

			if decl.ID == ChannelInterfaceID && op.OperatorConfirms {
				return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("a channel adapter's %s may not declare operator_confirms: the host drives it, and an operation waiting for the operator's confirmation neither delivers a message nor answers the host", op.ID)}
			}

			if decl.ID == EmbeddingsInterfaceID && op.OperatorConfirms {
				return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("a source of vectors' %s may not declare operator_confirms: the host drives it", op.ID)}
			}
			d := &opDescriptor{effects: op.Effects, maxResult: op.MaxResult,
				summary: op.Summary, family: op.Family, keywords: op.Keywords, examples: op.Examples,
				operatorConfirms: op.OperatorConfirms, primary: op.Primary, context: op.Context}
			if len(op.Capabilities) > 0 {

				d.capsDeclared = true
				if err := json.Unmarshal(op.Capabilities, &d.capabilities); err != nil {
					return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("capabilities must be a list of strings: %w", err)}
				}
			}
			if op.Input != "" {
				raw, compiled, unread, err := loadSchema(pkgPath, res, held, op.Input)
				if err != nil {
					return nil, &SchemaError{Operation: op.ID, File: op.Input, Err: err, Unread: unread}
				}
				d.raw, d.input = raw, compiled
			}
			if op.Output != "" {
				raw, compiled, unread, err := loadSchema(pkgPath, res, held, op.Output)
				if err != nil {
					return nil, &SchemaError{Operation: op.ID, File: op.Output, Err: err, Unread: unread}
				}
				d.outputRaw, d.output = raw, compiled
			}
			if d.context {
				if contextOperation != "" || d.effects != broker.EffectsReadInternal || d.primary || d.operatorConfirms || !d.capsDeclared || d.input == nil || d.output == nil || d.maxResult < 1 || d.maxResult > ContextMaxBytes {
					return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("context requires one read.internal operation with declared input/output/capabilities, a 1..%d byte result bound, and neither primary nor operator_confirms", ContextMaxBytes)}
				}
				contextOperation = op.ID
			}
			set.ops[op.ID] = d
		}
	}
	return set, nil
}

func descriptorFile(decl packagefmt.InterfaceDecl) string {
	return fmt.Sprintf("interfaces/%s.v%d.schema.json", decl.ID, decl.Version)
}

func namedSchemas(decls []packagefmt.InterfaceDecl, held map[string][]byte) []string {
	var names []string
	for _, decl := range decls {
		var ops []struct {
			Input  string `json:"input"`
			Output string `json:"output"`
		}
		if json.Unmarshal(held[descriptorFile(decl)], &ops) != nil {
			continue
		}
		for _, op := range ops {
			names = append(names, op.Input, op.Output)
		}
	}
	return names
}

type descriptorSet struct {
	ops   map[string]*opDescriptor
	files map[string][]byte
}

type DescriptorMismatchError struct {
	PluginID  string
	Interface string
	Detail    string

	Unasked bool
	Cause   error
}

func (e *DescriptorMismatchError) Unwrap() error { return e.Cause }

func (e *DescriptorMismatchError) Error() string {
	return fmt.Sprintf("pluginhost: plugin %s: the packaged descriptor for %s is not the artifact's own account: %s", e.PluginID, e.Interface, e.Detail)
}

func (e *DescriptorMismatchError) WaitsOnPackage() bool { return !e.Unasked }

func proveDescriptor(ctx context.Context, m *packagefmt.Manifest, artifactBytes []byte, proof string, opts *Options) error {
	ok, err := pluginworker.ProveDescriptor(ctx, artifactBytes, opts.MemoryMax[m.ID], proof)
	if err != nil {
		var mismatch *pluginworker.DescriptorMismatchError
		return descriptorError(m, err, !errors.As(err, &mismatch))
	}
	if !ok {
		logDecision("plugin %s: descriptor unproven — the artifact exports no %s (an older kit); the packaged file is taken as declared", m.ID, pluginworker.ExportDescribe)
		return nil
	}
	if len(m.Interfaces.Core) != 1 {
		logDecision("plugin %s: descriptor unproven — %d core interfaces and one describe export", m.ID, len(m.Interfaces.Core))
		return nil
	}
	return nil
}

func descriptorError(m *packagefmt.Manifest, err error, unasked bool) error {
	iface := ""
	if len(m.Interfaces.Core) == 1 {
		iface = m.Interfaces.Core[0].ID
	}
	return &DescriptorMismatchError{PluginID: m.ID, Interface: iface, Detail: err.Error(), Unasked: unasked, Cause: err}
}

func descriptorExpectation(m *packagefmt.Manifest, set *descriptorSet) (string, error) {
	if len(m.Interfaces.Core) != 1 {
		return pluginworker.DescriptorMultiple, nil
	}
	want, present := set.files[descriptorFile(m.Interfaces.Core[0])]
	if !present {
		return pluginworker.DescriptorMissing, nil
	}
	digest, err := pluginworker.DescriptorDigest(want)
	if err != nil {
		return "", descriptorError(m, fmt.Errorf("the packaged descriptor is not JSON: %w", err), false)
	}
	return digest, nil
}

func loadSchema(pkgPath string, res *packagefmt.Result, held map[string][]byte, rel string) (schema map[string]any, compiled *jsonschema.Schema, unread bool, err error) {
	if _, listed := res.FileDigests[rel]; !listed {
		return nil, nil, false, errors.New("declared but not in the package")
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, rel)
	if err != nil {
		return nil, nil, true, err
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, false, fmt.Errorf("not valid JSON: %w", err)
	}
	if err := normalizeRequired(schema); err != nil {
		return nil, nil, false, err
	}
	compiled, err = jsonschema.Compile(schema)
	if err != nil {
		return nil, nil, false, err
	}
	return schema, compiled, false, nil
}

func normalizeRequired(schema map[string]any) error {
	raw, present := schema["required"]
	if !present {
		return nil
	}
	list := []string{}
	switch v := raw.(type) {
	case []string:
		for i, s := range v {
			if s == "" {
				return fmt.Errorf("\"required\"[%d] is empty; want a non-empty string", i)
			}
		}
		list = append(list, v...)
	case []any:
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("\"required\"[%d] is %T; want a non-empty string", i, e)
			}
			if s == "" {
				return fmt.Errorf("\"required\"[%d] is empty; want a non-empty string", i)
			}
			list = append(list, s)
		}
	default:
		return fmt.Errorf("\"required\" is %T; want an array of non-empty strings", raw)
	}
	schema["required"] = list
	return nil
}
