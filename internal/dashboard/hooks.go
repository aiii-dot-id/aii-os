package dashboard

import (
	"context"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

type WSHandler struct {
	Interactions interaction.Reader

	Thinking func(ctx context.Context, turn uint64) (ThinkingRecord, bool, error)

	HeardSpeech    func(after uint64) (string, error)
	DatabaseExport func(context.Context) (io.ReadCloser, int64, error)

	Record         RecordSource
	RecordSettings func() RecordSettings

	Sent SentSource

	Background func(work func()) bool

	Speaker  string
	GetStats func() (*StatsResponse, error)

	PublicNameClaim func() (PublicNameState, error)
	PublicNameRetry func() (PublicNameState, error)
	PublicNameMove  func() (PublicNameState, error)
	PublicNameLook  func() (PublicNameState, error)
	PublicNameState func() PublicNameState

	AccessCodeSeconds func() int

	RefusedPauses func() int

	UnsignedRequests func() (perSecond, burst int)

	Channels       func() ChannelSettings
	SessionServers func() []ICEServer

	AccessCodeOrigin func() string
	HandleMessage    func(ctx context.Context, msg string) (string, error)

	TakeOutbox  func() (items []OutboxItem, settle func(shown bool), err error)
	HeldMail    func(context.Context, string) (MailRepairState, error)
	Messages    func(context.Context, MessagesRequest) (MessagesState, error)
	RepairMail  func(context.Context, MailRepairRequest) error
	InspectMail func(context.Context, string) (MailRepairState, error)

	PagesClosed   func()
	HandleGenesis func(ctx context.Context, req *GenesisRequest) (string, error)

	RecentTurns func() ([]HistoryTurn, error)
	GetTools    func() []ToolState
	SetToolFunc func(name string, enabled bool) error
	ObserveChat func(ctx context.Context, msg string, emit func(kind, name, args string)) (string, error)

	TurnActive func() bool
	Steer      func(text string) (bool, error)

	AdmitChat func(ctx context.Context, text string) (bool, error)

	AdmitRequest func(ctx context.Context, text string) error

	ReleaseTurn func()

	GetAsks   func() []AskView
	AnswerAsk func(AskAnswer) (text string, settle func(recorded bool), err error)

	GradeResult func(GradeRequest) (uint64, error)

	AcquireTurn func(ctx context.Context) error
	CancelTurn  func() bool

	PendingSteers func() []string

	HearUtterance   func(ctx context.Context, pcm []byte, sampleRate, channels int, answer bool) error
	VoiceConfigured func() bool

	VoiceStatus func() (state, reason, source string)

	VoiceMode func() (listen, speak string, revision uint64)

	AudioPlane       func() *audio.Plane
	VoiceEngine      func() bool
	VoiceSessionOpen func(ctx context.Context, inputID, outputID, mode string, processing *CaptureProcessing) (VoiceSession, error)

	GetIdentity   func() (*IdentityState, error)
	GetContinuity func() (*ContinuityState, error)

	Recall func(query string) (string, error)

	GetProviders func() ProviderDirectory

	SetProvider    func(ProviderEdit) error
	SetEffort      func(level string) error
	DeleteProvider func(name string) error

	RepairProvider       func(position int, sha256 string) error
	RemoveBrokenProvider func(position int, sha256 string) error

	SetSpeechService func(name, apiKey, baseURL string) error

	SpeechLists func(provider, direction, search, language, apiKey string) (SpeechLists, error)

	SpeakMint func(say SpeakText) (string, error)

	SpeakPlay func(ctx context.Context, id string, w io.Writer) error

	SpeakAhead func(text string) string

	ReplyVoice func() string

	VoiceSpeaker func() string

	SpeakerPolicy func() *SpeakerPolicyState

	DashboardToken func() string

	RotateDashboardToken func() error

	RequireDashboardToken func() error

	ApproveOverlayScript func(ApprovedScript) error

	SignInProvider      func(name string) (string, error)
	CompleteSignIn      func(name, input string) error
	OAuthCallback       func(code, state, authorityError string) error
	CancelSignIn        func(name string) error
	CancelProfileSignIn func(name string) error

	SignInProfile         func(name string) (string, error)
	CompleteProfileSignIn func(name, input string) error
	DeviceSignInProfile   func(name string) (*DeviceCodeView, error)
	DisconnectProfile     func(name string) error
	SetAuthProfile        func(edit AuthProfileEdit) error
	DeleteAuthProfile     func(name string) error

	SetPluginKey   func(plugin, key, secret string) error
	ClearPluginKey func(plugin, key string) error

	SettingChoices func(plugin, key string) ([]SettingChoice, error)

	UpdateCheck func() (*UpdateState, error)

	Continuity *ContinuityHooks

	RestoreNew *RestoreNewHooks

	Restart func() error

	Uninstall func() error

	DiscoverModels func(provider, baseURL, apiKey string) (models []string, needsKey bool, err error)

	GetWork func() (*WorkState, error)

	GetSandbox func() (*SandboxState, error)
	SetSandbox func(roots []string) error

	SetSandboxOff func(off, signedIn bool) error

	GetProjects func() ([]ProjectState, error)
	ProjectAct  func(ProjectRequest) error

	PluginAct func(PluginAction) error

	CatalogRefresh func() error

	GetWorkspace func(id string) (*WorkspaceState, error)

	GetProjectRoot func(id string) (string, bool)

	AdmitFile func(path string) (root, rel string, ok bool)

	ToldPath func(root, rel string) string
	Files    func() FilePolicy

	UploadDir func() string

	GetConfig func() *ConfigState
	SetConfig func(changes map[string]any) (*ConfigState, error)

	ListLogs func() ([]LogFileState, error)
	TailLogs func(name string) (*LogTailState, error)
}

func (s *Server) SwapHandler(h *WSHandler) {
	if h == nil {
		h = &WSHandler{}
	}
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
	if h.Interactions != nil {
		for _, c := range s.conns(func(_ carrier, cl *wsClient) bool { return cl.interactionVersion != "1" }) {
			c.CloseNow()
		}
	}
	s.PokeInteractions()
}

func (s *Server) currentHandler() *WSHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handler
}

func (s *Server) background(h *WSHandler, work func()) bool {
	if h.Background != nil {
		return h.Background(work)
	}
	return s.runJob(work)
}

func (s *Server) offLoop(ctx context.Context, conn carrier, reqID string, h *WSHandler, work func()) {
	if !s.background(h, work) {
		s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
	}
}

func (s *Server) runJob(work func()) bool {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.jobsStopping {
		return false
	}
	s.jobsWG.Add(1)
	go func() {
		defer s.jobsWG.Done()
		work()
	}()
	return true
}
