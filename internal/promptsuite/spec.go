package promptsuite

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

const SpecEnv = "AII_PROMPTSUITE_MODEL"

const (
	KindAnthropic = "anthropic"

	KindClaudeSubscription = "claude-subscription"

	KindChatGPT = "chatgpt"

	KindGemini = "gemini"

	KindOpenAICompatible = "openai-compatible"
)

type ModelSpec struct {
	Kind  string `json:"kind"`
	Model string `json:"model"`

	URL string `json:"url,omitempty"`

	KeyEnv string `json:"key_env,omitempty"`

	CredentialFile string `json:"credential_file,omitempty"`
}

var Entry = map[string]string{
	KindAnthropic:          "Anthropic",
	KindClaudeSubscription: "Claude (Max/Pro)",
	KindChatGPT:            "ChatGPT (Plus/Pro)",
	KindGemini:             "Google Gemini",
	KindOpenAICompatible:   "Local server",
}

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ParseSpec(blob []byte) (*ModelSpec, error) {
	var s ModelSpec
	if err := canonicaljson.DecodeStrict(blob, &s); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return nil, fmt.Errorf("model spec: content after the object")
	} else if err != nil {

		return nil, fmt.Errorf("model spec: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func LoadSpec(file string, getenv func(string) string) (*ModelSpec, error) {
	if file != "" {
		blob, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return ParseSpec(blob)
	}
	raw := getenv(SpecEnv)
	if raw == "" {
		return nil, fmt.Errorf("no model spec: name a file with -spec or set %s", SpecEnv)
	}
	return ParseSpec([]byte(raw))
}

func (s *ModelSpec) Validate() error {
	if _, ok := Entry[s.Kind]; !ok {
		return fmt.Errorf("model spec: kind %q is not one of anthropic, claude-subscription, chatgpt, gemini, openai-compatible", s.Kind)
	}
	if strings.TrimSpace(s.Model) == "" {
		return fmt.Errorf("model spec: no model")
	}
	if s.KeyEnv != "" && !envNameRE.MatchString(s.KeyEnv) {
		return fmt.Errorf("model spec: key_env is not an environment variable's name")
	}
	switch s.Kind {
	case KindAnthropic, KindGemini:
		if s.KeyEnv == "" || s.URL != "" || s.CredentialFile != "" {
			return fmt.Errorf("model spec: %s takes key_env alone, the variable holding the key", s.Kind)
		}
	case KindClaudeSubscription, KindChatGPT:
		if s.CredentialFile == "" || !filepath.IsAbs(s.CredentialFile) || s.KeyEnv != "" || s.URL != "" {
			return fmt.Errorf("model spec: %s takes credential_file alone, the absolute path of the credential its owner keeps", s.Kind)
		}
	case KindOpenAICompatible:
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("model spec: openai-compatible takes url, an http(s) address with no user, query or fragment")
		}
		if s.CredentialFile != "" {
			return fmt.Errorf("model spec: openai-compatible takes no credential_file")
		}
	}
	return nil
}

func (s *ModelSpec) Label() string {
	label := s.Kind + " " + s.Model
	if s.Kind == KindOpenAICompatible {
		if u, err := url.Parse(s.URL); err == nil {
			label += " at " + u.Host
		}
	}
	return label
}

func (s *ModelSpec) Ready(getenv func(string) string) error {
	if s.KeyEnv != "" && getenv(s.KeyEnv) == "" {
		return fmt.Errorf("model spec: %s is not set in the runner's environment", s.KeyEnv)
	}
	if s.CredentialFile != "" {
		if _, err := os.Stat(s.CredentialFile); err != nil {
			return fmt.Errorf("model spec: the credential file cannot be found: %w", err)
		}
	}
	return nil
}
