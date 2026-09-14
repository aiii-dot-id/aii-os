package app

import "encoding/json"

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
