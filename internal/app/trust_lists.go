package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

const (
	defaultStatusListBytes int64 = 1 << 20
	minStatusListBytes     int64 = 64 << 10
	maxStatusListBytes     int64 = 64 << 20
)

func (a *App) statusListBytes() int64 {
	switch n := a.configSnapshot().Plugins.TrustListMaxBytes; {
	case n == 0:
		return defaultStatusListBytes
	case n < minStatusListBytes:
		return minStatusListBytes
	case n > maxStatusListBytes:
		return maxStatusListBytes
	default:
		return n
	}
}

var listNames = map[string]string{
	packagefmt.KeyTypePlatformRelease:    "The platform release key's withdrawal list",
	packagefmt.KeyTypePublisherCertifier: "The publisher certifier's withdrawal list",
	packagefmt.KeyTypeReviewer:           "The plugin reviewer's withdrawal list",
}

var rootSettings = map[string]string{
	packagefmt.KeyTypePlatformRelease:    "plugins.platform_root",
	packagefmt.KeyTypePublisherCertifier: "plugins.certifier_root",
	packagefmt.KeyTypeReviewer:           "plugins.reviewer_root",
}

func (a *App) bringTrustLists(ctx context.Context) (string, error) {
	opts := a.pluginOptions()
	if opts == nil || a.trustDir == "" || a.configSnapshot().Identity.LedgerPath == "" {
		return "", nil
	}
	roots := opts.Roots
	wrote, err := packagefmt.PlaceShippedStatus(a.trustDir, roots)
	for _, name := range wrote {
		logsink.Info("plugins.decision", "trust: %s in %s was older than the one this build ships — wrote the shipped one; the identity's next load holds it to its accepted epoch", name, a.trustDir)
	}
	if err != nil {
		logsink.Warn("plugins.error", "trust: a shipped status file could not be placed in %s (%v)", a.trustDir, err)
	}
	var errs []error
	var notes []string
	cat := a.catalog.Current()
	if cat == nil {
		a.pokeTrust(len(wrote) > 0)
		return "", nil
	}
	ownCatalog := !packagefmt.IsShippedRoot(roots.PlatformRelease)
	for _, d := range packagefmt.RevocationDomains() {
		want, ok := cat.Announced.StatusEpochs[d.RootKeyType]
		if !ok {
			continue
		}
		if pinned := pinnedFor(roots, d.RootKeyType); pinned != nil && !packagefmt.IsShippedRoot(pinned) != ownCatalog {
			if ownCatalog {
				notes = append(notes, fmt.Sprintf("%s is announced in the catalog signed with this machine's own platform release key (%s), and this machine keeps the platform's key for that list: the announcement does not speak for it, and it is not fetched.", listNames[d.RootKeyType], rootSettings[packagefmt.KeyTypePlatformRelease]))
			} else {
				notes = append(notes, fmt.Sprintf("%s is announced by the platform, and this machine pins a key of its own for it (%s): the platform's list does not apply here, and it is not fetched. That key's list is put in %s by whoever holds the key.", listNames[d.RootKeyType], rootSettings[d.RootKeyType], a.trustDir))
			}
			continue
		}
		held, have := roots.Revocation.Epoch(d.RootKeyType)
		if have && held >= want {
			continue
		}
		fetch := a.listFetch
		if fetch == nil {
			fetch = a.catalog.FetchBeside
		}
		raw, err := fetch(ctx, d.FileName, a.statusListBytes())
		var got int64
		var placed bool
		if err != nil {
			err = fmt.Errorf("fetching it: %w", err)
		} else {
			got, placed, err = packagefmt.PlaceStatus(a.trustDir, roots, d.FileName, raw)
		}
		if err == nil && got < want {
			err = fmt.Errorf("the list published is at epoch %d", got)
		}
		if placed {
			wrote = append(wrote, d.FileName)
			logsink.Info("plugins.decision", "trust: %s at epoch %d, announced by the platform, written to %s; the identity's next load holds it to its accepted epoch", d.FileName, got, a.trustDir)
		}
		if err == nil {
			continue
		}
		heldSaid := "none usable"
		if have {
			heldSaid = fmt.Sprintf("epoch %d", held)
		}
		e := fmt.Errorf("%s announced at epoch %d could not be brought here (%v); this machine holds %s", listNames[d.RootKeyType], want, err, heldSaid)
		if d.RootKeyType == packagefmt.KeyTypePlatformRelease {
			e = releaseListBehind{fmt.Errorf("%w. Until it is here, no update is installed automatically", e)}
		}
		errs = append(errs, e)
	}
	a.pokeTrust(len(wrote) > 0)
	return strings.Join(notes, "\n"), errors.Join(errs...)
}

func pinnedFor(roots packagefmt.TrustRoots, keyType string) *sigenvelope.PublicKeyEnvelope {
	switch keyType {
	case packagefmt.KeyTypePlatformRelease:
		return roots.PlatformRelease
	case packagefmt.KeyTypeReviewer:
		return roots.Reviewer
	case packagefmt.KeyTypePublisherCertifier:
		return roots.PublisherCertifier
	}
	return nil
}

type releaseListBehind struct{ error }

func (e releaseListBehind) Unwrap() error { return e.error }

func (releaseListBehind) Is(target error) bool { return target == updates.ErrReleaseListBehind }

func (a *App) pokeTrust(wrote bool) {
	if !wrote || a.sweepPoke == nil {
		return
	}
	select {
	case a.sweepPoke <- struct{}{}:
	default:
	}
}
