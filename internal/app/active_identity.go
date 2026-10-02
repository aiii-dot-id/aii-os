package app

func (a *App) activeIdentity(configured IdentityConfig) IdentityConfig {
	id := configured
	if a.ledger != nil {
		id.LedgerPath = a.ledger.Path()
	}
	if v := a.databaseView.Load(); v != nil && v.path != "" {
		id.DBPath = v.path
	}
	if origin := a.keyOrigin.Load(); origin != nil {
		id.KeyPath = *origin
	}
	return id
}

func (a *App) activeConfig(cfg Config) Config {
	cfg.Identity = a.activeIdentity(cfg.Identity)
	return cfg
}
