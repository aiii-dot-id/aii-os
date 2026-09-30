package googleoidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const KeysURL = "https://www.googleapis.com/oauth2/v3/certs"

const (
	maxTokenBytes = 8 << 10
	maxKeysBytes  = 64 << 10
	skew          = time.Minute
	minKeyTTL     = time.Minute
	maxKeyTTL     = 24 * time.Hour
	defaultKeyTTL = time.Hour

	refreshFloor = time.Minute
	fetchTimeout = 10 * time.Second
)

type Reason string

const (
	ReasonMalformed       Reason = "malformed"
	ReasonAlgorithm       Reason = "algorithm"
	ReasonKeyUnknown      Reason = "key_unknown"
	ReasonKeysUnavailable Reason = "keys_unavailable"
	ReasonSignature       Reason = "signature"
	ReasonIssuer          Reason = "issuer"
	ReasonAudience        Reason = "audience"
	ReasonEmail           Reason = "email"
	ReasonExpired         Reason = "expired"
	ReasonNotYetValid     Reason = "not_yet_valid"
)

type Refusal struct {
	Reason Reason
	Detail string
}

func (r *Refusal) Error() string {
	return "google id token refused (" + string(r.Reason) + "): " + r.Detail
}

func refuse(r Reason, format string, a ...any) error {
	return &Refusal{Reason: r, Detail: fmt.Sprintf(format, a...)}
}

type Verifier struct {
	client *http.Client
	url    string
	now    func() time.Time

	fetchMu sync.Mutex

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
	lastTry time.Time
}

func New(client *http.Client) *Verifier {
	return &Verifier{client: client, url: KeysURL, now: time.Now}
}

func (v *Verifier) Verify(ctx context.Context, token, audience, email string) error {

	if audience == "" {
		return refuse(ReasonAudience, "no audience was named to verify against")
	}
	if email == "" {
		return refuse(ReasonEmail, "no service account was named to verify against")
	}
	if len(token) > maxTokenBytes {
		return refuse(ReasonMalformed, "the token is over %d bytes", maxTokenBytes)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return refuse(ReasonMalformed, "the token is not three segments")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &head); err != nil {
		return refuse(ReasonMalformed, "the header: %v", err)
	}
	if head.Alg != "RS256" {
		return refuse(ReasonAlgorithm, "the token names algorithm %q; only RS256 is accepted", head.Alg)
	}
	if head.Kid == "" {
		return refuse(ReasonMalformed, "the header names no key")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return refuse(ReasonMalformed, "the signature: %v", err)
	}
	key, err := v.key(ctx, head.Kid)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return refuse(ReasonSignature, "the signature does not verify under key %s", head.Kid)
	}

	var claims struct {
		Iss           string  `json:"iss"`
		Aud           string  `json:"aud"`
		Email         string  `json:"email"`
		EmailVerified bool    `json:"email_verified"`
		Exp           float64 `json:"exp"`
		Iat           float64 `json:"iat"`
		Nbf           float64 `json:"nbf"`
	}
	if err := decodeSegment(parts[1], &claims); err != nil {
		return refuse(ReasonMalformed, "the claims: %v", err)
	}
	switch {
	case claims.Iss != "https://accounts.google.com" && claims.Iss != "accounts.google.com":
		return refuse(ReasonIssuer, "issued by %q, not by Google", claims.Iss)
	case claims.Aud != audience:
		return refuse(ReasonAudience, "issued for %q, not for %q", claims.Aud, audience)
	case !claims.EmailVerified || !strings.EqualFold(claims.Email, email):
		return refuse(ReasonEmail, "signed for %q (verified: %v), not for the service account %q", claims.Email, claims.EmailVerified, email)
	}
	now := v.now()
	if claims.Exp == 0 || now.After(time.Unix(int64(claims.Exp), 0).Add(skew)) {
		return refuse(ReasonExpired, "the token expired")
	}
	if claims.Iat == 0 || time.Unix(int64(claims.Iat), 0).After(now.Add(skew)) ||
		claims.Nbf != 0 && time.Unix(int64(claims.Nbf), 0).After(now.Add(skew)) {
		return refuse(ReasonNotYetValid, "the token is not valid yet")
	}
	return nil
}

func decodeSegment(seg string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if k, ok := v.held(kid); ok {
		return k, nil
	}
	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()
	if k, ok := v.held(kid); ok {
		return k, nil
	}
	v.mu.Lock()
	wait := refreshFloor - v.now().Sub(v.lastTry)
	fresh := v.now().Before(v.expires)
	v.mu.Unlock()
	if wait > 0 {
		if fresh {
			return nil, refuse(ReasonKeyUnknown, "no key %s among Google's, which were fetched a moment ago", kid)
		}
		return nil, refuse(ReasonKeysUnavailable, "Google's keys are not held and were tried a moment ago; the next try is in %s", wait.Round(time.Second))
	}
	if err := v.fetch(ctx); err != nil {
		return nil, refuse(ReasonKeysUnavailable, "Google's keys could not be fetched: %v", err)
	}
	if k, ok := v.held(kid); ok {
		return k, nil
	}
	return nil, refuse(ReasonKeyUnknown, "no key %s among Google's", kid)
}

func (v *Verifier) held(kid string) (*rsa.PublicKey, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	k, ok := v.keys[kid]
	return k, ok && v.now().Before(v.expires)
}

func (v *Verifier) fetch(ctx context.Context) error {
	v.mu.Lock()
	v.lastTry = v.now()
	v.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxKeysBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxKeysBytes {
		return fmt.Errorf("the answer is over %d bytes", maxKeysBytes)
	}
	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("the answer is not a key set: %v", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, e, ok := modulusExponent(k.N, k.E)
		if !ok {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: n, E: e}
	}
	if len(keys) == 0 {
		return fmt.Errorf("the answer holds no RSA key")
	}
	v.mu.Lock()
	v.keys, v.expires = keys, v.now().Add(keyTTL(resp.Header.Get("Cache-Control")))
	v.mu.Unlock()
	return nil
}

func modulusExponent(n64, e64 string) (*big.Int, int, bool) {
	nb, err1 := base64.RawURLEncoding.DecodeString(n64)
	eb, err2 := base64.RawURLEncoding.DecodeString(e64)
	if err1 != nil || err2 != nil || len(nb) < 128 || len(eb) == 0 || len(eb) > 4 {
		return nil, 0, false
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e < 3 {
		return nil, 0, false
	}
	return new(big.Int).SetBytes(nb), e, true
}

func keyTTL(cacheControl string) time.Duration {
	for _, d := range strings.Split(cacheControl, ",") {
		if age, ok := strings.CutPrefix(strings.TrimSpace(d), "max-age="); ok {
			if s, err := strconv.Atoi(age); err == nil && s >= 0 {
				return min(max(time.Duration(s)*time.Second, minKeyTTL), maxKeyTTL)
			}
		}
	}
	return defaultKeyTTL
}
