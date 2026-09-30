package dashboard

import (
	"context"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

const (
	ChatNotRecordedCode     = "CHAT_NOT_RECORDED"
	ChatRecordingFailedCode = "CHAT_RECORDING_FAILED"
)

type ChatRecording struct {
	Ref   *interaction.Location `json:"ref,omitempty"`
	Error *ChatRecordingError   `json:"error,omitempty"`
}

type ChatRecordingError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type chatRecordingKey struct{}

func withChatRecording(ctx context.Context, send func(ChatRecording)) context.Context {
	var once sync.Once
	complete := func(r ChatRecording) {
		once.Do(func() {
			fn := send
			send = nil
			fn(r)
		})
	}
	return context.WithValue(ctx, chatRecordingKey{}, complete)
}

func completeChatRecording(ctx context.Context, r ChatRecording) {
	if ctx == nil {
		return
	}
	if done, ok := ctx.Value(chatRecordingKey{}).(func(ChatRecording)); ok {
		done(r)
	}
}

func ChatRecorded(ctx context.Context, ref *interaction.Location, err error) {
	r := ChatRecording{Ref: ref}
	if err != nil {
		r.Error = &ChatRecordingError{Code: ChatRecordingFailedCode, Message: err.Error()}
		if r.Error.Message == "" {
			r.Error.Message = "recording failed without a confirmed outcome"
		}
	}
	if ref == nil || ref.Identity == "" || ref.Incarnation == "" || ref.ID == "" ||
		(ref.Source != "recorded" && ref.Source != "transient") || (err != nil && ref.Source == "recorded") {
		r.Ref = nil
		if r.Error == nil {
			r.Error = &ChatRecordingError{Code: ChatRecordingFailedCode, Message: "recording returned no confirmed occurrence reference"}
		}
	}
	completeChatRecording(ctx, r)
}

func ChatNotRecorded(ctx context.Context, reason string) {
	if reason == "" {
		reason = "submission ended before recording"
	}
	completeChatRecording(ctx, ChatRecording{Error: &ChatRecordingError{Code: ChatNotRecordedCode, Message: reason}})
}
