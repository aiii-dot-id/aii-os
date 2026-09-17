package oauth

// .
// .
// .
// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// .
type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Expires                 time.Time
	Interval                time.Duration
}

// .
// .
var (
	ErrDeviceDenied  = errors.New("the sign-in was refused on the authority's page")
	ErrDeviceExpired = errors.New("the device code expired before it was entered")
)

// .
func StartDevice(ctx context.Context, client *http.Client, deviceURL string, p OAuthParams) (*DeviceAuthorization, error) {
	if deviceURL == "" {
		return nil, errors.New("this authority offers no device-code sign-in")
	}
	if p.ClientID == "" {
		return nil, errors.New("a device sign-in needs the client id")
	}
	form := url.Values{"client_id": {p.ClientID}}
	if p.Scope != "" {
		form.Set("scope", p.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = signInHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device endpoint answered %d: %s", resp.StatusCode, scrubAuthorityError(body))
	}
	var j struct {
		DeviceCode              string  `json:"device_code"`
		UserCode                string  `json:"user_code"`
		VerificationURI         string  `json:"verification_uri"`
		VerificationURL         string  `json:"verification_url"`
		VerificationURIComplete string  `json:"verification_uri_complete"`
		ExpiresIn               float64 `json:"expires_in"`
		Interval                float64 `json:"interval"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, fmt.Errorf("device response: %w", err)
	}
	if j.VerificationURI == "" {
		j.VerificationURI = j.VerificationURL
	}
	if j.DeviceCode == "" || j.UserCode == "" || j.VerificationURI == "" {
		return nil, errors.New("device response is missing device_code, user_code or verification_uri")
	}
	d := &DeviceAuthorization{DeviceCode: j.DeviceCode, UserCode: j.UserCode, VerificationURI: j.VerificationURI,
		VerificationURIComplete: j.VerificationURIComplete, Interval: 5 * time.Second, Expires: time.Now().Add(15 * time.Minute)}
	if j.Interval > 0 {
		d.Interval = time.Duration(j.Interval * float64(time.Second))
	}
	if j.ExpiresIn > 0 {
		d.Expires = time.Now().Add(time.Duration(j.ExpiresIn * float64(time.Second)))
	}
	return d, nil
}

// .
// .
// .
// .
func PollDevice(ctx context.Context, client *http.Client, p OAuthParams, d *DeviceAuthorization) (*Tokens, error) {
	if client == nil {
		client = signInHTTP
	}
	interval := d.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if !d.Expires.IsZero() && time.Now().After(d.Expires) {
			return nil, ErrDeviceExpired
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		form := map[string]any{
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			"device_code": d.DeviceCode,
			"client_id":   p.ClientID,
		}
		tok, err := exchange(ctx, client, p, form)
		if err == nil {
			return tok, nil
		}
		var pending devicePending
		if errors.As(err, &pending) {
			switch pending.code {
			case "authorization_pending":
				continue
			case "slow_down":
				interval += 5 * time.Second
				continue
			case "access_denied":
				return nil, ErrDeviceDenied
			case "expired_token":
				return nil, ErrDeviceExpired
			}
		}
		return nil, err
	}
}

// .
// .
type devicePending struct{ code string }

func (d devicePending) Error() string { return "device grant: " + d.code }

// .
// .
// .
func Revoke(ctx context.Context, client *http.Client, revokeURL, token string, p OAuthParams) error {
	if revokeURL == "" || token == "" {
		return nil
	}
	form := url.Values{"token": {token}}
	if p.ClientID != "" {
		form.Set("client_id", p.ClientID)
	}
	if p.ClientSecret != "" {
		form.Set("client_secret", p.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if client == nil {
		client = signInHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("revocation endpoint answered %d: %s", resp.StatusCode, scrubAuthorityError(body))
	}
	return nil
}

// .
// .
func WritePrivateFile(path string, data []byte) error { return writePrivate(path, data) }

// .
// .
type ProfileState struct {
	Connected   bool
	Expires     time.Time
	Refreshable bool
}

// .
func ReadProfileState(path string) (ProfileState, error) {
	raw, err := readFile(path)
	if err != nil {
		if isNotExist(err) {
			return ProfileState{}, nil
		}
		return ProfileState{}, err
	}
	st, err := parseGeneric(raw)
	if err != nil {
		return ProfileState{}, err
	}
	return ProfileState{Connected: st.access != "", Expires: st.expires, Refreshable: st.refresh != ""}, nil
}
