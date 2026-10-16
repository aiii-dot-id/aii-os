package pluginworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	DescriptorMissing  = "missing"
	DescriptorMultiple = "multiple"
)

func DescriptorDigest(raw []byte) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(normalizeDescriptorZeros(value))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeDescriptorZeros(value any) any {
	switch v := value.(type) {
	case float64:
		if v == 0 {
			return float64(0)
		}
	case []any:
		for i := range v {
			v[i] = normalizeDescriptorZeros(v[i])
		}
	case map[string]any:
		for key := range v {
			v[key] = normalizeDescriptorZeros(v[key])
		}
	}
	return value
}

type DescriptorMismatchError struct{ Detail string }

func (e *DescriptorMismatchError) Error() string { return e.Detail }

func ProveDescriptor(ctx context.Context, wasm []byte, memoryMax uint64, expected string) (present bool, err error) {
	m, err := Load(ctx, wasm, Config{MemoryMaxBytes: memoryMax})
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, m.Close(ctx)) }()
	return m.CheckDescriptor(ctx, expected)
}

func (m *Module) CheckDescriptor(ctx context.Context, expected string) (bool, error) {
	got, present, err := m.Describe(ctx)
	if err != nil || !present {
		return present, err
	}
	switch expected {
	case DescriptorMultiple:
		return true, nil
	case DescriptorMissing:
		return true, &DescriptorMismatchError{Detail: "the artifact describes itself and the package carries no descriptor file for the interface"}
	}
	digest, err := DescriptorDigest(got)
	if err != nil {
		return true, &DescriptorMismatchError{Detail: fmt.Sprintf("the artifact's account is not JSON: %v", err)}
	}
	if digest != expected {
		return true, &DescriptorMismatchError{Detail: "the packaged descriptor file and the artifact's own account differ"}
	}
	return true, nil
}
