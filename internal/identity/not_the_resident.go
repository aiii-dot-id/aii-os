package identity

import "context"

type notTheResidentKey struct{}

func NotTheResident(ctx context.Context) context.Context {
	return context.WithValue(ctx, notTheResidentKey{}, true)
}

func askedByTheResident(ctx context.Context) bool {
	marked, _ := ctx.Value(notTheResidentKey{}).(bool)
	return !marked
}
