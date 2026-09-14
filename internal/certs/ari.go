package certs

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// .
// .
// .
// .
// .

// .
// .
func (m *Manager) renewalInfoURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.DirectoryURL, nil)
	if err != nil {
		return "", err
	}
	res, err := m.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var dir struct {
		RenewalInfo string `json:"renewalInfo"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&dir); err != nil {
		return "", err
	}
	return dir.RenewalInfo, nil
}

// .
// .
// .
// .
func CertID(leaf *x509.Certificate) (string, error) {
	if len(leaf.AuthorityKeyId) == 0 {
		return "", errors.New("certificate has no authority key identifier")
	}
	der, err := asn1.Marshal(leaf.SerialNumber)
	if err != nil {
		return "", err
	}
	// .
	if len(der) < 2 || der[0] != 0x02 {
		return "", errors.New("serial number did not encode as an INTEGER")
	}
	n := int(der[1])
	value := der[2:]
	if der[1]&0x80 != 0 {
		lenBytes := int(der[1] & 0x7f)
		n = 0
		for _, b := range der[2 : 2+lenBytes] {
			n = n<<8 | int(b)
		}
		value = der[2+lenBytes:]
	}
	if len(value) != n {
		return "", errors.New("serial number length mismatch")
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(leaf.AuthorityKeyId) + "." + enc.EncodeToString(value), nil
}

// .
// .
func (m *Manager) renewalWindow(ctx context.Context) (*Window, error) {
	if m.leaf == nil {
		return nil, errors.New("no certificate")
	}
	base, err := m.renewalInfoURL(ctx)
	if err != nil || base == "" {
		return nil, err
	}
	id, err := CertID(m.leaf)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+id, nil)
	if err != nil {
		return nil, err
	}
	res, err := m.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("renewal info: HTTP %d", res.StatusCode)
	}
	var body struct {
		SuggestedWindow struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"suggestedWindow"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&body); err != nil {
		return nil, err
	}
	if body.SuggestedWindow.Start.IsZero() || body.SuggestedWindow.End.IsZero() {
		return nil, errors.New("renewal info without a window")
	}
	return &Window{Start: body.SuggestedWindow.Start, End: body.SuggestedWindow.End}, nil
}

func (m *Manager) httpClient() *http.Client {
	if m.cfg.HTTPClient != nil {
		return m.cfg.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}
