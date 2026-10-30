package interaction

import "context"

type turnKey struct{}

func WithTurn(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, turnKey{}, id)
}
func TurnID(ctx context.Context) string { id, _ := ctx.Value(turnKey{}).(string); return id }

const (
	SaidTyped  = "typed"
	SaidSpoken = "spoken"
	SaidAnswer = "answer"
)

type saidKey struct{}

func WithSaid(ctx context.Context, said string) context.Context {
	return context.WithValue(ctx, saidKey{}, said)
}

func SaidOf(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	said, _ := ctx.Value(saidKey{}).(string)
	return said
}
