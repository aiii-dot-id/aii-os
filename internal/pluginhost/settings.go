package pluginhost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const SettingsFile = "settings.json"

const (
	MaxSettings           = 16
	MaxSettingKeyBytes    = 32
	MaxSettingTitleBytes  = 64
	MaxSettingDescBytes   = 256
	MaxSettingStringBytes = 1024
	MaxSettingEnumValues  = 256
	MaxSettingLabelBytes  = 64
	MaxSettingHandleBytes = 64
)

const (
	SettingString  = "string"
	SettingNumber  = "number"
	SettingInteger = "integer"
	SettingBoolean = "boolean"
	SettingEnum    = "enum"
	SettingSecret  = "secret"
)

const (
	ScopeHearing  = "hearing"
	ScopeSpeaking = "speaking"
	ScopeSession  = "session"
)

var (
	settingKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	settingHandlePattern = regexp.MustCompile(fmt.Sprintf(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,%d}$`, MaxSettingHandleBytes-1))

	settingOperationPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type SettingDecl struct {
	Key         string            `json:"key"`
	Type        string            `json:"type"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Default     interface{}       `json:"default,omitempty"`
	Values      []string          `json:"values,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Required    bool              `json:"required,omitempty"`
	Minimum     *float64          `json:"minimum,omitempty"`
	Maximum     *float64          `json:"maximum,omitempty"`

	Scope string `json:"scope,omitempty"`

	OAuth *SettingOAuthHint `json:"oauth,omitempty"`

	ChoicesFrom string `json:"choices_from,omitempty"`
}

type SettingChoice struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type SettingOAuthHint struct {
	Provider string   `json:"provider"`
	Services []string `json:"services,omitempty"`
}

type SettingsError struct {
	PluginID string
	Detail   string
}

func (e *SettingsError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not a settings declaration the host honors: %s", e.PluginID, SettingsFile, e.Detail)
}

func (e *SettingsError) WaitsOnPackage() bool { return true }

func ParseSettings(raw []byte) ([]SettingDecl, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var decls []SettingDecl
	if err := dec.Decode(&decls); err != nil {
		return nil, fmt.Errorf("not a list of settings: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content after the list")
	}
	if len(decls) > MaxSettings {
		return nil, fmt.Errorf("%d settings; at most %d", len(decls), MaxSettings)
	}
	seen := map[string]bool{}
	for i := range decls {
		d := &decls[i]
		if !settingKeyPattern.MatchString(d.Key) {
			return nil, fmt.Errorf("setting %d: key %q is not lowercase [a-z][a-z0-9_]{0,31}", i, d.Key)
		}
		if seen[d.Key] {
			return nil, fmt.Errorf("setting %q declared twice", d.Key)
		}
		seen[d.Key] = true
		if d.Title == "" || len(d.Title) > MaxSettingTitleBytes {
			return nil, fmt.Errorf("setting %q: title must be 1..%d bytes", d.Key, MaxSettingTitleBytes)
		}
		if len(d.Description) > MaxSettingDescBytes {
			return nil, fmt.Errorf("setting %q: description over %d bytes", d.Key, MaxSettingDescBytes)
		}
		switch d.Type {
		case SettingString, SettingNumber, SettingInteger, SettingBoolean, SettingEnum, SettingSecret:
		default:
			return nil, fmt.Errorf("setting %q: type %q is not string, number, integer, boolean, enum or secret", d.Key, d.Type)
		}
		switch d.Scope {
		case "", ScopeHearing, ScopeSpeaking, ScopeSession:
		default:
			return nil, fmt.Errorf("setting %q: scope %q is not %s, %s or %s", d.Key, d.Scope, ScopeHearing, ScopeSpeaking, ScopeSession)
		}
		if d.Type == SettingEnum {
			if len(d.Values) == 0 || len(d.Values) > MaxSettingEnumValues {
				return nil, fmt.Errorf("setting %q: an enum declares 1..%d values", d.Key, MaxSettingEnumValues)
			}
			vs := map[string]bool{}
			for _, v := range d.Values {
				if v == "" || len(v) > MaxSettingTitleBytes || vs[v] {
					return nil, fmt.Errorf("setting %q: enum values are distinct, 1..%d bytes", d.Key, MaxSettingTitleBytes)
				}
				vs[v] = true
			}
			for v, label := range d.Labels {
				if !vs[v] {
					return nil, fmt.Errorf("setting %q: label for %q, which is not one of its values", d.Key, v)
				}
				if label == "" || len(label) > MaxSettingLabelBytes {
					return nil, fmt.Errorf("setting %q: labels are 1..%d bytes", d.Key, MaxSettingLabelBytes)
				}
			}
		} else {
			if len(d.Values) > 0 {
				return nil, fmt.Errorf("setting %q: values belong to an enum", d.Key)
			}
			if len(d.Labels) > 0 {
				return nil, fmt.Errorf("setting %q: labels belong to an enum", d.Key)
			}
		}
		if d.OAuth != nil {
			if d.Type != SettingSecret {
				return nil, fmt.Errorf("setting %q: an oauth hint belongs to a secret", d.Key)
			}
			if !settingHandlePattern.MatchString(d.OAuth.Provider) {
				return nil, fmt.Errorf("setting %q: oauth provider %q is not a name", d.Key, d.OAuth.Provider)
			}
			if len(d.OAuth.Services) > MaxSettingEnumValues {
				return nil, fmt.Errorf("setting %q: at most %d oauth services", d.Key, MaxSettingEnumValues)
			}
			for _, svc := range d.OAuth.Services {
				if !settingKeyPattern.MatchString(svc) {
					return nil, fmt.Errorf("setting %q: oauth service %q is not a name", d.Key, svc)
				}
			}
		}
		if d.ChoicesFrom != "" {
			if d.Type != SettingString {
				return nil, fmt.Errorf("setting %q: choices_from belongs to a string (an enum's choices are its own)", d.Key)
			}
			if !settingOperationPattern.MatchString(d.ChoicesFrom) {
				return nil, fmt.Errorf("setting %q: choices_from %q is not an operation name", d.Key, d.ChoicesFrom)
			}
		}
		numeric := d.Type == SettingNumber || d.Type == SettingInteger
		if !numeric && (d.Minimum != nil || d.Maximum != nil) {
			return nil, fmt.Errorf("setting %q: minimum and maximum belong to a number", d.Key)
		}
		if d.Minimum != nil && d.Maximum != nil && *d.Minimum > *d.Maximum {
			return nil, fmt.Errorf("setting %q: minimum above maximum", d.Key)
		}
		if d.Type == SettingInteger {
			for _, bound := range []*float64{d.Minimum, d.Maximum} {
				if bound != nil && !wholeNumber(*bound) {
					return nil, fmt.Errorf("setting %q: an integer's bounds are whole numbers", d.Key)
				}
			}
		}
		if d.Type == SettingSecret && d.Default != nil {
			return nil, fmt.Errorf("setting %q: a secret has no default; the operator names its handle", d.Key)
		}
		if d.Default != nil {
			if err := CheckSettingValue(*d, d.Default); err != nil {
				return nil, fmt.Errorf("setting %q: default %v", d.Key, err)
			}
		}
	}
	return decls, nil
}

func wholeNumber(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f)
}

func settingNumber(v interface{}) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		var err error
		if f, err = n.Float64(); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func CheckSettingValue(d SettingDecl, v interface{}) error {
	switch d.Type {
	case SettingString:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}
		if len(s) > MaxSettingStringBytes {
			return fmt.Errorf("over %d bytes", MaxSettingStringBytes)
		}
	case SettingNumber, SettingInteger:
		f, ok := settingNumber(v)
		if !ok {
			return fmt.Errorf("must be a number")
		}
		if d.Type == SettingInteger && !wholeNumber(f) {
			return fmt.Errorf("must be a whole number")
		}
		if d.Minimum != nil && f < *d.Minimum {
			return fmt.Errorf("below the minimum %v", *d.Minimum)
		}
		if d.Maximum != nil && f > *d.Maximum {
			return fmt.Errorf("above the maximum %v", *d.Maximum)
		}
	case SettingBoolean:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be true or false")
		}
	case SettingEnum:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("must be one of the declared %d values", len(d.Values))
		}
		for _, allowed := range d.Values {
			if s == allowed {
				return nil
			}
		}
		if len(d.Values) > 8 {
			return fmt.Errorf("must be one of the declared %d values; %q is not", len(d.Values), s)
		}
		return fmt.Errorf("must be one of %v", d.Values)
	case SettingSecret:
		s, ok := v.(string)
		if !ok || !settingHandlePattern.MatchString(s) {
			return fmt.Errorf("must name a credential handle")
		}
	default:
		return fmt.Errorf("type %q is not declared", d.Type)
	}
	return nil
}

func EffectiveSettings(decls []SettingDecl, values map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for _, d := range decls {
		if d.Default != nil {
			out[d.Key] = d.Default
		}
		if v, ok := values[d.Key]; ok && v != nil && CheckSettingValue(d, v) == nil {
			out[d.Key] = v
		}
	}
	return out
}

func StoredInvalid(d SettingDecl, values map[string]interface{}) string {
	v, ok := values[d.Key]
	if !ok || v == nil {
		return ""
	}
	if err := CheckSettingValue(d, v); err != nil {
		return err.Error()
	}
	return ""
}

func (ap *ActivePlugin) Setting(key string) *SettingDecl {
	for i := range ap.Settings {
		if ap.Settings[i].Key == key {
			return &ap.Settings[i]
		}
	}
	return nil
}

func SettingKeys(decls []SettingDecl) []string {
	keys := make([]string, 0, len(decls))
	for _, d := range decls {
		keys = append(keys, d.Key)
	}
	return keys
}

func loadSettings(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest) ([]SettingDecl, error) {
	if _, present := res.FileDigests[SettingsFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, SettingsFile)
	if err != nil {
		return nil, err
	}
	decls, err := ParseSettings(raw)
	if err != nil {
		return nil, &SettingsError{PluginID: m.ID, Detail: err.Error()}
	}
	declared := map[string]bool{}
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		for _, method := range decl.Methods {
			declared[method] = true
		}
	}
	for _, d := range decls {
		if d.ChoicesFrom != "" && !declared[d.ChoicesFrom] {
			return nil, &SettingsError{PluginID: m.ID, Detail: fmt.Sprintf("setting %q: choices_from %q is not a method this package declares", d.Key, d.ChoicesFrom)}
		}
	}
	return decls, nil
}

func ParseSettingChoices(raw []byte) ([]SettingChoice, error) {
	var reply struct {
		Choices []SettingChoice `json:"choices"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("the answer is not {\"choices\": [...]}: %v", err)
	}
	if len(reply.Choices) > MaxSettingEnumValues {
		return nil, fmt.Errorf("%d choices; at most %d", len(reply.Choices), MaxSettingEnumValues)
	}
	seen := map[string]bool{}
	for i, c := range reply.Choices {
		switch {
		case c.Value == "" || len(c.Value) > MaxSettingStringBytes:
			return nil, fmt.Errorf("choice %d: a value is 1 to %d bytes", i, MaxSettingStringBytes)
		case seen[c.Value]:
			return nil, fmt.Errorf("choice %q offered twice", c.Value)
		case len(c.Label) > MaxSettingLabelBytes:
			return nil, fmt.Errorf("choice %q: a label is at most %d bytes", c.Value, MaxSettingLabelBytes)
		}
		seen[c.Value] = true
	}
	if reply.Choices == nil {
		reply.Choices = []SettingChoice{}
	}
	return reply.Choices, nil
}
