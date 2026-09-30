package wire

import "context"

type modelIDKey struct{}

func WithModelID(ctx context.Context, modelID string) context.Context {
	if modelID == "" {
		return ctx
	}
	return context.WithValue(ctx, modelIDKey{}, modelID)
}

func ModelIDFromContext(ctx context.Context) string {
	modelID, _ := ctx.Value(modelIDKey{}).(string)
	return modelID
}

type stablePrefixKey struct{}

func WithStablePrefix(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, stablePrefixKey{}, n)
}

func StablePrefix(ctx context.Context) int { n, _ := ctx.Value(stablePrefixKey{}).(int); return n }

type tapSourceKey struct{}

func WithTapSource(ctx context.Context, source string) context.Context {
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, tapSourceKey{}, source)
}

func TapSource(ctx context.Context) string {
	source, _ := ctx.Value(tapSourceKey{}).(string)
	return source
}
