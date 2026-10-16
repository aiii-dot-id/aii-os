package continuity

import (
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type MaintenanceConfig struct {
	Enabled *bool `json:"enabled,omitempty"`

	BackupKeep int `json:"backup_keep,omitempty"`

	OnDemandSpacingSeconds int `json:"on_demand_spacing_seconds,omitempty"`
}

type Settings struct {
	Maintenance MaintenanceConfig

	Identity    Identity
	Compression store.CompressionOptimization
	WitnessURL  string

	WitnessPlatform string
}

type Identity struct {
	LedgerPath, DBPath, KeyPath string
}

func SnapshotKeyPath(identityKeyPath string) string {
	return escrow.SnapshotKeyPath(identityKeyPath)
}
