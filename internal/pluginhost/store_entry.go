//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func WorkMaterial(store string, work StoreWork) Material {
	m := Material{PluginID: work.Entry, store: store}
	switch {
	case work.View != nil:
		v := *work.View
		m.view = &ViewWork{ModelsSHA256: v.ModelsSHA256, Names: v.Names}
		m.Models = v.Models
		m.ModelsDir, _ = plugstore.Store{Dir: store}.View(strings.TrimPrefix(work.Entry, plugstore.DirViews+"/"))
	case work.Root != nil:
		r := *work.Root
		m.Runtime = &r.Runtime
		m.RuntimeDir = filepath.Join(store, plugstore.DirRoots)
		m.entry = entrypointSpec{Name: r.Name, Digest: r.Digest}
		m.pkg, m.member, m.limits = r.Package, r.Member, r.Limits
	}
	return m
}

func StoreRoom(store string, work StoreWork, others []StoreWork, measure func(string) (int64, error)) error {
	if measure == nil {
		measure = freeDisk
	}
	need := workRemaining(store, work)
	if need == 0 {
		return nil
	}
	var ahead int64
	for _, o := range others {
		ahead += workRemaining(store, o)
	}
	free, err := measure(store)
	if err != nil {

		return nil
	}
	if free-ahead-need >= diskMargin {
		return nil
	}
	return &MaterialWait{
		Cause: fmt.Sprintf("the plugin store's volume has %s free; this needs %s more, the downloads ahead of it %s, and %s stays free",
			humanBytes(free), humanBytes(need), humanBytes(ahead), humanBytes(diskMargin)),
		Remedy: "free space on the volume holding " + store + ", or name a store on another volume: aii machine store <dir>",
		After:  time.Minute,
	}
}

func workRemaining(store string, work StoreWork) int64 {
	s := plugstore.Store{Dir: store}
	var n int64
	switch {
	case work.View != nil:
		for _, d := range work.View.Models {
			if blob, err := s.Blob(d.SHA256); err == nil {
				n += fileRemaining(blob, d.Size)
			}
		}
	case work.Root != nil:
		if root, err := s.Root(strings.TrimPrefix(work.Entry, plugstore.DirRoots+"/")); err == nil && isDir(root) {
			return 0
		}
		if archive, err := s.Archive(work.Root.Runtime.SHA256); err == nil {
			n += fileRemaining(archive, work.Root.Runtime.Size)
		}
		n += work.Root.Runtime.InstalledBytes
	}
	return n
}

func fileRemaining(path string, size int64) int64 {
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() && fi.Size() == size {
		return 0
	}
	if fi, err := os.Lstat(path + plugstore.PartialSuffix); err == nil && fi.Mode().IsRegular() && fi.Size() <= size {
		return size - fi.Size()
	}
	return size
}
