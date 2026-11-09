package dashboard

import "context"

type SpendView struct {
	Zone  string     `json:"zone"`
	Today string     `json:"today"`
	Days  []SpendDay `json:"days"`
}

type SpendDay struct {
	Day     string      `json:"day"`
	Tokens  int         `json:"tokens"`
	Floor   bool        `json:"floor,omitempty"`
	Unknown int         `json:"unknown,omitempty"`
	Lines   []SpendLine `json:"lines"`
}

type SpendLine struct {
	Role     string `json:"role"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Calls    int    `json:"calls"`
	Tokens   int    `json:"tokens"`
	Input    int    `json:"input,omitempty"`
	Output   int    `json:"output,omitempty"`
	Cached   int    `json:"cached,omitempty"`
	Floor    bool   `json:"floor,omitempty"`
	Unknown  int    `json:"unknown,omitempty"`
}

func (s *Server) querySpend(ctx context.Context, conn carrier, h *WSHandler, msg ClientMessage) {
	if h.GetSpend == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	view, err := h.GetSpend(max(msg.Days, 1))
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "spend", RequestID: msg.RequestID, Spend: view})
}
