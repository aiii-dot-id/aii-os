package memory

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (f *Facility) recentPluginMemories(ctx context.Context, q Query) (Result, error) {
	if q.PluginID == "" || len(q.Stores) != 1 || q.Stores[0] != "plugin_memories" || q.Text != "" || q.Exact || q.Decay != "" && q.Decay != DecayNone {
		return Result{}, errors.New("recent inspection requires only a plugin store, without query, exact or decay")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	d, _ := Lookup("plugin_memories")
	where, args := predicates(d, q, true)
	res := Result{Policy: DecayNone, Limit: limit}
	src := Source{Store: d.Name, Status: StatusFoundNothing}
	err := f.st.ReadWith(ctx, func(db store.Reader) error {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_memories b WHERE "+where, args...).Scan(&src.Matched); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, "SELECT "+selectList(d)+" FROM plugin_memories b WHERE "+where+" ORDER BY b.created_at DESC, b.id DESC LIMIT ?", append(args, limit)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		res.Hits, err = scanHits(rows, d)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	ids := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		ids = append(ids, h.ID)
	}
	accesses, err := f.st.MemoryAccesses(d.Name, ids)
	if err != nil {
		return Result{}, err
	}
	for i := range res.Hits {
		h := &res.Hits[i]
		h.Match = "recent"
		h.Snippet = Excerpt(h.Text, nil, SnippetWords)
		h.Class = classOf(d, *h)
		h.Accesses = accesses[h.ID].Count
	}

	src.Shown = len(res.Hits)
	if src.Shown > 0 {
		src.Status = StatusFound
	}
	res.Truncated = src.Matched > src.Shown
	res.Sources = []Source{src}
	return res, nil
}
