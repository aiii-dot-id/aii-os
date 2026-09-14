package oauth

// .
// .
// .
// .
// .
// .
// .

// .
// .
type ScopeSet struct {
	Read   []string
	Modify []string
}

// .
type Provider struct {
	Name         string
	AuthorizeURL string
	TokenURL     string
	DeviceURL    string
	RevokeURL    string
	// .
	// .
	AuthorizeParams map[string]string
	// .
	BaseScopes []string
	// .
	// .
	Hosts []string
	// .
	Scopes map[string]ScopeSet
	// .
	// .
	DeviceScopesUnsupported []string
}

var providers = map[string]Provider{
	"google": {
		Name:            "google",
		AuthorizeURL:    "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:        "https://oauth2.googleapis.com/token",
		DeviceURL:       "https://oauth2.googleapis.com/device/code",
		RevokeURL:       "https://oauth2.googleapis.com/revoke",
		AuthorizeParams: map[string]string{"access_type": "offline", "prompt": "consent"},
		Hosts:           []string{"www.googleapis.com:443", "gmail.googleapis.com:443", "people.googleapis.com:443", "imap.gmail.com:993", "smtp.gmail.com:465"},
		Scopes: map[string]ScopeSet{
			"calendar": {Read: []string{"https://www.googleapis.com/auth/calendar.readonly"}, Modify: []string{"https://www.googleapis.com/auth/calendar.events"}},
			"gmail":    {Read: []string{"https://www.googleapis.com/auth/gmail.readonly"}, Modify: []string{"https://www.googleapis.com/auth/gmail.modify", "https://www.googleapis.com/auth/gmail.send"}},
			"drive":    {Read: []string{"https://www.googleapis.com/auth/drive.readonly"}, Modify: []string{"https://www.googleapis.com/auth/drive.file"}},
			"contacts": {Read: []string{"https://www.googleapis.com/auth/contacts.readonly"}, Modify: []string{"https://www.googleapis.com/auth/contacts"}},
			"mail":     {Read: []string{"https://mail.google.com/"}, Modify: []string{"https://mail.google.com/"}},
		},
		DeviceScopesUnsupported: []string{"gmail", "calendar", "contacts", "mail"},
	},
	"microsoft": {
		Name:         "microsoft",
		AuthorizeURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		DeviceURL:    "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode",
		BaseScopes:   []string{"offline_access", "openid"},
		Hosts:        []string{"graph.microsoft.com:443", "outlook.office365.com:993", "smtp.office365.com:587"},
		Scopes: map[string]ScopeSet{
			"mail":     {Read: []string{"Mail.Read"}, Modify: []string{"Mail.ReadWrite", "Mail.Send"}},
			"calendar": {Read: []string{"Calendars.Read"}, Modify: []string{"Calendars.ReadWrite"}},
			"files":    {Read: []string{"Files.Read"}, Modify: []string{"Files.ReadWrite"}},
			"contacts": {Read: []string{"Contacts.Read"}, Modify: []string{"Contacts.ReadWrite"}},
		},
	},
	"github": {
		Name:         "github",
		AuthorizeURL: "https://github.com/login/oauth/authorize",
		TokenURL:     "https://github.com/login/oauth/access_token",
		DeviceURL:    "https://github.com/login/device/code",
		Hosts:        []string{"api.github.com:443"},
		Scopes: map[string]ScopeSet{
			// .
			// .
			// .
			"issues": {Read: []string{"read:user"}, Modify: []string{"public_repo"}},
			"repo":   {Read: []string{"repo"}, Modify: []string{"repo"}},
		},
	},
	"slack": {
		Name:         "slack",
		AuthorizeURL: "https://slack.com/oauth/v2/authorize",
		TokenURL:     "https://slack.com/api/oauth.v2.access",
		Hosts:        []string{"slack.com:443"},
		Scopes: map[string]ScopeSet{
			"chat": {Read: []string{"channels:history", "channels:read"}, Modify: []string{"chat:write"}},
		},
	},
}

// .
// .
// .
func ProviderTemplate(name string) (Provider, bool) {
	p, ok := providers[name]
	if !ok {
		return Provider{}, false
	}
	// .
	out := p
	out.AuthorizeParams = copyMap(p.AuthorizeParams)
	out.BaseScopes = append([]string(nil), p.BaseScopes...)
	out.Hosts = append([]string(nil), p.Hosts...)
	out.DeviceScopesUnsupported = append([]string(nil), p.DeviceScopesUnsupported...)
	out.Scopes = make(map[string]ScopeSet, len(p.Scopes))
	for k, v := range p.Scopes {
		out.Scopes[k] = ScopeSet{Read: append([]string(nil), v.Read...), Modify: append([]string(nil), v.Modify...)}
	}
	return out, true
}

// .
func ProviderNames() []string {
	return []string{"google", "microsoft", "github", "slack"}
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
