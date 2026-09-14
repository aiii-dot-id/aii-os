package cognitive

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/llm"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// .
// .
type Facility interface {
	Name() string
	Predicate(ctx context.Context) bool
	Execute(ctx context.Context) error
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func evidenceText(e store.Experience) string {
	if e.Provenance != "external" {
		return e.Content
	}
	// .
	// .
	return untrusted.Wrap("", e.Content)
}

// .
// .
type AuthoritySource interface {
	AuthorityPreamble() (string, error)
}

// .
// .
// .
// .
type prefixAuthority interface{ AuthorityPrefix() (string, int, error) }

func withPreamble(ctx context.Context, src AuthoritySource, base string) (context.Context, string, error) {
	if src == nil {
		return ctx, base, nil
	}
	var pre string
	var seam int
	var err error
	if source, ok := src.(prefixAuthority); ok {
		pre, seam, err = source.AuthorityPrefix()
	} else {
		pre, err = src.AuthorityPreamble()
	}
	if err != nil {
		return ctx, "", err
	}
	if pre == "" {
		return ctx, base, nil
	}
	return llm.WithStablePrefix(ctx, seam), pre + "\n\n---\n\n" + base, nil
}
