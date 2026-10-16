//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func StoreMaterializerForTest(w *plugstore.Writer) func(context.Context, Material, ModelFetcher) error {
	return func(ctx context.Context, m Material, fetch ModelFetcher) error {
		damaged := map[string]bool{}
		for _, e := range m.Damaged() {
			damaged[e] = true
		}
		for _, work := range m.Works() {
			if damaged[work.Entry] {
				if err := RepairEntry(ctx, w, work, fetch, m.Adoption(), nil); err != nil {
					return err
				}
			}
			if err := EnsureEntry(ctx, w, work, fetch, m.Adoption(), nil); err != nil {
				return err
			}
		}
		return nil
	}
}

func ModelMaterialForTest(store, id, version string, models ...ModelDecl) Material {
	var names []string
	for _, d := range models {
		if d.When == nil {
			names = append(names, d.Name)
		}
	}
	v := &ViewWork{ModelsSHA256: "sha256:" + strings.Repeat("1", 64), Names: names}
	dir, _ := plugstore.Store{Dir: store}.View(plugstore.ViewKey(v.ModelsSHA256, v.Names))
	return Material{PluginID: id, Version: version, Models: models, ModelsDir: dir, store: store, view: v}
}
