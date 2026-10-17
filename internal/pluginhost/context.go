package pluginhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const ContextMaxBytes = 4096
const ContextQueryBytes = 2048
const ContextRouteBytes = 512

type ContextResult struct {
	Text        string `json:"text"`
	DetailRoute string `json:"detail_route"`
}

func ReadContextResult(raw string) (ContextResult, error) {
	var out ContextResult
	if !utf8.ValidString(raw) || len(raw) > ContextMaxBytes {
		return out, fmt.Errorf("context result exceeds its UTF-8 byte bound")
	}
	var fields map[string]json.RawMessage
	if err := canonicaljson.DecodeStrict([]byte(raw), &fields); err != nil || len(fields) != 2 || fields["text"] == nil || fields["detail_route"] == nil {
		return out, fmt.Errorf("context result requires only text and detail_route")
	}
	if string(fields["text"]) == "null" || string(fields["detail_route"]) == "null" || json.Unmarshal(fields["text"], &out.Text) != nil || json.Unmarshal(fields["detail_route"], &out.DetailRoute) != nil {
		return ContextResult{}, fmt.Errorf("context text and detail_route must be strings")
	}
	if len(out.DetailRoute) > ContextRouteBytes || (strings.TrimSpace(out.Text) == "") != (strings.TrimSpace(out.DetailRoute) == "") {
		return ContextResult{}, fmt.Errorf("nonempty context requires a bounded detail route; empty context requires an empty route")
	}
	return out, nil
}

func validateContextRuntime(descs *descriptorSet, m *packagefmt.Manifest, runtime string, supervised bool) error {
	for op, d := range descs.ops {
		if !d.context {
			continue
		}
		unsupported := !supervised || runtime != "wasm_component" || servesSessionLane(m) || m.PluginFamily == "channel_adapter"
		for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
			unsupported = unsupported || decl.ID == ChannelInterfaceID || decl.ID == EmbeddingsInterfaceID
		}
		if unsupported {
			return &SchemaError{Operation: op, Err: fmt.Errorf("context requires a supervised nonresident wasm_component without channel or embedding interfaces; use a supervised host or retain the previous package")}
		}
	}
	return nil
}
