package dashboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"

	"github.com/coder/websocket"
)

// .
// .
// .
// .
// .
// .

// .
type ContinuitySnapshot struct {
	Name      string `json:"name"`
	At        string `json:"at"`
	Record    uint64 `json:"record"`
	OnDemand  bool   `json:"on_demand,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

// .
type ContinuityPass struct {
	At           string `json:"at"`
	Outcome      string `json:"outcome"`
	Detail       string `json:"detail,omitempty"`
	Snapshot     string `json:"snapshot,omitempty"`
	FailingSince string `json:"failing_since,omitempty"`
}

// .
type ContinuityView struct {
	Enabled         bool                 `json:"enabled"`
	Safe            bool                 `json:"safe,omitempty"`
	BackupKeep      int                  `json:"backup_keep"`
	NextPass        string               `json:"next_pass,omitempty"`
	Snapshots       []ContinuitySnapshot `json:"snapshots"`
	LastPass        *ContinuityPass      `json:"last_pass,omitempty"`
	Encrypting      bool                 `json:"encrypting"`
	Unencrypted     string               `json:"unencrypted,omitempty"`
	UnencryptedText string               `json:"unencrypted_text,omitempty"`
	EscrowCheckedAt string               `json:"escrow_checked_at,omitempty"`
	EscrowCovers    bool                 `json:"escrow_covers"`
	Chain           string               `json:"chain,omitempty"`
	// .
	// .
	SetAside []ContinuitySetAside `json:"set_aside,omitempty"`
	// .
	// .
	RestoreFailed string `json:"restore_failed,omitempty"`
	// .
	// .
	// .
	SecretsOK bool `json:"secrets_ok"`
}

// .
type ContinuitySetAside struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
	SetAsideAt   string `json:"set_aside_at,omitempty"`
	WasThrough   uint64 `json:"was_through,omitempty"`
	RestoredFrom string `json:"restored_from,omitempty"`
	RestoredTo   uint64 `json:"restored_to,omitempty"`
	PutBackAt    string `json:"put_back_at,omitempty"`
}

// .
// .
type RestorePlan struct {
	Source            string `json:"source"`
	Name              string `json:"name"`
	At                string `json:"at,omitempty"`
	Encrypted         bool   `json:"encrypted,omitempty"`
	RestoredTo        uint64 `json:"restored_to"`
	LiveThrough       int64  `json:"live_through"`
	SetAside          int64  `json:"set_aside"`
	Witnessed         int64  `json:"witnessed"`
	WitnessedSetAside int64  `json:"witnessed_set_aside"`
	WitnessHash       string `json:"witness_hash,omitempty"`
	WitnessURL        string `json:"witness_url,omitempty"`
	Identity          string `json:"identity,omitempty"`
	ConfirmText       string `json:"confirm_text"`
	AsideUnder        string `json:"aside_under"`
}

// .
type ContinuityRequest struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"`
	// .
	// .
	Source  string `json:"source,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	// .
	// .
	// .
	// .
	Passphrase string `json:"passphrase,omitempty"`
	Again      string `json:"again,omitempty"`
	FileB64    string `json:"file_b64,omitempty"`
}

// .
type ContinuityReply struct {
	Action string          `json:"action"`
	View   *ContinuityView `json:"view,omitempty"`
	// .
	Said string `json:"said,omitempty"`
	// .
	// .
	FileB64  string `json:"file_b64,omitempty"`
	FileName string `json:"file_name,omitempty"`
	// .
	// .
	Plan       *RestorePlan `json:"plan,omitempty"`
	Restarting bool         `json:"restarting,omitempty"`
	// .
	// .
	Error string `json:"error,omitempty"`
}

// .
type ContinuityHooks struct {
	// .
	// .
	// .
	Background func(work func()) bool

	View         func() ContinuityView
	Take         func(ctx context.Context) (ContinuityView, error)
	Verify       func(ctx context.Context, name string) (string, error)
	EscrowCreate func(passphrase []byte) (file []byte, name string, err error)
	EscrowCheck  func(sealed, passphrase []byte) (ContinuityView, error)
	// .
	// .
	RestorePlan func(source, name string) (RestorePlan, error)
	Restore     func(source, name, confirm string) error
}

// .
// .
const maxEscrowFileB64 = 96 << 10

// .
func remoteIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Server) handleBackups(ctx context.Context, conn *websocket.Conn, msg ClientMessage, secretsOK bool) {
	s.answerBackups(ctx, msg, secretsOK,
		func(r ContinuityReply) {
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "backups", Backups: &r})
		},
		func(text string) { s.sendErrorFor(ctx, conn, msg.RequestID, text) })
}

// .
// .
// .
func (s *Server) answerBackups(ctx context.Context, msg ClientMessage, secretsOK bool, send func(ContinuityReply), refuse func(string)) {
	h := s.currentHandler()
	if h == nil || h.Continuity == nil || msg.Backups == nil {
		refuse("backups are not available")
		return
	}
	hooks, req := h.Continuity, *msg.Backups
	reply := func(r ContinuityReply) {
		if r.View != nil {
			r.View.SecretsOK = secretsOK
		}
		r.Action = req.Action
		send(r)
	}
	withView := func(v ContinuityView, err error) {
		r := ContinuityReply{View: &v}
		if err != nil {
			r.Error = err.Error()
		}
		reply(r)
	}
	switch req.Action {
	case "status":
		v := hooks.View()
		reply(ContinuityReply{View: &v})
	case "take":
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if !hooks.background(func() {
			withView(hooks.Take(ctx))
			s.BroadcastStatus()
		}) {
			refuse("this identity is stopping")
		}
	case "verify":
		if !hooks.background(func() {
			said, err := hooks.Verify(ctx, req.Name)
			v := hooks.View()
			r := ContinuityReply{View: &v, Said: said}
			if err != nil {
				r.Error = err.Error()
			}
			reply(r)
		}) {
			refuse("this identity is stopping")
		}
	case "restore_plan":
		if hooks.RestorePlan == nil {
			refuse("restore is not available")
			return
		}
		plan, err := hooks.RestorePlan(req.Source, req.Name)
		v := hooks.View()
		if err != nil {
			reply(ContinuityReply{View: &v, Error: err.Error()})
			return
		}
		reply(ContinuityReply{View: &v, Plan: &plan})
	case "restore":
		if hooks.Restore == nil || h.Restart == nil {
			refuse("restore is not available")
			return
		}
		if err := hooks.Restore(req.Source, req.Name, req.Confirm); err != nil {
			v := hooks.View()
			reply(ContinuityReply{View: &v, Error: err.Error()})
			return
		}
		// .
		// .
		// .
		reply(ContinuityReply{Restarting: true})
		if err := h.Restart(); err != nil {
			reply(ContinuityReply{Error: "The restore is asked for and the restart did not start: " + err.Error() + " Restart the identity and it will be performed."})
		}
	case "escrow_create", "escrow_check":
		if !secretsOK {
			v := hooks.View()
			reply(ContinuityReply{View: &v, Error: "This connection is not private: a passphrase typed here would cross the network in the clear. Open the dashboard on the machine itself, or switch on TLS in Settings → Dashboard, and come back."})
			return
		}
		pass, again := []byte(req.Passphrase), []byte(req.Again)
		defer wipe(pass)
		defer wipe(again)
		if req.Action == "escrow_create" {
			if !bytes.Equal(pass, again) {
				v := hooks.View()
				reply(ContinuityReply{View: &v, Error: "The two passphrases differ."})
				return
			}
			file, name, err := hooks.EscrowCreate(pass)
			v := hooks.View()
			if err != nil {
				reply(ContinuityReply{View: &v, Error: err.Error()})
				return
			}
			reply(ContinuityReply{View: &v, FileB64: base64.StdEncoding.EncodeToString(file), FileName: name})
			return
		}
		if len(req.FileB64) > maxEscrowFileB64 {
			v := hooks.View()
			reply(ContinuityReply{View: &v, Error: "That file is larger than any escrow file."})
			return
		}
		sealed, err := base64.StdEncoding.DecodeString(req.FileB64)
		if err != nil {
			v := hooks.View()
			reply(ContinuityReply{View: &v, Error: "That file did not arrive whole. Pick it again."})
			return
		}
		withView(hooks.EscrowCheck(sealed, pass))
		s.BroadcastStatus()
	default:
		refuse("backups: unknown action")
	}
}

// .
// .
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// .

// .
type RestoreNewKeys struct {
	Identity string `json:"identity"`
	// .
	// .
	HoldsSnapshotKey bool `json:"holds_snapshot_key"`
}

// .
type RestoreNewDone struct {
	Identity   string `json:"identity"`
	RestoredTo uint64 `json:"restored_to"`
	Witnessed  int64  `json:"witnessed"`
}

// .
type RestoreNewRequest struct {
	Action     string          `json:"action"`
	FileB64    string          `json:"file_b64,omitempty"`
	Passphrase string          `json:"passphrase,omitempty"`
	Snapshot   string          `json:"snapshot,omitempty"`
	Provider   *GenesisRequest `json:"provider,omitempty"`
}

// .
type RestoreNewReply struct {
	Action string          `json:"action"`
	Keys   *RestoreNewKeys `json:"keys,omitempty"`
	Done   *RestoreNewDone `json:"done,omitempty"`
	Error  string          `json:"error,omitempty"`
	// .
	SecretsOK bool `json:"secrets_ok"`
}

// .
type RestoreNewHooks struct {
	Keys    func(sealed, passphrase []byte) (RestoreNewKeys, error)
	Upload  func(snapshot, member string, body io.Reader) error
	Restore func(ctx context.Context, snapshot string, provider *GenesisRequest) (RestoreNewDone, error)
}

const notPrivate = "This connection is not private: a passphrase, or an identity in the clear, would cross the network. Open this page on the machine itself, or serve the dashboard with TLS, and come back."

// .
func (s *Server) answerRestoreNew(ctx context.Context, msg ClientMessage, secretsOK bool, send func(RestoreNewReply), refuse func(string)) {
	h := s.currentHandler()
	if h == nil || h.RestoreNew == nil || msg.RestoreNew == nil {
		refuse("restoring an identity is offered at first boot only")
		return
	}
	req := *msg.RestoreNew
	reply := func(r RestoreNewReply) { r.Action, r.SecretsOK = req.Action, secretsOK; send(r) }
	if req.Action == "hello" {
		reply(RestoreNewReply{})
		return
	}
	if !secretsOK {
		reply(RestoreNewReply{Error: notPrivate})
		return
	}
	switch req.Action {
	case "keys":
		pass := []byte(req.Passphrase)
		defer wipe(pass)
		if len(req.FileB64) > maxEscrowFileB64 {
			reply(RestoreNewReply{Error: "That file is larger than any escrow file."})
			return
		}
		sealed, err := base64.StdEncoding.DecodeString(req.FileB64)
		if err != nil {
			reply(RestoreNewReply{Error: "That file did not arrive whole. Pick it again."})
			return
		}
		keys, err := h.RestoreNew.Keys(sealed, pass)
		if err != nil {
			reply(RestoreNewReply{Error: err.Error()})
			return
		}
		reply(RestoreNewReply{Keys: &keys})
	case "restore":
		// .
		// .
		go func() {
			done, err := h.RestoreNew.Restore(ctx, req.Snapshot, req.Provider)
			if err != nil {
				reply(RestoreNewReply{Error: err.Error()})
				return
			}
			reply(RestoreNewReply{Done: &done})
			s.BroadcastStatus()
		}()
	default:
		refuse("restore: unknown action")
	}
}

// .
// .
// .
// .
// .
// .
func (s *Server) handleRestoreUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	refuse := func(code int, text string) { http.Error(w, text, code) }
	if !s.tokenAuthorized(r) {
		refuse(http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !sameSiteAs(o, r.Host) {
		refuse(http.StatusForbidden, "this request came from another site")
		return
	}
	if r.TLS == nil && !remoteIsLoopback(r.RemoteAddr) {
		refuse(http.StatusForbidden, notPrivate)
		return
	}
	h := s.currentHandler()
	if h == nil || h.RestoreNew == nil || h.RestoreNew.Upload == nil {
		refuse(http.StatusNotFound, "restoring an identity is offered at first boot only")
		return
	}
	if err := h.RestoreNew.Upload(r.URL.Query().Get("snapshot"), r.URL.Query().Get("member"), r.Body); err != nil {
		refuse(http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// .
// .
func (h *ContinuityHooks) background(work func()) bool {
	if h.Background == nil {
		return false
	}
	return h.Background(work)
}
