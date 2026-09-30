package interaction

import "context"

type turnKey struct{}

func WithTurn(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, turnKey{}, id)
}
func TurnID(ctx context.Context) string { id, _ := ctx.Value(turnKey{}).(string); return id }
