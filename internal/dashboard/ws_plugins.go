package dashboard

import (
	"context"
	"fmt"
	"strings"
)

func (s *Server) wsPlugin(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.PluginAct == nil || msg.Plugin == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}

	reqID, act := msg.RequestID, *msg.Plugin
	s.offLoop(ctx, conn, reqID, h, func() {
		if err := h.PluginAct(act); err != nil {
			s.sendErrorFor(ctx, conn, reqID, err.Error())
			return
		}
		s.replyConfig(ctx, conn, reqID, h)
	})
}

func (s *Server) wsCatalogRefresh(ctx context.Context, conn carrier, msg ClientMessage) {
	h := s.currentHandler()
	if h.CatalogRefresh == nil {
		s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
		return
	}

	reqID := msg.RequestID
	s.offLoop(ctx, conn, reqID, h, func() {
		if err := h.CatalogRefresh(); err != nil {
			s.sendErrorFor(ctx, conn, reqID, err.Error())
			return
		}
		s.replyConfig(ctx, conn, reqID, h)
	})
}

func (s *Server) handlePluginKey(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage, secretsOK bool) {
	reqID, kind := msg.RequestID, msg.Type
	label := strings.ReplaceAll(kind, "_", " ")
	if refusal := pluginKeyRefusal(msg, secretsOK); refusal != "" {
		s.sendErrorFor(ctx, conn, reqID, label+": "+refusal)
		return
	}
	req := *msg.PluginKey
	s.offLoop(ctx, conn, reqID, h, func() {
		var err error
		switch {
		case kind == "plugin_key_set" && h.SetPluginKey != nil:
			err = h.SetPluginKey(req.Plugin, req.Key, req.Secret)
		case kind == "plugin_key_clear" && h.ClearPluginKey != nil:
			err = h.ClearPluginKey(req.Plugin, req.Key)
		default:
			err = fmt.Errorf("not available")
		}
		if err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("%s: %v", label, err))
			return
		}
		s.replyConfig(ctx, conn, reqID, h)
	})
}

func (s *Server) handleSettingChoices(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	reqID, pk := msg.RequestID, msg.PluginKey
	if pk == nil || pk.Plugin == "" || pk.Key == "" {
		s.sendErrorFor(ctx, conn, reqID, "setting choices: name the plugin and the setting")
		return
	}
	req := *pk
	s.offLoop(ctx, conn, reqID, h, func() {
		reply := &SettingChoicesReply{Plugin: req.Plugin, Key: req.Key}
		if h.SettingChoices == nil {
			reply.Error = "not available"
		} else if choices, err := h.SettingChoices(req.Plugin, req.Key); err != nil {
			reply.Error = err.Error()
		} else {
			reply.Choices = choices
			if reply.Choices == nil {
				reply.Choices = []SettingChoice{}
			}
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "setting_choices", SettingChoices: reply})
	})
}

func pluginKeyRefusal(msg ClientMessage, secretsOK bool) string {
	pk := msg.PluginKey
	switch {
	case pk == nil || pk.Plugin == "" || pk.Key == "":
		return "name the plugin and the setting"
	case msg.Type == "plugin_key_set" && !secretsOK:
		return "this connection is not private, so the key was not taken — open the dashboard on the machine itself, or switch on TLS in Settings → Dashboard"
	}
	return ""
}
