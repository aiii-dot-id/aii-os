package pluginhost

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
	"fmt"
	"regexp"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
)

// .
const MaxPublishedTools = 32

var rePublishedName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// .
func (ap *ActivePlugin) PublishTool(spec broker.PublishedTool) (string, error) {
	if ap.superseded.Load() {
		return "", fmt.Errorf("plugin %s: this activation was superseded by an update; a draining release finishes what it was given and admits nothing new", ap.ID)
	}
	// .
	// .
	// .
	// .
	if !ap.admitted.Load() {
		return "", fmt.Errorf("plugin %s: this release is being admitted; publish once it serves", ap.ID)
	}
	if !rePublishedName.MatchString(spec.Name) || len(spec.Name) > 64 {
		return "", fmt.Errorf("name %q is not lowercase dotted segments of at most 64 bytes", spec.Name)
	}
	if spec.Summary == "" || len(spec.Summary) > 256 {
		return "", fmt.Errorf("tool %s: summary must be 1..256 bytes", spec.Name)
	}
	if !broker.KnownEffects(spec.Effects) || spec.Effects == "" {
		return "", fmt.Errorf("tool %s: effects %q is not a declared class", spec.Name, spec.Effects)
	}
	for _, c := range spec.Capabilities {
		if !ap.envelopeHas(c) {
			return "", fmt.Errorf("tool %s: capability %s is outside the plugin's signed envelope", spec.Name, c)
		}
	}
	var compiled *jsonschema.Schema
	raw := spec.Input
	if raw != nil {
		if err := normalizeRequired(raw); err != nil {
			return "", fmt.Errorf("tool %s: input schema: %v", spec.Name, err)
		}
		var err error
		if compiled, err = jsonschema.Compile(raw); err != nil {
			return "", fmt.Errorf("tool %s: input schema: %v", spec.Name, err)
		}
	}
	name, err := toolName(ap.ID, "dyn."+spec.Name)
	if err != nil {
		return "", err
	}
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	// .
	// .
	// .
	// .
	// .
	// .
	if ap.superseded.Load() {
		return "", fmt.Errorf("plugin %s: this activation was superseded by an update; a draining release finishes what it was given and admits nothing new", ap.ID)
	}
	if !ap.isAuthorized() {
		return "", fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
	}
	// .
	if !ap.admitted.Load() {
		return "", fmt.Errorf("plugin %s: this release is being admitted; publish once it serves", ap.ID)
	}
	if len(ap.published) >= MaxPublishedTools {
		return "", fmt.Errorf("tool %s: %d tools are published; the ceiling is %d — withdraw one", spec.Name, len(ap.published), MaxPublishedTools)
	}
	if _, dup := ap.published[spec.Name]; dup {
		return "", fmt.Errorf("tool %s is already published; withdraw it first", spec.Name)
	}
	desc := &opDescriptor{raw: raw, input: compiled, effects: spec.Effects, capabilities: append([]string(nil), spec.Capabilities...), capsDeclared: true, summary: spec.Summary, family: spec.Family}
	// .
	// .
	// .
	// .
	// .
	// .
	t := ap.newOperationTool(name, spec.Name,
		spec.Summary+fmt.Sprintf(" — published at run time by plugin %s (brokered under its signed envelope).", ap.ID),
		desc, desc.discovery(ap.ID, ap.Version, ap.Tier.String(), spec.Name))
	if err := ap.reg.RegisterDynamic(t, ap.ID); err != nil {
		return "", fmt.Errorf("tool %s: %v", spec.Name, err)
	}
	if ap.published == nil {
		ap.published = map[string]string{}
	}
	ap.published[spec.Name] = name
	ap.ToolNames = append(ap.ToolNames, name)
	return name, nil
}

// .
func (ap *ActivePlugin) WithdrawTool(name string) error {
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	// .
	// .
	// .
	// .
	if ap.superseded.Load() {
		return fmt.Errorf("plugin %s: this activation was superseded by an update; withdrawing now could deregister the successor's tool", ap.ID)
	}
	registry, ok := ap.published[name]
	if !ok {
		return fmt.Errorf("tool %s is not published by this activation", name)
	}
	ap.reg.Deregister(registry)
	delete(ap.published, name)
	for i, n := range ap.ToolNames {
		if n == registry {
			ap.ToolNames = append(ap.ToolNames[:i:i], ap.ToolNames[i+1:]...)
			break
		}
	}
	return nil
}

// .
func (ap *ActivePlugin) Published() []string {
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	out := make([]string, 0, len(ap.published))
	for n := range ap.published {
		out = append(out, n)
	}
	return out
}

func (ap *ActivePlugin) envelopeHas(capability string) bool {
	for _, c := range ap.envelope {
		if c == capability {
			return true
		}
	}
	return false
}

var _ sync.Locker = (*sync.Mutex)(nil)
