package pluginhost

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

type reason struct {
	log, words, remedy string
	momentary          bool
}

func logs(rs []reason) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.log
	}
	return out
}

func runtimeLimits(d RuntimeDecl, operator packagefmt.TreeLimits) packagefmt.TreeLimits {
	l := operator
	if l.MaxCompressedBytes <= 0 {
		l.MaxCompressedBytes = d.Size
	}
	if l.MaxInstalledBytes <= 0 {
		l.MaxInstalledBytes = d.InstalledBytes
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.InstalledBytes
	}
	defaults := packagefmt.DefaultTreeLimits
	if l.MaxFiles <= 0 {
		l.MaxFiles = defaults.MaxFiles
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = defaults.MaxDepth
	}
	if l.MaxInventoryBytes <= 0 {
		l.MaxInventoryBytes = defaults.MaxInventoryBytes
	}
	return l
}

type setMaterial struct {
	archive, tree, models int64
	sources               map[string]int64

	where      string
	free       int64
	unmeasured error

	ceilings []runtimeCeiling
}

func (s *setMaterial) need() int64 { return s.archive + s.tree + s.models }

func (s *setMaterial) fetch() int64 {
	var n int64
	for _, b := range s.sources {
		n += b
	}
	return n
}

func materialOf(pkgPath string, res *packagefmt.Result, held map[string][]byte, v *packagefmt.Variant, profile *AcceleratorProfile, rt *RuntimeDecl, opts *Options) (*setMaterial, error) {
	m := res.Manifest
	s := &setMaterial{sources: map[string]int64{}}
	if opts.StoreDir == "" {
		return s, nil
	}
	store := plugstore.Store{Dir: opts.StoreDir}
	adopt := opts.adoptionFor(m.ID)
	var stored map[string]any
	if opts.Settings != nil {
		stored = opts.Settings(m.ID)
	}
	models, _, err := loadModels(pkgPath, res, held, m, profile, stored)
	if err != nil {
		return nil, err
	}
	for i, st := range (Material{store: opts.StoreDir}).modelStatuses(models) {
		if st.Stored {
			continue
		}
		left := st.Size - min(max(st.Partial, 0), st.Size)
		s.models += left
		if !adoptable(adopt.ModelDirs, models[i]) {
			s.sources[sourceHost(models[i].URL)] += left
		}
	}
	if rt != nil {
		s.ceilings = runtimeDeclarationRefusals(*rt, opts.RuntimeLimits)
		key := plugstore.RootKey(v.VariantID, rt.InventorySHA256, filepath.Base(v.Entrypoint), res.FileDigests[v.Entrypoint])
		if root, _ := store.Root(key); !isDir(root) {
			archive, _ := store.Archive(rt.SHA256)
			var have int64
			if fi, err := os.Stat(archive); err == nil && fi.Mode().IsRegular() && fi.Size() == rt.Size {
				have = rt.Size
			} else if fi, err := os.Stat(archive + plugstore.PartialSuffix); err == nil && fi.Mode().IsRegular() && fi.Size() <= rt.Size {
				have = fi.Size()
			}
			s.archive, s.tree = rt.Size-have, rt.InstalledBytes
			if !archiveAdoptable(adopt.ArchiveDirs, rt) {
				s.sources[sourceHost(rt.URL)] += rt.Size - have
			}
		}
	}
	if s.need() == 0 {
		return s, nil
	}
	measure := opts.FreeDisk
	if measure == nil {
		measure = freeDisk
	}
	s.where = opts.StoreDir
	s.free, s.unmeasured = measure(opts.StoreDir)
	return s, nil
}

func adoptable(dirs []string, d ModelDecl) bool {
	for _, dir := range dirs {
		if fi, err := os.Lstat(modelFile(dir, d)); err == nil && fi.Mode().IsRegular() && fi.Size() == d.Size {
			return true
		}
	}
	return false
}

func archiveAdoptable(dirs []string, rt *RuntimeDecl) bool {
	for _, dir := range dirs {
		if fi, err := os.Lstat(filepath.Join(dir, rt.SHA256+archiveSuffix)); err == nil && fi.Mode().IsRegular() && fi.Size() == rt.Size {
			return true
		}
	}
	return false
}

func sourceHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.ToLower(u.Host)
}

func (s *setMaterial) refusals() []reason {
	var out []reason
	for _, c := range s.ceilings {
		out = append(out, c.reason())
	}
	need := s.need()
	switch {
	case need == 0:
	case s.unmeasured != nil:
		out = append(out, reason{momentary: true,
			log:   fmt.Sprintf("free disk at %s is unknown: %v", s.where, s.unmeasured),
			words: fmt.Sprintf("it needs %s more on disk, and the free space on the volume holding %s could not be measured: %v", humanBytes(need), s.where, s.unmeasured)})
	case need > s.free-diskMargin:
		short := need - max(s.free-diskMargin, 0)
		out = append(out, reason{momentary: true,
			log: fmt.Sprintf("disk needs %d more bytes at %s, %d free less the %d-byte margin", need, s.where, s.free, diskMargin),
			words: fmt.Sprintf("needs %s more on disk at its peak (%s), beside everything already there; %s is free on the volume holding %s, and the host keeps %s of that free for the identity's own records",
				humanBytes(need), s.parts(), humanBytes(s.free), s.where, humanBytes(diskMargin)),
			remedy: fmt.Sprintf("free %s on that volume", humanBytes(short))})
	}
	return out
}

func (s *setMaterial) parts() string {
	var p []string
	if s.archive > 0 {
		p = append(p, "runtime archive "+humanBytes(s.archive))
	}
	if s.tree > 0 {
		p = append(p, "installed runtime "+humanBytes(s.tree))
	}
	if s.models > 0 {
		p = append(p, "models "+humanBytes(s.models))
	}
	return strings.Join(p, ", ")
}

func (s *setMaterial) storageWords() string {
	need := s.need()
	switch {
	case need == 0:
		return "everything it needs is on disk already"
	case s.unmeasured != nil:
		return fmt.Sprintf("needs %s more on disk; the free space on the volume holding %s could not be measured: %v", humanBytes(need), s.where, s.unmeasured)
	}
	return fmt.Sprintf("needs %s more on disk at its peak (%s); %s free on the volume holding %s", humanBytes(need), s.parts(), humanBytes(s.free), s.where)
}

func (s *setMaterial) bandwidthWords(rate func(host string) (float64, bool)) string {
	total := s.fetch()
	if total == 0 {
		return "nothing to download"
	}
	hosts := make([]string, 0, len(s.sources))
	for h, n := range s.sources {
		if n > 0 {
			hosts = append(hosts, h)
		}
	}
	slices.Sort(hosts)
	head := fmt.Sprintf("%s to download from %s", humanBytes(total), strings.Join(hosts, " and "))
	var seconds float64
	var unmeasured []string
	var only float64
	for _, h := range hosts {
		r, ok := rate(h)
		if !ok {
			unmeasured = append(unmeasured, h)
			continue
		}
		seconds += float64(s.sources[h]) / r
		only = r
	}
	if len(unmeasured) == len(hosts) {
		return head + "; the speed of this link is not yet measured"
	}
	if len(unmeasured) > 0 {
		return head + "; the speed of the link to " + strings.Join(unmeasured, " and ") + " is not yet measured"
	}
	eta := durationWords(time.Duration(seconds * float64(time.Second)))
	if len(hosts) == 1 {
		return fmt.Sprintf("%s; about %s at the %s/s this host measured from there", head, eta, humanBytes(int64(only)))
	}
	return fmt.Sprintf("%s; about %s at the speeds this host measured from them", head, eta)
}

func durationWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", max(int(d.Round(time.Second)/time.Second), 1))
	case d < 90*time.Minute:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
	return fmt.Sprintf("%.1f days", d.Hours()/24)
}

func resourcesWords(p *AcceleratorProfile, a pluginfacility.Availability, judged []reason) string {
	if p == nil {
		return "judged when it starts: the package states no reservation for this set before then, and admission weighs it at start"
	}
	if len(judged) > 0 {
		w := make([]string, len(judged))
		for i, r := range judged {
			w[i] = r.words
		}
		return strings.Join(w, "; ")
	}
	parts := []string{fmt.Sprintf("needs %s of memory (%s free now)", humanBytes(p.MemoryBytes), humanBytes(a.HostAvailable))}
	if p.DeviceMemoryBytes != nil && *p.DeviceMemoryBytes > 0 {
		for _, name := range p.RequiredAccelerators {
			d := a.Devices[name]
			parts = append(parts, fmt.Sprintf("%s of the %s device's memory (%s of %s free now)", humanBytes(*p.DeviceMemoryBytes), name, humanBytes(d.Available), humanBytes(d.Total)))
		}
	}
	return "fits: " + strings.Join(parts, " and ")
}

func verdicts(res *packagefmt.Result, host hostContext, order []string, chosen, why, arriving string, rate func(string) (float64, bool)) []pluginfacility.SetVerdict {
	var out []pluginfacility.SetVerdict
	for _, id := range order {
		s := host.sets[id]
		v := variantByID(res.Manifest, id)
		if s == nil || v == nil {
			continue
		}
		verdict := pluginfacility.SetVerdict{Variant: id, Storage: s.storageWords(), Bandwidth: s.bandwidthWords(rate), Chosen: id == chosen}
		var profile *AcceleratorProfile
		var capacity []reason
		if r := host.resources; r != nil {
			if p, ok := r.profiles[id]; ok {
				profile = &p
				fixed, momentary := capacityRefusals(p, r.available, r.policy)
				capacity = append(fixed, momentary...)
			}
		}
		verdict.Resources = resourcesWords(profile, availableOf(host), capacity)
		switch {
		case verdict.Chosen:
			verdict.Why = why
		case id == arriving:
			verdict.Arriving, verdict.Why = true, arrivingWords(chosen)
		default:
			for _, w := range laneMissing(res, host, v) {
				verdict.Reasons = append(verdict.Reasons, pluginfacility.Reason{Words: w})
			}
			for _, r := range host.refusals(id) {
				verdict.Reasons = append(verdict.Reasons, pluginfacility.Reason{Words: r.words, Remedy: r.remedy})
			}
			switch {
			case len(verdict.Reasons) > 0 || chosen == "":
			case arriving != "":
				verdict.Reasons = append(verdict.Reasons, pluginfacility.Reason{Words: fmt.Sprintf("the package prefers %s, which this machine can run; %s serves until it arrives", arriving, chosen)})
			default:
				verdict.Reasons = append(verdict.Reasons, pluginfacility.Reason{Words: fmt.Sprintf("the package prefers %s, which this machine can run", chosen)})
			}
		}
		out = append(out, verdict)
	}
	return out
}

func availableOf(h hostContext) pluginfacility.Availability {
	if h.resources == nil {
		return pluginfacility.Availability{}
	}
	return h.resources.available
}

func variantByID(m *packagefmt.Manifest, id string) *packagefmt.Variant {
	for i := range m.Variants {
		if m.Variants[i].VariantID == id {
			return &m.Variants[i]
		}
	}
	return nil
}

func nativeOrder(m *packagefmt.Manifest, host hostContext) []string {
	ids := m.VariantPreference
	if ids == nil {
		for _, v := range m.Variants {
			ids = append(ids, v.VariantID)
		}
	}
	var out []string
	for _, id := range ids {
		v := variantByID(m, id)
		if v != nil && v.ExecutionRuntime == "native_t3_component" && v.Platform == host.platform && v.Arch == host.arch && v.Topology == host.topology {
			out = append(out, id)
		}
	}
	return out
}

func ownBound(err error, operator packagefmt.TreeLimits) bool {
	var fe *packagefmt.Error
	if !errors.As(err, &fe) || fe.Reason != packagefmt.ReasonCeilingExceeded {
		return false
	}
	switch fe.Limit {
	case "max_installed_bytes":
		return operator.MaxInstalledBytes <= 0
	case "max_file_bytes":
		return operator.MaxFileBytes <= 0
	}
	return false
}
