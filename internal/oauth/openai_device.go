package oauth

// .
// .
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

func deviceJSON(ctx context.Context, client *http.Client, endpoint string, body any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = signInHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("device endpoint answered %d", resp.StatusCode)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, errors.New("invalid device response")
	}
	return resp.StatusCode, nil
}

func StartOpenAIDevice(ctx context.Context, client *http.Client, p Provider) (*DeviceAuthorization, error) {
	if p.ClientID == "" || p.DeviceURL == "" || p.VerificationURI == "" || p.DevicePollURL == "" || p.DeviceRedirectURI == "" || p.TokenURL == "" || p.DeviceExpiresSeconds <= 0 {
		return nil, errors.New("incomplete OpenAI device configuration")
	}
	var j struct {
		ID       string `json:"device_auth_id"`
		Code     string `json:"user_code"`
		Alias    string `json:"usercode"`
		Interval string `json:"interval"`
	}
	if _, err := deviceJSON(ctx, client, p.DeviceURL, map[string]string{"client_id": p.ClientID}, &j); err != nil {
		return nil, err
	}
	if j.Code == "" {
		j.Code = j.Alias
	}
	if j.ID == "" || j.Code == "" {
		return nil, errors.New("device response is missing device_auth_id or user_code")
	}
	interval := 5 * time.Second
	if j.Interval != "" {
		n, err := strconv.Atoi(j.Interval)
		if err != nil || n < 1 || n > p.DeviceExpiresSeconds {
			return nil, errors.New("invalid device interval")
		}
		interval = time.Duration(n) * time.Second
	}
	return &DeviceAuthorization{DeviceCode: j.ID, UserCode: j.Code, VerificationURI: p.VerificationURI, Interval: interval, Expires: time.Now().Add(time.Duration(p.DeviceExpiresSeconds) * time.Second)}, nil
}

func PollOpenAIDevice(ctx context.Context, pollClient, tokenClient *http.Client, p Provider, params OAuthParams, d *DeviceAuthorization) (*Tokens, error) {
	ctx, cancel := context.WithDeadline(ctx, d.Expires)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var j struct {
			Code      string `json:"authorization_code"`
			Verifier  string `json:"code_verifier"`
			Challenge string `json:"code_challenge"`
		}
		status, err := deviceJSON(ctx, pollClient, p.DevicePollURL, map[string]string{"device_auth_id": d.DeviceCode, "user_code": d.UserCode}, &j)
		if err == nil {
			sum := sha256.Sum256([]byte(j.Verifier))
			if j.Challenge == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != j.Challenge {
				return nil, errors.New("device response PKCE challenge mismatch")
			}
			params.RedirectURI = p.DeviceRedirectURI
			return ExchangeCode(ctx, tokenClient, params, j.Code, j.Verifier)
		}
		if status != http.StatusForbidden && status != http.StatusNotFound {
			return nil, err
		}
		interval := d.Interval
		if interval <= 0 {
			interval = 5 * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
