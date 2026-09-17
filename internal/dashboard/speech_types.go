package dashboard

// .
// .
type ProviderSpeech struct {
	STT *SpeechOffer `json:"stt,omitempty"`
	TTS *SpeechOffer `json:"tts,omitempty"`
}

// .
type SpeechOffer struct {
	// .
	// .
	ModelRequired bool `json:"model_required,omitempty"`
	VoiceRequired bool `json:"voice_required,omitempty"`
	// .
	MaxChars int `json:"max_chars,omitempty"`
	// .
	// .
	// .
	// .
	ListsModels bool `json:"lists_models,omitempty"`
	ListsVoices bool `json:"lists_voices,omitempty"`
	// .
	// .
	SearchesVoices bool `json:"searches_voices,omitempty"`
}

// .
// .
// .
type SpeechItem struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	Languages []string `json:"languages,omitempty"`
}

// .
type SpeechLanguage struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// .
// .
type SpeechLists struct {
	Provider  string `json:"provider"`
	Direction string `json:"direction"`
	// .
	// .
	Search   string `json:"search,omitempty"`
	Language string `json:"language,omitempty"`

	Models    []SpeechItem     `json:"models,omitempty"`
	Voices    []SpeechItem     `json:"voices,omitempty"`
	Languages []SpeechLanguage `json:"languages,omitempty"`

	// .
	// .
	ModelsListed bool `json:"models_listed,omitempty"`
	VoicesListed bool `json:"voices_listed,omitempty"`
	// .
	// .
	ModelsComplete bool `json:"models_complete,omitempty"`
	VoicesComplete bool `json:"voices_complete,omitempty"`
	// .
	// .
	Searched bool `json:"searched,omitempty"`
	// .
	// .
	NeedsKey bool `json:"needs_key,omitempty"`
	// .
	// .
	ModelsError string `json:"models_error,omitempty"`
	VoicesError string `json:"voices_error,omitempty"`
}

// .
type SpeechVoice struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// .
// .
type SpeechConfigState struct {
	STT SpeechInputState  `json:"stt"`
	TTS SpeechOutputState `json:"tts"`
	// .
	// .
	// .
	Services []SpeechService `json:"services,omitempty"`
	// .
	// .
	// .
	Spent  []SpeechSpent `json:"spent,omitempty"`
	Resets string        `json:"resets,omitempty"`
	// .
	Speakers *SpeakerPolicyState `json:"speakers,omitempty"`
}

// .
// .
// .
// .
type SpeakerPolicyState struct {
	Mode             string   `json:"mode"`
	UIDs             []string `json:"uids"`
	Unidentified     string   `json:"unidentified"`
	Revision         uint64   `json:"revision"`
	WithheldFinals   uint64   `json:"withheld_finals"`
	WithheldPartials uint64   `json:"withheld_partials"`
}

// .
// .
// .
type SpeechSpent struct {
	Provider   string `json:"provider"`
	Direction  string `json:"direction"`
	Requests   int    `json:"requests"`
	Characters int    `json:"characters,omitempty"`
	Seconds    int    `json:"seconds,omitempty"`
}

// .
// .
// .
type SpeechInputState struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Language string `json:"language"`
	// .
	MonthlyMinutes int `json:"monthly_minutes"`
	SpeechResolution
}

// .
type SpeechOutputState struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Voice    string `json:"voice"`
	// .
	MonthlyCharacters int `json:"monthly_characters"`
	SpeechResolution
}

// .
type SpeechResolution struct {
	Endpoint     string `json:"endpoint,omitempty"`
	APIKeyMasked string `json:"api_key_masked,omitempty"`
	Error        string `json:"error,omitempty"`
}

// .
// .
type SpeechService struct {
	Name      string          `json:"name"`
	Endpoint  string          `json:"endpoint"`
	APIKeyEnv string          `json:"api_key_env,omitempty"`
	Speech    *ProviderSpeech `json:"speech"`
	// .
	// .
	Added  bool `json:"added"`
	HasKey bool `json:"has_key"`
	// .
	// .
	Chats bool `json:"chats"`
	// .
	// .
	Custom bool `json:"custom,omitempty"`
}
