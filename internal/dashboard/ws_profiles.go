package dashboard

import (
	"context"
	"fmt"
	"strings"
)

func (s *Server) wsProfileSignIn(ctx context.Context, conn carrier, msg ClientMessage) {

	h := s.currentHandler()
	if h.SignInProfile == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "profile sign-in not available")
		return
	}
	reqID, name := msg.RequestID, msg.Profile
	s.offLoop(ctx, conn, reqID, h, func() {
		u, err := h.SignInProfile(name)
		if err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("connect: %v", err))
			return
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "profile_signin", SignInURL: u})
	})
}

func (s *Server) wsProfileAct(ctx context.Context, conn carrier, msg ClientMessage) {

	h := s.currentHandler()
	reqID, kind, name, edit := msg.RequestID, msg.Type, msg.Profile, msg.ProfileEdit
	s.offLoop(ctx, conn, reqID, h, func() {
		var err error
		var device *DeviceCodeView
		switch {
		case kind == "profile_signin_complete" && h.CompleteProfileSignIn != nil:
			err = h.CompleteProfileSignIn(name, msg.Input)
		case kind == "profile_device" && h.DeviceSignInProfile != nil:
			device, err = h.DeviceSignInProfile(name)
		case kind == "profile_disconnect" && h.DisconnectProfile != nil:
			err = h.DisconnectProfile(name)
		case kind == "auth_profile_set" && h.SetAuthProfile != nil && edit != nil:
			err = h.SetAuthProfile(*edit)
		case kind == "auth_profile_delete" && h.DeleteAuthProfile != nil:
			err = h.DeleteAuthProfile(name)
		default:
			err = fmt.Errorf("not available")
		}
		if err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("%s: %v", strings.ReplaceAll(kind, "_", " "), err))
			return
		}
		if device != nil {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "profile_device", Device: device})
			return
		}
		s.replyConfig(ctx, conn, reqID, h)
	})
}
