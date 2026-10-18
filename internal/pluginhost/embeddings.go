package pluginhost

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const (
	EmbeddingsInterfaceID = "aii.embeddings"

	EmbeddingsInterfaceVersion = 1

	EmbeddingsFamily = "provider_bridge"

	MethodEmbed = "embed"

	EmbedKindMemory = "memory"
	EmbedKindCue    = "cue"

	MaxEmbeddingDimension = 4096

	MaxEmbeddingForm = 65535

	MaxEmbeddingProbes = 32

	MaxEmbedChars = 8000

	MinProbeBound = 0.9
)

const EmbeddingsFile = "embeddings.json"

type EmbeddingsDecl struct {
	Dimension int `json:"dimension"`

	Form int `json:"form"`

	Floor float64 `json:"floor"`

	Model string `json:"model"`

	ProbeBound float64 `json:"probe_bound"`

	Probes []EmbeddingProbe `json:"probes"`
}

type EmbeddingProbe struct {
	Kind   string      `json:"kind"`
	Text   string      `json:"text"`
	Vector ProbeVector `json:"vector"`
}

type ProbeVector []float64

func (v *ProbeVector) UnmarshalJSON(raw []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("a vector is a list of numbers")
	}
	out := make(ProbeVector, len(items))
	for i, item := range items {
		if len(item) == 0 || !(item[0] == '-' || (item[0] >= '0' && item[0] <= '9')) {
			return fmt.Errorf("a vector is a list of numbers; entry %d is not one", i+1)
		}
		if err := json.Unmarshal(item, &out[i]); err != nil {
			return fmt.Errorf("a vector is a list of numbers; entry %d is not one", i+1)
		}
	}
	*v = out
	return nil
}

type EmbeddingsError struct {
	PluginID string
	Detail   string
}

func (e *EmbeddingsError) Error() string {
	return fmt.Sprintf("plugin %s is not admitted as a source of vectors: %s", e.PluginID, e.Detail)
}

type Embeddings struct {
	PluginID string
	Embed    string
	Decl     EmbeddingsDecl

	ModelSHA256 string
}

func (e *Embeddings) Basis() string {
	return fmt.Sprintf("%s/%s/f%d", e.PluginID, e.ModelSHA256[:12], e.Decl.Form)
}

var reSourceBasis = regexp.MustCompile(`^(.+)/[0-9a-f]{12}/f[0-9]+$`)

func SourceOfBasis(basis string) (pluginID string, ok bool) {
	m := reSourceBasis.FindStringSubmatch(basis)
	if m == nil || !packagefmt.ValidManifestID(m[1]) {
		return "", false
	}
	return m[1], true
}

func ParseEmbeddings(raw []byte) (*EmbeddingsDecl, error) {
	var d EmbeddingsDecl
	if err := readDeclaration(raw, &d, "an embeddings declaration", "trailing content after the embeddings declaration"); err != nil {
		return nil, err
	}
	switch {
	case d.Dimension < 1 || d.Dimension > MaxEmbeddingDimension:
		return nil, fmt.Errorf("dimension must be 1..%d", MaxEmbeddingDimension)
	case d.Form < 1 || d.Form > MaxEmbeddingForm:
		return nil, fmt.Errorf("form must be 1..%d", MaxEmbeddingForm)
	case !(d.Floor > 0 && d.Floor <= 1):
		return nil, fmt.Errorf("floor must be above 0 and at most 1")
	case !reModelName.MatchString(d.Model):
		return nil, fmt.Errorf("model must name an entry of %s", ModelsFile)
	case !(d.ProbeBound >= MinProbeBound && d.ProbeBound <= 1):
		return nil, fmt.Errorf("probe_bound must be %.1f..1", MinProbeBound)
	case len(d.Probes) < 1 || len(d.Probes) > MaxEmbeddingProbes:
		return nil, fmt.Errorf("probes must hold 1..%d entries", MaxEmbeddingProbes)
	}
	for i, p := range d.Probes {
		if p.Kind != EmbedKindMemory && p.Kind != EmbedKindCue {
			return nil, fmt.Errorf("probe %d: kind must be %q or %q", i+1, EmbedKindMemory, EmbedKindCue)
		}
		if n := utf8.RuneCountInString(p.Text); n < 1 || n > MaxEmbedChars || !utf8.ValidString(p.Text) {
			return nil, fmt.Errorf("probe %d: text must be 1..%d characters of valid UTF-8", i+1, MaxEmbedChars)
		}

		if strings.TrimSpace(p.Text) == "" {
			return nil, fmt.Errorf("probe %d: text is white space only, which a source is never handed", i+1)
		}
		if len(p.Vector) != d.Dimension {
			return nil, fmt.Errorf("probe %d: vector has %d numbers, the declared dimension is %d", i+1, len(p.Vector), d.Dimension)
		}
		var norm float64
		for _, v := range p.Vector {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("probe %d: vector holds a number that is not finite", i+1)
			}
			norm += v * v
		}
		if norm == 0 {
			return nil, fmt.Errorf("probe %d: vector is all zeros, which no cosine can be taken against", i+1)
		}

		if math.IsInf(norm, 0) {
			return nil, fmt.Errorf("probe %d: vector's entries are too large for its length to be a number, so no cosine can be taken against it", i+1)
		}
	}

	kinds := map[string]bool{}
	for _, p := range d.Probes {
		kinds[p.Kind] = true
	}
	if !kinds[EmbedKindMemory] || !kinds[EmbedKindCue] {
		return nil, fmt.Errorf("probes must hold at least one %s and one %s", EmbedKindMemory, EmbedKindCue)
	}
	return &d, nil
}

func declaresEmbeddings(m *packagefmt.Manifest) (bool, error) {
	var decl *packagefmt.InterfaceDecl
	interfaces := 0
	if m.Interfaces != nil {
		for _, d := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
			interfaces++
			if d.ID == EmbeddingsInterfaceID && decl == nil {
				d := d
				decl = &d
			}
		}
	}
	if decl == nil {
		return false, nil
	}

	if interfaces != 1 {
		return false, &EmbeddingsError{PluginID: m.ID,
			Detail: fmt.Sprintf("a source of vectors declares %s and no other interface; the package declares %d", EmbeddingsInterfaceID, interfaces)}
	}
	if m.PluginFamily != EmbeddingsFamily {
		return false, &EmbeddingsError{PluginID: m.ID, Detail: "declares " + EmbeddingsInterfaceID + " but plugin_family is " + m.PluginFamily +
			"; the interface is accepted only under " + EmbeddingsFamily}
	}
	if decl.Version != EmbeddingsInterfaceVersion {
		return false, &EmbeddingsError{PluginID: m.ID,
			Detail: fmt.Sprintf("declares %s@%d; this host speaks @%d", EmbeddingsInterfaceID, decl.Version, EmbeddingsInterfaceVersion)}
	}
	if len(decl.Methods) != 1 || decl.Methods[0] != MethodEmbed {
		return false, &EmbeddingsError{PluginID: m.ID,
			Detail: fmt.Sprintf("%s@%d has one operation, %s; the package declares %d", EmbeddingsInterfaceID, EmbeddingsInterfaceVersion, MethodEmbed, len(decl.Methods))}
	}
	return true, nil
}

func admitEmbeddings(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest) (*Embeddings, error) {
	declared, err := declaresEmbeddings(m)
	if err != nil {
		return nil, err
	}
	_, member := res.FileDigests[EmbeddingsFile]
	if !declared {
		if member {
			return nil, &EmbeddingsError{PluginID: m.ID, Detail: "carries " + EmbeddingsFile + " and declares no " + EmbeddingsInterfaceID + " interface"}
		}
		return nil, nil
	}
	refuse := func(format string, args ...any) (*Embeddings, error) {
		return nil, &EmbeddingsError{PluginID: m.ID, Detail: fmt.Sprintf(format, args...)}
	}

	if !packagefmt.ValidHostBound(m.AiiosMinVersion) || packagefmt.CompareHostBounds(m.AiiosMinVersion, packagefmt.EmbeddingsMinHost) < 0 {
		return refuse("%s requires aiios_min_version >= %s", EmbeddingsInterfaceID, packagefmt.EmbeddingsMinHost)
	}

	var envelope []string
	if len(m.CapabilityEnvelope) != 0 && json.Unmarshal(m.CapabilityEnvelope, &envelope) != nil {
		return refuse("the capability envelope does not read as a list")
	}
	if len(envelope) != 0 {
		return refuse("a source of vectors declares no capability; the envelope names %d, the first %q", len(envelope), envelope[0])
	}

	for i := range m.Variants {
		if m.Variants[i].ExecutionRuntime != "native_t3_component" {
			return refuse("variant %s is not native; only a native child is handed the model the basis names", m.Variants[i].VariantID)
		}
	}
	for i := range m.Variants {
		var caps []string
		if len(m.Variants[i].VariantCapabilities) != 0 && json.Unmarshal(m.Variants[i].VariantCapabilities, &caps) != nil {
			return refuse("variant %s: its capabilities do not read as a list", m.Variants[i].VariantID)
		}
		if len(caps) != 0 {
			return refuse("a source of vectors declares no capability; variant %s names %d, the first %q", m.Variants[i].VariantID, len(caps), caps[0])
		}
	}

	for _, other := range []string{SettingsFile, WebhooksFile, ScheduleFile, SubscriptionsFile} {
		if _, present := res.FileDigests[other]; present {
			return refuse("a source of vectors carries no %s", other)
		}
	}
	if !member {
		return refuse("declares %s and carries no %s", EmbeddingsInterfaceID, EmbeddingsFile)
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, EmbeddingsFile)
	if err != nil {
		return nil, err
	}
	decl, err := ParseEmbeddings(raw)
	if err != nil {
		return refuse("%s: %v", EmbeddingsFile, err)
	}

	if _, present := res.FileDigests[ModelsFile]; !present {
		return refuse("%s names model %q and the package declares no models", EmbeddingsFile, decl.Model)
	}
	modelsRaw, err := loadVerifiedMember(pkgPath, res, held, ModelsFile)
	if err != nil {
		return nil, err
	}
	models, err := ParseModels(modelsRaw)
	if err != nil {
		return nil, &ModelsError{PluginID: m.ID, Detail: err.Error()}
	}
	hash := ""
	for _, model := range models {
		if model.Name == decl.Model {
			if model.When != nil {
				return refuse("%s names model %q, which is needed only for some values of a setting; a source's model is always present", EmbeddingsFile, decl.Model)
			}
			hash = model.SHA256
		}
	}
	if hash == "" {
		return refuse("%s names model %q, which %s does not declare", EmbeddingsFile, decl.Model, ModelsFile)
	}
	name, err := toolName(m.ID, MethodEmbed)
	if err != nil {
		return nil, err
	}
	return &Embeddings{PluginID: m.ID, Embed: name, Decl: *decl, ModelSHA256: hash}, nil
}
