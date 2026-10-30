package install

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

const ServingRoute = "/serving"

const ServingKeyName = "serving.key"

func ServingKeyPath(slotDir string) string { return filepath.Join(slotDir, ServingKeyName) }

func WriteServingKey(slotDir string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	if published, err := atomicfile.WriteReplace(ServingKeyPath(slotDir), []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil && !published {
		return nil, fmt.Errorf("write the dashboard's serving key: %w", err)
	}
	return key, nil
}

func ServingProof(key, challenge []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("aii-os serving proof\x00"))
	m.Write(challenge)
	return m.Sum(nil)
}

const (
	DefaultStartWaitSeconds = 60
	MinStartWaitSeconds     = 5
	MaxStartWaitSeconds     = 1800
)

func HeldStartWaitSeconds(n int) int {
	return held(n, DefaultStartWaitSeconds, MinStartWaitSeconds, MaxStartWaitSeconds)
}

func ConfiguredStartWait(slotDir string) time.Duration {
	d, _ := dashboardSection(slotDir)
	return time.Duration(HeldStartWaitSeconds(d.StartWaitSeconds)) * time.Second
}

const ExitNotServing = 3

func Serving(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func PortFree(port int) bool {
	if Serving(port) {
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

type NotServingError struct {
	Port   int
	Within time.Duration
	Other  bool
}

func (e *NotServingError) Error() string {
	if e.Other {
		return fmt.Sprintf("port %d answers, but not with this identity's proof: another program holds the port", e.Port)
	}
	return fmt.Sprintf("the identity did not start serving on port %d within %s", e.Port, Seconds(e.Within))
}

func Seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + "s"
}

func WaitServing(port int, dir string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		other := false
		if Serving(port) {
			if proved(port, dir) {
				return nil
			}
			other = true
		}
		if !time.Now().Before(deadline) {
			return &NotServingError{Port: port, Within: within, Other: other}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func proved(port int, dir string) bool {
	raw, err := atomicfile.ReadFile(ServingKeyPath(dir))
	if err != nil {
		return false
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) == 0 {
		return false
	}
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return false
	}
	client := &http.Client{
		Timeout:       2 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s?c=%s", port, ServingRoute, hex.EncodeToString(challenge)))
	if err != nil {
		return false
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
	got, err := hex.DecodeString(strings.TrimSpace(string(body)))
	return res.StatusCode == http.StatusOK && err == nil && hmac.Equal(got, ServingProof(key, challenge))
}
