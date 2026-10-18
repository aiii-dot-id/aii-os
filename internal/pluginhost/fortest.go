package pluginhost

import (
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

func (r *Activation) AdmitForTest() { r.admitted.Store(true) }

func (ap *ActivePlugin) DeclareChecksForTest(v *packagefmt.Validation, checked *pluginfacility.Checks) {
	ap.validation = v
	ap.checked.Store(checked)
}

func (ap *ActivePlugin) RunsFromForTest(m Material) {
	ap.material, ap.ModelsDir = m, m.ModelsDir
}
