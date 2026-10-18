package pluginhost

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

type ServingSet struct {
	PackageHash, ManifestHash string
	Variant                   string
	Profile                   *AcceleratorProfile
}

func (s *ServingSet) profile() *AcceleratorProfile {
	if s == nil {
		return nil
	}
	return s.Profile
}

func (s *ServingSet) same(res *packagefmt.Result) bool {
	return s != nil && s.PackageHash == res.PackageHash && s.ManifestHash == res.ManifestHash
}

func installedInPlaceOf(res *packagefmt.Result, host hostContext, order []string, preferred string) string {
	if s := host.sets[preferred]; s == nil || s.need() == 0 {
		return ""
	}
	for _, id := range order {
		v, s := variantByID(res.Manifest, id), host.sets[id]
		if id == preferred || v == nil || s == nil || s.need() > 0 {
			continue
		}
		if len(laneMissing(res, host, v)) == 0 && len(host.refusals(id)) == 0 {
			return id
		}
	}
	return ""
}

func servesWhileWords(better string) string {
	return fmt.Sprintf("serves while %s arrives: %s is the first in the package's order that this machine can run, and it still has material to fetch; everything this set needs is on disk", better, better)
}

func arrivingWords(serving string) string {
	return fmt.Sprintf("arriving: its material is fetched in the background while %s serves, and it takes over once that material is verified and it has started, reported ready and answered healthy", serving)
}

type NoBetterSetError struct {
	PluginID, Version, Serving, Needs string
	Sets                              []pluginfacility.SetVerdict
}

func (e *NoBetterSetError) Error() string {
	return fmt.Sprintf("pluginhost: %s %s: nothing to move to — %s, serving now, is the component set this machine would run of these bytes", e.PluginID, e.Version, e.Serving)
}

func (e *NoBetterSetError) Judgment() *pluginfacility.Selection {
	return &pluginfacility.Selection{Variant: e.Serving, Sets: e.Sets, Needs: e.Needs}
}
