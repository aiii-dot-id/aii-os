package packagefmt

import (
	"encoding/json"
	"fmt"
)

const VariantPreferenceMinHost = "0.1.12"

const RuntimeExtentMinHost = "0.1.14"

func ValidateVariantPreference(raw json.RawMessage, minHost string, variants map[string]bool) error {
	var order []string
	if json.Unmarshal(raw, &order) != nil || len(order) == 0 || len(order) > 64 {
		return fmt.Errorf("variant_preference must be an array of 1..64 variant IDs; omit it for default selection")
	}
	if !ValidHostBound(minHost) || CompareHostBounds(minHost, VariantPreferenceMinHost) < 0 {
		return fmt.Errorf("variant_preference requires aiios_min_version >= %s", VariantPreferenceMinHost)
	}
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		if !variants[id] || seen[id] {
			return fmt.Errorf("variant_preference names unknown or repeated variant %q", id)
		}
		seen[id] = true
	}
	if len(seen) != len(variants) {
		return fmt.Errorf("variant_preference must name every declared variant exactly once")
	}
	return nil
}
