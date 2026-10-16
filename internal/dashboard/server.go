package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/coder/websocket"
)

//go:embed static/*
var staticFS embed.FS

const shippedPrefix = "/shipped/"

type Server struct {
	voiceControlsFailed atomic.Uint64

	voiceControlsUnheld atomic.Uint64

	wsMu    sync.Mutex
	wsConns map[carrier]*wsClient

	interactionSignal chan struct{}
	interactionDone   chan struct{}
	outboxSignal      chan struct{}
	pumpCancel        context.CancelFunc
	pumpDone          chan struct{}

	turnCtx       context.Context
	turnCtxCancel context.CancelFunc

	jobsMu       sync.Mutex
	jobsWG       sync.WaitGroup
	jobsStopping bool

	requests inFlight

	auth atomic.Pointer[accessPolicy]

	refusedPause func(time.Duration)

	refused refusalGate

	unsigned                 unsignedGate
	foreignHosts, badOrigins minuteLines

	serviceWorkers minuteLines

	strangerBodyWait time.Duration

	wsAdmitHook func()
	sweepEvery  time.Duration

	rangeCap int

	gate *quiesce.Gate

	sessionMu    sync.Mutex
	sessionConns int
	lastActivity time.Time
	sessionGrace time.Duration

	lastOperatorAct time.Time

	voiceMu   sync.Mutex
	voiceBusy bool

	port   int
	addr   string
	server *http.Server

	tlsDir      string
	tlsMaterial *TLSMaterial
	startedAt   time.Time
	handler     *WSHandler
	mu          sync.RWMutex

	loopbackAddr string
	loopServer   *http.Server

	carried            *carriedListener
	carriedServer      *http.Server
	carrying           atomic.Bool
	carriedIdleSeconds int

	channelSessions atomic.Int64

	accessCode        atomic.Pointer[accessCode]
	accessCodeSeconds atomic.Int64
	now               func() time.Time

	accessCodeSpent func()
	hostsMu         sync.RWMutex
	allowedHosts    map[string]bool
	host            string
	boundAddr       string
	tls             bool
	anyHostPort     string
	webhook         func(w http.ResponseWriter, r *http.Request, pluginID, hookPath string)

	origin string

	publicName string
	publicCert func() *tls.Certificate

	secMu        sync.RWMutex
	secReg       *sections.Registry
	layoutSource func() []byte
	themeSource  func() []byte

	overlayDir string

	approvedScripts map[string]string

	buildStamp string

	overlayReported map[string]bool

	overlayEvents []OverlayEvent
}

func New(host string, port int, handler *WSHandler) *Server {

	if handler == nil {
		handler = &WSHandler{}
	}
	s := &Server{
		handler:           handler,
		port:              port,
		wsConns:           make(map[carrier]*wsClient),
		outboxSignal:      make(chan struct{}, 1),
		interactionSignal: make(chan struct{}, 1), interactionDone: make(chan struct{}),
		pumpDone:     make(chan struct{}),
		sessionGrace: 90 * time.Second,
		sweepEvery:   60 * time.Second,
		startedAt:    time.Now(),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		shippedOnly := false
		if p == shippedPrefix[:len(shippedPrefix)-1] {
			http.Redirect(w, r, shippedPrefix, http.StatusFound)
			return
		}
		if rest, ok := strings.CutPrefix(p, shippedPrefix); ok {
			p, shippedOnly = "/"+rest, true
		}
		if p == "/" {
			p = "/index.html"
		}
		var ctype string
		switch path.Ext(p) {
		case ".html":
			ctype = "text/html; charset=utf-8"
		case ".js":
			ctype = "text/javascript; charset=utf-8"
		case ".css":
			ctype = "text/css; charset=utf-8"
		case ".png":

			ctype = "image/png"
			shippedOnly = true
		default:
			http.NotFound(w, r)
			return
		}

		var data []byte
		ok := false
		if !shippedOnly || path.Ext(p) == ".css" {
			data, ok = s.overlayAsset(p)
		}
		if !ok {

			var err error
			data, err = staticFS.ReadFile("static" + p)
			if err != nil {
				if p == "/index.html" {

					http.Error(w, "cannot read index.html", http.StatusInternalServerError)
					return
				}
				http.NotFound(w, r)
				return
			}
		}

		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Security-Policy", uiCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		referrerPolicy := "no-referrer"
		if p == "/index.html" {

			referrerPolicy = "same-origin"
		}
		w.Header().Set("Referrer-Policy", referrerPolicy)

		w.Header().Set("Cache-Control", "no-cache")

		sum := sha256.Sum256(data)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)

		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	})

	mux.HandleFunc("GET /sections/{id}/{path...}", s.handleSectionFile)

	mux.HandleFunc("GET /p/{id}/{path...}", s.handleProjectFile)

	mux.HandleFunc("GET /w", privateRoute(s.handleWorldFile))

	mux.HandleFunc("POST /upload", privateRoute(s.handleUpload))

	mux.HandleFunc("POST /restore/upload", privateRoute(s.handleRestoreUpload))
	mux.HandleFunc("POST /speech/say", privateRoute(s.handleSpeechSay))

	mux.HandleFunc("POST /database/export", privateRoute(s.handleDatabaseExport))
	mux.HandleFunc("GET /speech/say/{id}", privateRoute(s.handleSpeechPlay))
	mux.HandleFunc("GET /speech/heard", privateRoute(s.handleSpeechHeard))

	mux.HandleFunc("GET /overlay/replacements", privateRoute(s.handleOverlayReplacements))
	mux.HandleFunc("POST /overlay/set-aside", privateRoute(s.handleOverlaySetAside))

	mux.HandleFunc("GET /overlay/scripts", privateRoute(s.handleOverlayScripts))
	mux.HandleFunc("GET /overlay/scripts/file", privateRoute(s.handleOverlayScriptFile))
	mux.HandleFunc("POST /overlay/scripts/approve", privateRoute(s.handleOverlayScriptApprove))

	mux.HandleFunc("GET /auth/token", privateRoute(s.handleAccessToken))
	mux.HandleFunc("POST /auth/token", privateRoute(s.handleAccessToken))

	mux.HandleFunc("GET /auth/link", privateRoute(s.handleAccessCodeLink))
	mux.HandleFunc("GET /auth/link.js", privateRoute(s.handleAccessCodeLinkJS))

	mux.HandleFunc("POST /auth/launcher", privateRoute(s.handleLauncherLink))
	mux.HandleFunc("POST /auth/rotate", privateRoute(s.handleRotateAccessToken))
	mux.HandleFunc("POST /auth/require", privateRoute(s.handleRequireAccessToken))

	mux.HandleFunc("GET /interaction/detail", privateRoute(s.serveInteractionDetail))
	mux.HandleFunc("GET /interaction/thinking", privateRoute(s.serveInteractionThinking))

	mux.HandleFunc("GET /record", privateRoute(s.serveRecord))
	mux.HandleFunc("GET /record/payload", privateRoute(s.serveRecordPayload))

	mux.HandleFunc("GET /sent", privateRoute(s.serveSent))
	mux.HandleFunc("GET /sent/body", privateRoute(s.serveSentBody))
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /oauth/callback", privateRoute(s.handleOAuthCallback))

	mux.HandleFunc("POST /hooks/{plugin}/{path...}", s.handleWebhook)
	mux.HandleFunc("GET /hooks/{plugin}/{path...}", s.handleWebhook)

	if host == "" {
		host = "127.0.0.1"
	}
	s.host = host
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", s.port))
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.requests.track(s.unsignedRate(s.hostGate(s.noServiceWorker(mux)))),

		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 16,
	}
	s.addr = addr

	return s
}

func privateRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next(w, r)
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.jobsMu.Lock()
	s.jobsStopping = true
	s.jobsMu.Unlock()
	s.mu.RLock()
	pumpCancel, turnCancel := s.pumpCancel, s.turnCtxCancel
	s.mu.RUnlock()
	if pumpCancel != nil {
		pumpCancel()
		select {
		case <-s.pumpDone:
		case <-time.After(2 * time.Second):
		}
	}
	if pumpCancel != nil {
		select {
		case <-s.interactionDone:
		case <-ctx.Done():
		}
	}

	if turnCancel != nil {
		turnCancel()
	}
	for _, c := range s.conns(nil) {

		c.CloseNow()
	}

	servers := []*http.Server{s.server}
	if s.loopServer != nil {
		servers = append(servers, s.loopServer)
	}
	if s.carriedServer != nil {
		s.carrying.Store(false)
		servers = append(servers, s.carriedServer)
	}
	drained := make(chan error, len(servers))
	for _, srv := range servers {
		go func() { drained <- srv.Shutdown(ctx) }()
	}
	var shutdownErr error
	for range servers {

		shutdownErr = errors.Join(shutdownErr, <-drained)
	}
	turnsDone := make(chan struct{})
	go func() {
		s.jobsWG.Wait()
		close(turnsDone)
	}()
	joined := false
	select {
	case <-turnsDone:
		joined = true
	case <-ctx.Done():
	}
	if shutdownErr == nil && joined {
		return nil
	}
	if ctx.Err() == nil {
		return shutdownErr
	}

	for _, srv := range servers {
		_ = srv.Close()
	}
	return &DrainCut{Cause: ctx.Err()}
}

func (s *Server) noteOperatorAct() {
	s.sessionMu.Lock()
	s.lastOperatorAct = time.Now()
	s.sessionMu.Unlock()
}

func (s *Server) OperatorActiveWithin(d time.Duration) bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return !s.lastOperatorAct.IsZero() && time.Since(s.lastOperatorAct) <= d
}

func isOperatorAct(msgType string) bool {
	switch msgType {
	case "query", "probe", "":
		return false
	}
	return true
}

func (s *Server) SessionLive() bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionConns > 0 {
		return true
	}
	if s.sessionGrace > 0 && !s.lastActivity.IsZero() {
		return time.Since(s.lastActivity) < s.sessionGrace
	}
	return false
}

func (s *Server) PageOpen() bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return len(s.wsConns) > 0
}

func (s *Server) forgetConn(conn carrier) bool {
	s.wsMu.Lock()
	_, present := s.wsConns[conn]
	delete(s.wsConns, conn)
	last := present && len(s.wsConns) == 0
	s.wsMu.Unlock()
	if h := s.currentHandler(); last && h != nil && h.PagesClosed != nil {
		h.PagesClosed()
	}
	return present
}

func (s *Server) conns(keep func(carrier, *wsClient) bool) []carrier {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	out := make([]carrier, 0, len(s.wsConns))
	for c, cl := range s.wsConns {
		if keep == nil || keep(c, cl) {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	admitted := s.auth.Load()
	if !s.wsAuthorizedUnder(admitted, r) {

		if s.wsOriginAllowed(r) && s.guessed(r) {
			s.credentialRefused(r)
		} else if s.badOrigins.own(refusalLinesAMinute, func(n uint64) {
			logsink.Warn("dashboard.refusal", "%d more session(s) refused for their origin or sign-in in the last minute", n)
		}) {
			logsink.Warn("dashboard.refusal", "refused: origin %q host %q failed auth", r.Header.Get("Origin"), r.Host)
		}
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	if h := s.currentHandler(); h.Interactions != nil && r.URL.Query().Get("interaction_version") != "1" {
		http.Error(w, "dashboard protocol changed; reload the page", http.StatusUpgradeRequired)
		return
	}
	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{

		InsecureSkipVerify: true,
	})
	if err != nil {
		logsink.Warn("dashboard.error", "accept error: %v", err)
		return
	}
	defer socket.Close(websocket.StatusNormalClosure, "")

	socket.SetReadLimit(maxVoiceFrameBytes)

	ctx := r.Context()

	conn, release, alive := s.chooseCarrier(ctx, socket, r, &s.channelSessions)
	if !alive {
		return
	}
	defer release()

	if s.wsAdmitHook != nil {
		s.wsAdmitHook()
	}

	s.wsMu.Lock()
	if cutsOff(admitted, s.auth.Load()) {
		s.wsMu.Unlock()
		logsink.Info("dashboard.refusal", "refused: the access policy changed while the session was being admitted")
		conn.CloseNow()
		return
	}
	cl := &wsClient{interactionVersion: r.URL.Query().Get("interaction_version"), interactionRequests: make(chan ClientMessage, 1), addr: r.RemoteAddr, agent: r.UserAgent(), secretsOK: privateConnection(r), policy: admitted}
	s.wsConns[conn] = cl
	s.wsMu.Unlock()
	defer func() {

		s.dropVoiceSession(cl)
		s.forgetConn(conn)
	}()

	s.sessionMu.Lock()
	s.sessionConns++
	s.sessionMu.Unlock()
	defer func() {
		s.sessionMu.Lock()
		s.sessionConns--
		s.sessionMu.Unlock()
	}()

	h := s.currentHandler()
	if h.Interactions != nil {
		s.sendMsg(ctx, conn, ServerMessage{Type: "interaction_changed"})
	}

	s.carryOutbox(ctx, h, []carrier{conn}, false)

	s.sendStatus(ctx, conn, h)

	if h.Interactions == nil && h.RecentTurns != nil {
		if turns, err := h.RecentTurns(); err == nil && len(turns) > 0 {
			s.sendMsg(ctx, conn, ServerMessage{Type: "history", History: turns})
		}
	}

	if h.TurnActive != nil && h.TurnActive() {
		s.sendMsg(ctx, conn, ServerMessage{Type: "response", Message: "", Stream: true})
	}

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		s.sessionMu.Lock()
		s.lastActivity = time.Now()
		s.sessionMu.Unlock()

		if typ == websocket.MessageBinary {
			if len(data) > 0 && data[0] == voiceStreamVersion {
				s.handleVoiceStream(ctx, conn, data)
			} else {
				s.handleVoiceFrame(ctx, conn, data)
			}
			continue
		}

		if len(data) > maxTextFrameBytes {
			s.sendError(ctx, conn, "message too large")
			continue
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.sendError(ctx, conn, "invalid message format")
			continue
		}

		var requestEnvelope map[string]json.RawMessage
		if err := json.Unmarshal(data, &requestEnvelope); err == nil {
			if raw, present := requestEnvelope["request_options"]; present || msg.RequestOptions != nil {
				if msg.Type != "chat" || string(raw) == "null" {
					s.sendRequestError(ctx, conn, msg.RequestID, "REQUEST_OPTIONS_INVALID", "request_options requires a chat request and an object")
					continue
				}
				if err := interaction.Strict(data, &msg); err != nil {
					s.sendRequestError(ctx, conn, msg.RequestID, "REQUEST_OPTIONS_INVALID", err.Error())
					continue
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					s.sendRequestError(ctx, conn, msg.RequestID, "REQUEST_OPTIONS_INVALID", "request_options must be an object")
					continue
				}
				nullOption := false
				for _, key := range []string{"allowed_tools", "response_format"} {
					if value, exists := fields[key]; exists && string(value) == "null" {
						nullOption = true
					}
				}
				if nullOption {
					s.sendRequestError(ctx, conn, msg.RequestID, "REQUEST_OPTIONS_INVALID", "omit an unused option; null cannot specify a request constraint")
					continue
				}
			}
		}
		if msg.Type == "query" && msg.Query == "interaction" {
			var envelope map[string]json.RawMessage
			if err := interaction.Strict(data, &envelope); err != nil {
				s.interactionError(ctx, conn, msg.RequestID, err)
				continue
			}
			var query interaction.Query
			if err := interaction.Strict(envelope["interaction_query"], &query); err != nil {
				s.interactionError(ctx, conn, msg.RequestID, err)
				continue
			}
			msg.InteractionQuery = &query
		}
		if isOperatorAct(msg.Type) {
			s.noteOperatorAct()
		}
		switch msg.Type {
		case "voice_session":
			s.handleVoiceSession(ctx, conn, msg.Voice)
			continue
		case "presence":

			continue
		case "probe":
			s.wsProbe(ctx, conn, msg)
		case "chat":

			h := s.currentHandler()
			s.runOperatorRequest(WithAttachments(ctx, msg.Attachments), conn, h, msg.Message, msg.RequestOptions, nil, msg.RequestID)

		case "ask":
			s.wsAsk(ctx, conn, msg)

		case "cancel":
			s.wsCancel(ctx, conn)

		case "query":
			h := s.currentHandler()
			switch msg.Query {
			case "status":
				if status, ok := s.statusMessage(h); ok {
					status.RequestID = msg.RequestID
					s.sendMsg(ctx, conn, status)
				}
			case "interaction":
				s.sendInteractionQuery(ctx, conn, h, msg)
			case "history":
				s.queryHistory(ctx, conn, h, msg)
			case "sections":

				s.sendMsg(ctx, conn, s.sectionsMessage())
			case "ui_layout":
				s.sendMsg(ctx, conn, s.layoutMessage())
			case "ui_theme":
				s.sendMsg(ctx, conn, s.themeMessage())
			case "ui.overlay":

				s.sendMsg(ctx, conn, s.overlayMessage())
			case "steering":

				s.sendMsg(ctx, conn, s.steeringMessage(h))
			case "outbox":
				s.sendOutbox(ctx, conn, h)
			case "held_mail":
				s.handleMailRepair(ctx, conn, h, msg)
			case "messages":
				s.handleMessages(ctx, conn, h, msg)
			case "projects":
				s.sendProjects(ctx, conn, h, msg.RequestID)
			case "workspace":
				s.sendWorkspace(ctx, conn, h, msg.Name)
			case "sandbox":
				s.sendSandbox(ctx, conn, h, msg.RequestID)
			case "work":
				s.queryWork(ctx, conn, h)
			case "tools":
				s.queryTools(ctx, conn, h)
			case "recall":
				s.queryRecall(ctx, conn, h, msg)
			case "identity":
				s.queryIdentity(ctx, conn, h)
			case "config":
				s.queryConfig(ctx, conn, h)
			case "asks":
				s.queryAsks(ctx, conn, h)
			case "providers":
				s.queryProviders(ctx, conn, h)
			case "discover":
				s.queryDiscover(ctx, conn, h, msg)
			case "speech_lists":
				s.querySpeechLists(ctx, conn, h, msg)
			case "dashboard_token":
				s.queryDashboardToken(ctx, conn, h, msg)
			case "continuity":
				s.queryContinuity(ctx, conn, h)
			case "logs":
				s.queryLogs(ctx, conn, h, msg)
			default:
				s.sendError(ctx, conn, "unknown query: "+msg.Query)
			}

		case "sandbox_set":
			s.wsSandboxSet(ctx, conn, msg)

		case "sandbox_switch":
			s.wsSandboxSwitch(ctx, conn, msg)

		case "project":
			s.wsProject(ctx, conn, msg)

		case "plugin":
			s.wsPlugin(ctx, conn, msg)

		case "restart":
			s.wsRestart(ctx, conn, msg)

		case "catalog_refresh":
			s.wsCatalogRefresh(ctx, conn, msg)

		case "config_set":
			s.wsConfigSet(ctx, conn, cl, msg)

		case "tool_toggle":
			s.wsToolToggle(ctx, conn, msg)

		case "genesis":
			s.wsGenesis(ctx, conn, msg)

		case "public_name_claim", "public_name_retry", "public_name_move", "public_name_look", "public_name_state":
			s.wsPublicName(ctx, conn, msg)

		case "access_code", "access_code_cancel":
			s.wsAccessCode(ctx, conn, cl, msg)

		case "restore_new":
			s.answerRestoreNew(ctx, msg, cl.secretsOK,
				func(rep RestoreNewReply) {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "restore_new", RestoreNew: &rep})
				},
				func(text string) { s.sendErrorFor(ctx, conn, msg.RequestID, text) })

		case "backups":
			s.handleBackups(ctx, conn, msg, cl.secretsOK)

		case "update_check":
			s.wsUpdateCheck(ctx, conn, msg)
		case "uninstall":
			s.wsUninstall(ctx, conn, msg)
		case "provider_signin_cancel", "profile_signin_cancel":
			s.wsSignInCancel(ctx, conn, msg)
		case "provider_signin":
			s.wsProviderSignIn(ctx, conn, msg)
		case "provider_signin_complete":
			s.wsProviderSignInComplete(ctx, conn, msg)
		case "repair_mail", "inspect_mail":
			s.handleMailRepair(ctx, conn, s.currentHandler(), msg)
		case "grade":
			s.wsGrade(ctx, conn, msg)

		case "profile_signin":
			s.wsProfileSignIn(ctx, conn, msg)
		case "profile_signin_complete", "profile_device", "profile_disconnect", "auth_profile_set", "auth_profile_delete":
			s.wsProfileAct(ctx, conn, msg)
		case "plugin_key_set", "plugin_key_clear":
			s.handlePluginKey(ctx, conn, s.currentHandler(), msg, cl.secretsOK)
			continue
		case "plugin_setting_choices":
			s.handleSettingChoices(ctx, conn, s.currentHandler(), msg)
			continue
		case "effort_set":
			s.wsEffortSet(ctx, conn, msg)

		case "provider_set", "provider_delete", "provider_repair", "provider_remove_broken", "speech_service":
			s.wsProviderEdit(ctx, conn, msg)

		default:
			s.sendError(ctx, conn, "unknown message type: "+msg.Type)
		}
	}
}

type wsClient struct {
	interactionVersion  string
	interactionRead     atomic.Bool
	interactionRequests chan ClientMessage
	mu                  sync.Mutex

	addr, agent string

	policy *accessPolicy

	secretsOK bool

	vmu   sync.Mutex
	voice *connVoice

	viewing string
}

func (s *Server) signedIn(conn carrier) bool {
	cl, now := s.client(conn), s.auth.Load()
	return cl != nil && cl.policy != nil && now != nil &&
		now.required && now.hash != "" && cl.policy.required && cl.policy.hash == now.hash
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	h := s.currentHandler()
	q := r.URL.Query()
	if h == nil || h.OAuthCallback == nil || len(q["state"]) != 1 || len(q["code"]) > 1 || len(q["error"]) > 1 || (q.Get("code") == "" && q.Get("error") == "") {
		http.Error(w, "No matching sign-in. Return to AII OS and try again.", http.StatusBadRequest)
		return
	}
	if err := h.OAuthCallback(q.Get("code"), q.Get("state"), q.Get("error")); err != nil {
		http.Error(w, "Sign-in did not complete. Return to AII OS and try again.", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html><title>AII OS</title><p>Signed in. Return to your AII OS tab.</p>")
}
