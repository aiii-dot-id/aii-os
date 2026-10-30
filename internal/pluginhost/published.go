package pluginhost

import (
	"fmt"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const MaxPublishedTools = 32

func (ap *ActivePlugin) PublishTool(spec broker.PublishedTool) (string, error) {
	if ap.superseded.Load() {
		return "", fmt.Errorf("plugin %s: this activation was superseded by an update; a draining release finishes what it was given and admits nothing new", ap.ID)
	}

	if !ap.admitted.Load() {
		return "", fmt.Errorf("plugin %s: this release is being admitted; publish once it serves", ap.ID)
	}
	if !tools.PrintableName(spec.Name) {
		return "", fmt.Errorf("name %q is not lowercase dotted segments of at most 64 bytes", spec.Name)
	}
	if spec.Summary == "" || len(spec.Summary) > tools.MaxSummaryBytes {
		return "", fmt.Errorf("tool %s: summary must be 1..%d bytes", spec.Name, tools.MaxSummaryBytes)
	}
	if !broker.KnownEffects(spec.Effects) || spec.Effects == "" {
		return "", fmt.Errorf("tool %s: effects %q is not a declared class", spec.Name, spec.Effects)
	}
	for _, c := range spec.Capabilities {
		if !ap.envelopeHas(c) {
			return "", fmt.Errorf("tool %s: capability %s is outside the plugin's signed envelope", spec.Name, c)
		}
	}
	confirms, err := publishedConfirms(spec)
	if err != nil {
		return "", err
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
		if err := checkInputSchemaSize(raw, ap.schemaBound); err != nil {
			return "", fmt.Errorf("tool %s: input schema: %w", spec.Name, err)
		}
	}
	name, err := toolName(ap.ID, "dyn."+spec.Name)
	if err != nil {
		return "", err
	}
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()

	if ap.superseded.Load() {
		return "", fmt.Errorf("plugin %s: this activation was superseded by an update; a draining release finishes what it was given and admits nothing new", ap.ID)
	}
	if !ap.isAuthorized() {
		return "", fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
	}

	if !ap.admitted.Load() {
		return "", fmt.Errorf("plugin %s: this release is being admitted; publish once it serves", ap.ID)
	}
	if len(ap.published) >= MaxPublishedTools {
		return "", fmt.Errorf("tool %s: %d tools are published; the ceiling is %d — withdraw one", spec.Name, len(ap.published), MaxPublishedTools)
	}
	if _, dup := ap.published[spec.Name]; dup {
		return "", fmt.Errorf("tool %s is already published; withdraw it first", spec.Name)
	}

	for _, n := range ap.ToolNames {
		if d, ok := ap.reg.Declared(n); ok && d.Operation == spec.Name && !d.Published {
			return "", fmt.Errorf("tool %s: the plugin's package declares an operation of that name; publish under another", spec.Name)
		}
	}
	desc := &opDescriptor{raw: raw, input: compiled, effects: spec.Effects, capabilities: append([]string(nil), spec.Capabilities...), capsDeclared: true, summary: spec.Summary, family: spec.Family,
		operatorConfirms: confirms, published: true, unconfirmed: strings.TrimSpace(spec.UnconfirmedByDesign)}

	t := ap.newOperationTool(name, spec.Name,
		tools.AuthorLine(ap.ID, spec.Summary, name)+fmt.Sprintf(" — published at run time by plugin %s (brokered under its signed envelope).", ap.ID),
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

func publishedConfirms(spec broker.PublishedTool) (bool, error) {
	reason := strings.TrimSpace(spec.UnconfirmedByDesign)
	if len(reason) > tools.MaxSummaryBytes {
		return false, fmt.Errorf("tool %s: unconfirmed_by_design must be at most %d bytes", spec.Name, tools.MaxSummaryBytes)
	}
	if broker.IsReadEffect(spec.Effects) {
		if reason != "" {
			return false, fmt.Errorf("tool %s: unconfirmed_by_design excuses nothing: a %s tool runs on the identity's call unless it asks for operator_confirms", spec.Name, spec.Effects)
		}
		return spec.OperatorConfirms, nil
	}
	if reason != "" && spec.OperatorConfirms {
		return false, fmt.Errorf("tool %s: operator_confirms and unconfirmed_by_design say opposite things; publish one of them", spec.Name)
	}
	if reason == "" && spec.UnconfirmedByDesign != "" {
		return false, fmt.Errorf("tool %s: unconfirmed_by_design gives no reason", spec.Name)
	}
	return reason == "", nil
}

func (ap *ActivePlugin) WithdrawTool(name string) error {
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()

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
