package interaction

import "context"

type turnKey struct{}

func WithTurn(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, turnKey{}, id)
}
func TurnID(ctx context.Context) string { id, _ := ctx.Value(turnKey{}).(string); return id }

type turnSourceKey struct{}

func WithTurnSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, turnSourceKey{}, source)
}

func TurnSource(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(turnSourceKey{}).(string)
	return source
}

type resumeSessionKey struct{}

func WithResumeSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, resumeSessionKey{}, sessionID)
}

func ResumeSession(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(resumeSessionKey{}).(string)
	return id
}

type alarmSetInKey struct{}
type alarmDeadlineKey struct{}

func WithAlarmSetIn(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, alarmSetInKey{}, source)
}

func WithAlarmDeadline(ctx context.Context, deadline int64) context.Context {
	return context.WithValue(ctx, alarmDeadlineKey{}, deadline)
}

func AlarmDeadline(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	d, _ := ctx.Value(alarmDeadlineKey{}).(int64)
	return d
}

func AlarmSetIn(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(alarmSetInKey{}).(string)
	return source
}

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
