package logsink

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// .
// .
// .
const categoryKey = "sub"

// .
// .
// .
// .
// .
// .
// .
type handler struct {
	mu      *sync.Mutex
	file    io.Writer
	console io.Writer
	attrs   []slog.Attr
	group   string
}

func newHandler(file, console io.Writer) *handler {
	return &handler{mu: &sync.Mutex{}, file: file, console: console}
}

// .
// .
// .
func (h *handler) Enabled(context.Context, slog.Level) bool { return true }

// .
// .
// .
func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	next := *h
	next.attrs = make([]slog.Attr, 0, len(h.attrs)+len(as))
	next.attrs = append(next.attrs, h.attrs...)
	for _, a := range as {
		next.attrs = appendScoped(next.attrs, h.group, a)
	}
	return &next
}

// .
// .
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	if next.group == "" {
		next.group = name
	} else {
		next.group = next.group + "." + name
	}
	return &next
}

// .
// .
// .
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	defer func() { _ = recover() }()

	category := ""
	extra := make([]string, 0, 4)
	collect := func(a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		if a.Key == categoryKey {
			category = a.Value.String()
			return true
		}
		extra = append(extra, a.Key+"="+a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		for _, resolved := range appendScoped(nil, h.group, a) {
			collect(resolved)
		}
		return true
	})

	when := r.Time
	if when.IsZero() {
		when = time.Now()
	}
	var b strings.Builder
	b.WriteString(when.Format("2006/01/02 15:04:05 "))
	if category != "" {
		b.WriteString(category)
		// .
		// .
		if src, ok := SourceFrom(ctx); ok {
			if stamp := src.stamp(); stamp != "" {
				b.WriteString("[" + stamp + "]")
			}
		}
		b.WriteString(": ")
	}
	b.WriteString(r.Message)
	for _, e := range extra {
		b.WriteString(" " + e)
	}
	b.WriteString("\n")
	line := b.String()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file != nil {
		_, _ = io.WriteString(h.file, line)
	}
	// .
	// .
	if h.console != nil && (category == "" || r.Level >= slog.LevelWarn || r.Level >= thresholdFor(category)) {
		_, _ = io.WriteString(h.console, line)
	}
	return nil
}

// .
// .
func appendScoped(dst []slog.Attr, group string, a slog.Attr) []slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			if group != "" {
				group += "."
			}
			group += a.Key
		}
		for _, child := range a.Value.Group() {
			dst = appendScoped(dst, group, child)
		}
		return dst
	}
	if group != "" {
		a.Key = group + "." + a.Key
	}
	return append(dst, a)
}

// .

func emit(level slog.Level, category, format string, args ...any) {
	emitCtx(context.Background(), level, category, format, args...)
}

// .
// .
// .
func emitCtx(ctx context.Context, level slog.Level, category, format string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	l := slog.Default()
	if !l.Enabled(ctx, level) {
		return
	}
	l.LogAttrs(ctx, level, fmt.Sprintf(format, args...),
		slog.String(categoryKey, category))
}

// .
func Error(category, format string, args ...any) { emit(slog.LevelError, category, format, args...) }

// .
func Warn(category, format string, args ...any) { emit(slog.LevelWarn, category, format, args...) }

// .
func Info(category, format string, args ...any) { emit(slog.LevelInfo, category, format, args...) }

// .
func Debug(category, format string, args ...any) { emit(slog.LevelDebug, category, format, args...) }

// .
// .
func Trace(category, format string, args ...any) { emit(LevelTrace, category, format, args...) }

// .
// .
// .
// .
// .
// .

func ErrorCtx(ctx context.Context, category, format string, args ...any) {
	emitCtx(ctx, slog.LevelError, category, format, args...)
}

func WarnCtx(ctx context.Context, category, format string, args ...any) {
	emitCtx(ctx, slog.LevelWarn, category, format, args...)
}

func InfoCtx(ctx context.Context, category, format string, args ...any) {
	emitCtx(ctx, slog.LevelInfo, category, format, args...)
}

func DebugCtx(ctx context.Context, category, format string, args ...any) {
	emitCtx(ctx, slog.LevelDebug, category, format, args...)
}

func TraceCtx(ctx context.Context, category, format string, args ...any) {
	emitCtx(ctx, LevelTrace, category, format, args...)
}

// .
const PreviewRunes = 200

// .
// .
// .
// .
// .
// .
// .
func Preview(s string) string {
	r := []rune(s)
	total := len(r)
	if total > PreviewRunes {
		r = r[:PreviewRunes]
	}
	flat := strings.NewReplacer("\n", "⏎", "\r", "").Replace(string(r))
	if total > PreviewRunes {
		return flat + fmt.Sprintf("… (%d runes total)", total)
	}
	return flat
}
