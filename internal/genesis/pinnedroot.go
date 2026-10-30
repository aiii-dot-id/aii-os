package genesis

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/rootlist"
)

//go:embed ring0_root_pubkey.json
var pinnedRootJSON []byte

var pinnedRootList = []rootlist.Entry{
	{Envelope: pinnedRootJSON},
}

var pinnedRoots = sync.OnceValue(func() *rootlist.List { return mustParseRoots(pinnedRootList) })

func PinnedRoot() *publicKeyEnvelope { return pinnedRoot() }

func PinnedRoots() *rootlist.List { return pinnedRoots() }

func pinnedRoot() *publicKeyEnvelope { return pinnedRoots().Current() }

func mustParseRoot(raw []byte) *publicKeyEnvelope {
	return mustParseRoots([]rootlist.Entry{{Envelope: raw}}).Current()
}

func mustParseRoots(entries []rootlist.Entry) *rootlist.List {
	l, err := rootlist.Parse(ring0RootKeyType, entries)
	if err != nil {
		panic(fmt.Sprintf("genesis: embedded pinned root invalid — broken build: %v", err))
	}
	return l
}

const ring0RootKeyType = "ring0"
