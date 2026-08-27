package app

import "encoding/json"

// SalvageIdentityInto recovers the one thing an unreadable config must
// never take with it: where the identity lives, and what they are
// called. The full schema stays strict (DisallowUnknownFields —
// refusing to RUN on a config we do not understand is the contract),
// but quarantining the file used to discard its identity block with it,
// re-pointing the boot at defaults that may hold nothing — the D02
// residual fork (Sev 2026-08-26). This is a tolerant decode of ONLY
// the identity block: unparseable bytes salvage nothing, and the
// chooseBoot evidence scan still guards the firstboot behind it.
//
// Adopted values are persisted through saveConfig — the one
// persistence owner — so disk and memory do not diverge (§5.4). A
// persist failure is returned for the caller to act on, never
// swallowed: values that hold for one boot only are a deferred fork.
func SalvageIdentityInto(raw []byte, cfg *Config) (adopted []string, persistErr error) {
	if cfg == nil {
		return nil, nil
	}
	var probe struct {
		Identity IdentityConfig `json:"identity"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return nil, nil
	}
	take := func(field, from string, into *string) {
		if from != "" && from != *into {
			*into = from
			adopted = append(adopted, field+"="+from)
		}
	}
	take("identity.name", probe.Identity.Name, &cfg.Identity.Name)
	take("identity.ledger_path", probe.Identity.LedgerPath, &cfg.Identity.LedgerPath)
	take("identity.db_path", probe.Identity.DBPath, &cfg.Identity.DBPath)
	take("identity.key_path", probe.Identity.KeyPath, &cfg.Identity.KeyPath)
	if len(adopted) == 0 {
		return nil, nil
	}
	if _, err := saveConfig(cfg); err != nil {
		return adopted, err
	}
	return adopted, nil
}
