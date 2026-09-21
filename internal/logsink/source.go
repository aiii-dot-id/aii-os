package logsink

import (
	"context"
	"strings"
)

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
type Source struct {
	// .
	Turn string
	// .
	// .
	Worker string
}

// .
func (s Source) stamp() string {
	turn := strings.TrimSpace(s.Turn)
	worker := strings.TrimSpace(s.Worker)
	switch {
	case turn != "" && worker != "":
		return turn + "/" + worker
	case turn != "":
		return turn
	default:
		return worker
	}
}

type sourceKey struct{}

// .
func WithSource(ctx context.Context, s Source) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.stamp() == "" {
		return ctx
	}
	return context.WithValue(ctx, sourceKey{}, s)
}

// .
// .
// .
func WithWorker(ctx context.Context, worker string) context.Context {
	s, _ := SourceFrom(ctx)
	s.Worker = worker
	return WithSource(ctx, s)
}

// .
func SourceFrom(ctx context.Context) (Source, bool) {
	if ctx == nil {
		return Source{}, false
	}
	s, ok := ctx.Value(sourceKey{}).(Source)
	return s, ok
}
