package vulkancap

import "path/filepath"

const Helper = "aii-vulkan-probe"

func HelperPath(exe string) string { return filepath.Join(filepath.Dir(exe), Helper) }

const Subcommand = "_hardware-vulkan"

type Capacity struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
}
