package packagefmt

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	reManifestID   = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(\.[a-z0-9]+([._-][a-z0-9]+)*)*$`)
	reSHA256       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	reVariantID    = regexp.MustCompile(`^[a-z0-9._-]+$`)
	reEntrypoint   = regexp.MustCompile(`^variants/[a-z0-9._-]+/.+`)
	reInterfaceID  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	reInterfaceRef = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+@[1-9][0-9]*$`)
	reCapability   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+(:.+)?$`)
	reXExtension   = regexp.MustCompile(`^x-[a-z0-9._-]+$`)
	// .
	// .
	// .
	// .
	rePredicateName = regexp.MustCompile(`^[a-z0-9._/-]+$`)
)

// .
// .
// .
// .
// .
// .
// .
const (
	PredicateClassFacility     = "facility"
	PredicateClassRuntime      = "runtime"
	PredicateClassDistribution = "distribution"
	PredicateClassBackend      = "backend"
	PredicateClassPermission   = "permission"
	PredicateClassTopology     = "topology"
)

// .
// .
const maxRequirementEntries = 16

// .
// .
var topLevelManifestKeys = map[string]bool{
	"kind": true, "id": true, "version": true, "package_hash": true,
	"aiios_min_version": true, "aiios_max_exclusive_version": true,
	"title": true, "description": true, "publisher": true,
	"homepage": true, "license": true, "asset_type": true,
	"plugin_family": true, "bbb_protocol_version": true,
	"package_mode": true, "interfaces": true, "capability_envelope": true,
	"requirements": true, "default_variant": true,
	"operator_projection": true, "operation_descriptors": true,
	"heartbeat": true, "variants": true,
}

// .
type Manifest struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Version      string `json:"version"`
	PackageHash  string `json:"package_hash"`
	AssetType    string `json:"asset_type"`
	PluginFamily string `json:"plugin_family"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	BBBProtocolVersion int             `json:"bbb_protocol_version"`
	Publisher          string          `json:"publisher"`
	Interfaces         *InterfaceSet   `json:"interfaces"`
	CapabilityEnvelope json.RawMessage `json:"capability_envelope"`
	Requirements       *Requirements   `json:"requirements"`
	DefaultVariant     string          `json:"default_variant"`
	Variants           []Variant       `json:"variants"`
}

// .
// .
// .
// .
// .
// .
type Requirements struct {
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

// .
type InterfaceSet struct {
	Core     []InterfaceDecl `json:"core"`
	Optional []InterfaceDecl `json:"optional"`
}

// .
type InterfaceDecl struct {
	ID         string   `json:"id"`
	Version    int      `json:"version"`
	SchemaHash string   `json:"schema_hash"`
	Methods    []string `json:"methods"`
}

// .
// .
type Variant struct {
	VariantID           string          `json:"variant_id"`
	Platform            string          `json:"platform"`
	Arch                string          `json:"arch"`
	Topology            string          `json:"topology"`
	ExecutionRuntime    string          `json:"execution_runtime"`
	AdmissionProfile    string          `json:"admission_profile"`
	Entrypoint          string          `json:"entrypoint"`
	ArtifactHash        string          `json:"artifact_hash"`
	Implements          json.RawMessage `json:"implements"`
	VariantCapabilities json.RawMessage `json:"variant_capabilities"`
	Requirements        *Requirements   `json:"requirements"`
}

// .
// .
// .
// .
func SplitPredicate(p string) (class, name string, ok bool) {
	i := strings.IndexByte(p, ':')
	if i <= 0 || i == len(p)-1 {
		return "", "", false
	}
	return p[:i], p[i+1:], true
}

// .
// .
// .
// .
// .
// .
func (m *Manifest) RequiredPredicates(v *Variant) []string {
	var out []string
	if m.Requirements != nil {
		out = append(out, m.Requirements.Required...)
	}
	if v != nil && v.Requirements != nil {
		out = append(out, v.Requirements.Required...)
	}
	return out
}

// .
// .
// .
// .
// .
// .
func (m *Manifest) BackendDeclarations(v *Variant) []string {
	var out []string
	seen := map[string]bool{}
	collect := func(r *Requirements) {
		if r == nil {
			return
		}
		for _, p := range append(append([]string{}, r.Required...), r.Optional...) {
			if class, _, ok := SplitPredicate(p); ok && class == PredicateClassBackend && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	collect(m.Requirements)
	if v != nil {
		collect(v.Requirements)
	}
	return out
}

func enumHas(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

// .
func parseManifest(raw []byte) (*Manifest, *Error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fail(ReasonManifestInvalid, "manifest", "manifest.json is not a JSON object: %v", err)
	}
	for key := range keys {
		if !topLevelManifestKeys[key] && !reXExtension.MatchString(key) {
			// .
			// .
			// .
			return nil, fail(ReasonManifestInvalid, "manifest", "unknown top-level field %q (manifest top level is closed)", key)
		}
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fail(ReasonManifestInvalid, "manifest", "manifest fields mistyped: %v", err)
	}

	// .
	if _, ok := keys["kind"]; !ok {
		return nil, fail(ReasonManifestInvalid, "manifest", "required field kind is missing")
	}
	if !enumHas(m.Kind, "asset", "plugin") {
		return nil, fail(ReasonManifestInvalid, "manifest", "kind must be asset or plugin, got %q", m.Kind)
	}
	if !reManifestID.MatchString(m.ID) {
		return nil, fail(ReasonManifestInvalid, "manifest", "id %q does not match the manifest id grammar", m.ID)
	}
	if m.Version == "" {
		return nil, fail(ReasonManifestInvalid, "manifest", "required field version is missing or empty")
	}
	if !reSHA256.MatchString(m.PackageHash) {
		return nil, fail(ReasonManifestInvalid, "manifest", "package_hash must be sha256:<64-lowercase-hex>")
	}

	// .
	// .
	// .
	if verr := validateRequirements(m.Requirements, "requirements"); verr != nil {
		return nil, verr
	}
	if m.DefaultVariant != "" && !reVariantID.MatchString(m.DefaultVariant) {
		return nil, fail(ReasonManifestInvalid, "manifest", "default_variant %q does not match the variant grammar", m.DefaultVariant)
	}

	switch m.Kind {
	case "asset":
		if verr := validateAssetManifest(&m, keys); verr != nil {
			return nil, verr
		}
	case "plugin":
		if verr := validatePluginManifest(&m, keys); verr != nil {
			return nil, verr
		}
	}
	return &m, nil
}

// .
// .
// .
// .
// .
// .
// .
func validateRequirements(r *Requirements, where string) *Error {
	if r == nil {
		return nil
	}
	for _, list := range []struct {
		label   string
		entries []string
	}{
		{where + ".required", r.Required},
		{where + ".optional", r.Optional},
	} {
		if len(list.entries) > maxRequirementEntries {
			return fail(ReasonManifestInvalid, "manifest", "%s declares more than %d entries", list.label, maxRequirementEntries)
		}
		seen := map[string]bool{}
		for _, entry := range list.entries {
			class, name, ok := SplitPredicate(entry)
			if !ok || !rePredicateName.MatchString(name) {
				return fail(ReasonManifestInvalid, "manifest", "%s entry %q does not match the requirement grammar class:name", list.label, entry)
			}
			switch class {
			case PredicateClassFacility, PredicateClassRuntime, PredicateClassDistribution,
				PredicateClassBackend, PredicateClassPermission, PredicateClassTopology:
			default:
				return fail(ReasonManifestInvalid, "manifest", "%s entry %q uses unknown predicate class %q — the class set is closed, refusing fail-closed", list.label, entry, class)
			}
			if seen[entry] {
				return fail(ReasonManifestInvalid, "manifest", "%s entry %q duplicated", list.label, entry)
			}
			seen[entry] = true
		}
	}
	return nil
}

// .
// .
func validateAssetManifest(m *Manifest, keys map[string]json.RawMessage) *Error {
	if !enumHas(m.AssetType, "prompt_pack", "static_template", "skill", "metadata_blob") {
		return fail(ReasonManifestInvalid, "manifest", "asset manifests require a valid asset_type, got %q", m.AssetType)
	}
	for _, forbidden := range []string{
		"plugin_family", "bbb_protocol_version", "interfaces", "capability_envelope",
		"operator_projection", "operation_descriptors", "heartbeat", "variants",
	} {
		if _, present := keys[forbidden]; present {
			return fail(ReasonManifestInvalid, "manifest", "asset manifests forbid %q", forbidden)
		}
	}
	return nil
}

// .
// .
func validatePluginManifest(m *Manifest, keys map[string]json.RawMessage) *Error {
	if _, present := keys["asset_type"]; present {
		return fail(ReasonManifestInvalid, "manifest", "plugin manifests forbid asset_type")
	}
	if !enumHas(m.PluginFamily, "channel_adapter", "provider_bridge", "tool_bridge", "voice_interface") {
		return fail(ReasonManifestInvalid, "manifest", "plugin manifests require a valid plugin_family, got %q", m.PluginFamily)
	}
	if m.BBBProtocolVersion != 2 {
		return fail(ReasonManifestInvalid, "manifest", "bbb_protocol_version must be 2, got %d", m.BBBProtocolVersion)
	}

	if m.Interfaces == nil || len(m.Interfaces.Core) == 0 {
		return fail(ReasonManifestInvalid, "manifest", "plugin manifests require interfaces.core with at least one declaration")
	}
	if len(m.Interfaces.Core) > 16 || len(m.Interfaces.Optional) > 16 {
		return fail(ReasonManifestInvalid, "manifest", "interfaces declare more than 16 entries")
	}
	for _, decl := range append(append([]InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		if !reInterfaceID.MatchString(decl.ID) {
			return fail(ReasonManifestInvalid, "manifest", "interface id %q does not match the interface grammar", decl.ID)
		}
		if decl.Version < 1 {
			return fail(ReasonManifestInvalid, "manifest", "interface %s version must be >= 1", decl.ID)
		}
		if !reSHA256.MatchString(decl.SchemaHash) {
			return fail(ReasonManifestInvalid, "manifest", "interface %s schema_hash must be sha256:<64-lowercase-hex>", decl.ID)
		}
		if len(decl.Methods) == 0 || len(decl.Methods) > 16 {
			return fail(ReasonManifestInvalid, "manifest", "interface %s must declare 1..16 methods", decl.ID)
		}
	}

	if _, present := keys["capability_envelope"]; !present {
		return fail(ReasonManifestInvalid, "manifest", "plugin manifests require capability_envelope")
	}
	if _, verr := parseCapabilityList(m.CapabilityEnvelope, "capability_envelope", 32); verr != nil {
		return verr
	}

	if len(m.Variants) == 0 {
		return fail(ReasonManifestInvalid, "manifest", "plugin manifests require at least one variant")
	}
	seenVariant := map[string]bool{}
	for i := range m.Variants {
		if verr := validateVariant(&m.Variants[i]); verr != nil {
			return verr
		}
		if seenVariant[m.Variants[i].VariantID] {
			return fail(ReasonManifestInvalid, "manifest", "duplicate variant_id %q", m.Variants[i].VariantID)
		}
		seenVariant[m.Variants[i].VariantID] = true
	}
	return nil
}

func validateVariant(v *Variant) *Error {
	if !reVariantID.MatchString(v.VariantID) {
		return fail(ReasonManifestInvalid, "manifest", "variant_id %q does not match the variant grammar", v.VariantID)
	}
	if !enumHas(v.Platform, "linux", "macos", "windows", "android", "ios") {
		return fail(ReasonManifestInvalid, "manifest", "variant %s platform %q is not a supported platform", v.VariantID, v.Platform)
	}
	if !enumHas(v.Arch, "x86_64", "arm64") {
		return fail(ReasonManifestInvalid, "manifest", "variant %s arch %q is not a supported arch", v.VariantID, v.Arch)
	}
	if !enumHas(v.Topology, "full_identity_host", "mobile_app_host") {
		return fail(ReasonManifestInvalid, "manifest", "variant %s topology %q is not a supported topology", v.VariantID, v.Topology)
	}
	if !enumHas(v.ExecutionRuntime, "service_process", "wasm_component", "wasm_aot_component", "inprocess_component", "native_t3_component") {
		return fail(ReasonManifestInvalid, "manifest", "variant %s execution_runtime %q is not a supported runtime", v.VariantID, v.ExecutionRuntime)
	}
	if !enumHas(v.AdmissionProfile, "standard", "wasm_sandbox", "certified_native", "platform_reserved") {
		return fail(ReasonManifestInvalid, "manifest", "variant %s admission_profile %q is not a supported profile", v.VariantID, v.AdmissionProfile)
	}
	if !reEntrypoint.MatchString(v.Entrypoint) {
		return fail(ReasonManifestInvalid, "manifest", "variant %s entrypoint %q must live under variants/", v.VariantID, v.Entrypoint)
	}
	if !reSHA256.MatchString(v.ArtifactHash) {
		return fail(ReasonManifestInvalid, "manifest", "variant %s artifact_hash must be sha256:<64-lowercase-hex>", v.VariantID)
	}

	// .
	// .
	pairingOK := false
	switch v.ExecutionRuntime {
	case "service_process":
		pairingOK = enumHas(v.AdmissionProfile, "standard", "certified_native")
	case "wasm_component", "wasm_aot_component":
		pairingOK = v.AdmissionProfile == "wasm_sandbox"
	case "inprocess_component":
		pairingOK = v.AdmissionProfile == "certified_native" &&
			enumHas(v.Platform, "android", "ios") && v.Topology == "mobile_app_host"
	case "native_t3_component":
		pairingOK = v.AdmissionProfile == "platform_reserved"
	}
	if !pairingOK {
		return fail(ReasonManifestInvalid, "manifest", "variant %s pairs runtime %q with profile %q, which the schema forbids", v.VariantID, v.ExecutionRuntime, v.AdmissionProfile)
	}

	var impl struct {
		Core     []string `json:"core"`
		Optional []string `json:"optional"`
	}
	if len(v.Implements) == 0 {
		return fail(ReasonManifestInvalid, "manifest", "variant %s requires implements", v.VariantID)
	}
	if err := json.Unmarshal(v.Implements, &impl); err != nil {
		return fail(ReasonManifestInvalid, "manifest", "variant %s implements mistyped: %v", v.VariantID, err)
	}
	if len(impl.Core) == 0 || len(impl.Core) > 16 || len(impl.Optional) > 16 {
		return fail(ReasonManifestInvalid, "manifest", "variant %s implements.core must declare 1..16 interface refs", v.VariantID)
	}
	for _, ref := range append(append([]string{}, impl.Core...), impl.Optional...) {
		if !reInterfaceRef.MatchString(ref) {
			return fail(ReasonManifestInvalid, "manifest", "variant %s implements ref %q does not match id@version", v.VariantID, ref)
		}
	}

	if len(v.VariantCapabilities) == 0 {
		return fail(ReasonManifestInvalid, "manifest", "variant %s requires variant_capabilities", v.VariantID)
	}
	if _, verr := parseCapabilityList(v.VariantCapabilities, fmt.Sprintf("variant %s variant_capabilities", v.VariantID), 32); verr != nil {
		return verr
	}
	if verr := validateRequirements(v.Requirements, fmt.Sprintf("variant %s requirements", v.VariantID)); verr != nil {
		return verr
	}
	return nil
}

func parseCapabilityList(raw json.RawMessage, field string, maxItems int) ([]string, *Error) {
	var caps []string
	if err := json.Unmarshal(raw, &caps); err != nil {
		return nil, fail(ReasonManifestInvalid, "manifest", "%s mistyped: %v", field, err)
	}
	if len(caps) > maxItems {
		return nil, fail(ReasonManifestInvalid, "manifest", "%s declares more than %d entries", field, maxItems)
	}
	seen := map[string]bool{}
	for _, c := range caps {
		if !reCapability.MatchString(c) {
			return nil, fail(ReasonManifestInvalid, "manifest", "%s entry %q does not match the capability grammar", field, c)
		}
		if seen[c] {
			return nil, fail(ReasonManifestInvalid, "manifest", "%s entry %q duplicated", field, c)
		}
		seen[c] = true
	}
	return caps, nil
}
