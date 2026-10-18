package publicname

import (
	"fmt"
	"strconv"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/relay"
)

type CertificateConfig struct {
	ServerURL     string `json:"server_url,omitempty"`
	TLSSPKISHA256 string `json:"tls_spki_sha256,omitempty"`
	ACMEDirectory string `json:"acme_directory,omitempty"`
	Contact       string `json:"contact,omitempty"`

	RouteMode string `json:"route_mode,omitempty"`

	RelayEndpoint string `json:"relay_endpoint,omitempty"`

	DirectAddresses *[]string `json:"direct_addresses,omitempty"`

	RelayPingSeconds   int `json:"relay_ping_seconds,omitempty"`
	RelayBackoffBaseMS int `json:"relay_backoff_base_ms,omitempty"`
	RelayBackoffMaxMS  int `json:"relay_backoff_max_ms,omitempty"`
	RelayAnswerSeconds int `json:"relay_answer_seconds,omitempty"`

	RelayCarriedMax        int `json:"relay_carried_max,omitempty"`
	RelayAttachesPerSecond int `json:"relay_attaches_per_second,omitempty"`
	RelayAttachBurst       int `json:"relay_attach_burst,omitempty"`

	Prepare *bool `json:"prepare,omitempty"`

	PrepareCertificate bool `json:"prepare_certificate,omitempty"`
}

var prepareWhenUnset = true

func (c CertificateConfig) prepare() bool {
	if c.Prepare == nil {
		return prepareWhenUnset
	}
	return *c.Prepare
}

var relayTuningBounds = map[string][2]int{
	"certificate.relay_ping_seconds":    {10, 3600},
	"certificate.relay_backoff_base_ms": {100, 60000},
	"certificate.relay_backoff_max_ms":  {100, 3600000},
	"certificate.relay_answer_seconds":  {2, 120},

	"certificate.relay_carried_max":         {1, 256},
	"certificate.relay_attaches_per_second": {1, 100},
	"certificate.relay_attach_burst":        {1, 1000},
}

type RelayTuningOutOfBounds struct {
	Key           string
	Got, Min, Max int
	BaseOverMax   bool
	BaseMS, MaxMS int
}

func (e *RelayTuningOutOfBounds) Error() string {
	if e.BaseOverMax {
		return fmt.Sprintf("certificate.relay_backoff_base_ms is %d and certificate.relay_backoff_max_ms is %d: the first wait cannot be longer than the longest", e.BaseMS, e.MaxMS)
	}
	return fmt.Sprintf("%s: 0 takes the default; otherwise %d to %d, and this is %d", e.Key, e.Min, e.Max, e.Got)
}

func CheckRelayTuning(key string, n int) error {
	b, ok := relayTuningBounds[key]
	if !ok {
		return fmt.Errorf("%s: not a setting of the relay connection", key)
	}
	if n != 0 && (n < b[0] || n > b[1]) {
		return &RelayTuningOutOfBounds{Key: key, Got: n, Min: b[0], Max: b[1]}
	}
	return nil
}

func (c CertificateConfig) relayBackoffMS() (base, max int) {
	base, max = c.RelayBackoffBaseMS, c.RelayBackoffMaxMS
	if base == 0 {
		base = int(relay.BackoffBase / time.Millisecond)
	}
	if max == 0 {
		max = int(relay.BackoffMax / time.Millisecond)
	}
	return base, max
}

func (c CertificateConfig) CheckRelayBackoff() error {
	if base, max := c.relayBackoffMS(); base > max {
		return &RelayTuningOutOfBounds{BaseOverMax: true, BaseMS: base, MaxMS: max}
	}
	return nil
}

func (c CertificateConfig) RelayTuning() (t relay.Tuning, held []string) {
	hold := func(key string, n int) int {
		b := relayTuningBounds[key]
		switch {
		case n == 0:
			return 0
		case n < b[0]:
			held = append(held, fmt.Sprintf("%s is %d, under its least; %d is used", key, n, b[0]))
			return b[0]
		case n > b[1]:
			held = append(held, fmt.Sprintf("%s is %d, over its most; %d is used", key, n, b[1]))
			return b[1]
		}
		return n
	}
	t.PingSeconds = hold("certificate.relay_ping_seconds", c.RelayPingSeconds)
	t.BackoffBase = time.Duration(hold("certificate.relay_backoff_base_ms", c.RelayBackoffBaseMS)) * time.Millisecond
	t.BackoffMax = time.Duration(hold("certificate.relay_backoff_max_ms", c.RelayBackoffMaxMS)) * time.Millisecond
	t.Answer = time.Duration(hold("certificate.relay_answer_seconds", c.RelayAnswerSeconds)) * time.Second
	t.Carried = hold("certificate.relay_carried_max", c.RelayCarriedMax)
	t.AttachesPerSecond = hold("certificate.relay_attaches_per_second", c.RelayAttachesPerSecond)
	t.AttachBurst = hold("certificate.relay_attach_burst", c.RelayAttachBurst)
	if err := c.CheckRelayBackoff(); err != nil {
		held = append(held, err.Error()+"; the first wait is used for both")
	}
	return t, held
}

func (c CertificateConfig) InForce() []dashboard.SettingRead {
	t, _ := c.RelayTuning()
	count := func(v, unset int) string {
		if v <= 0 {
			v = unset
		}
		return strconv.Itoa(v)
	}
	span := func(d, unset, unit time.Duration, word string) string {
		if d <= 0 {
			d = unset
		}
		return strconv.Itoa(int(d/unit)) + " " + word
	}
	if t.BackoffMax > 0 && t.BackoffBase > t.BackoffMax {
		t.BackoffMax = t.BackoffBase
	}
	server := c.serverURL()
	if server == "" {
		server = "none: the certificate service is declined"
	}
	mode, err := RouteMode(c)
	if err != nil {
		mode = c.RouteMode + ": not a route this identity knows, so nothing is published"
	}
	ping := span(time.Duration(t.PingSeconds)*time.Second, relay.PingEvery, time.Second, "seconds")
	if t.PingSeconds <= 0 {
		ping += ", or what the relay states"
	}
	return dashboard.ReadSettings("certificate", c, map[string]string{
		"certificate.server_url":                server,
		"certificate.acme_directory":            c.acmeDirectory(),
		"certificate.route_mode":                mode,
		"certificate.relay_ping_seconds":        ping,
		"certificate.relay_backoff_base_ms":     span(t.BackoffBase, relay.BackoffBase, time.Millisecond, "ms"),
		"certificate.relay_backoff_max_ms":      span(t.BackoffMax, relay.BackoffMax, time.Millisecond, "ms"),
		"certificate.relay_answer_seconds":      span(t.Answer, relay.AnswerWithin, time.Second, "seconds"),
		"certificate.relay_carried_max":         count(t.Carried, relay.CarriedAtOnce),
		"certificate.relay_attaches_per_second": count(t.AttachesPerSecond, relay.AttachesPerSecond),
		"certificate.relay_attach_burst":        count(t.AttachBurst, relay.AttachBurst),
		"certificate.prepare":                   dashboard.OnOff(c.prepare()),
		"certificate.prepare_certificate":       dashboard.OnOff(c.PrepareCertificate),
	})
}

const (
	defaultCertificateServerURL = "https://certificate.aiii.id"
	defaultACMEDirectory        = "https://acme-v02.api.letsencrypt.org/directory"
)

func (c CertificateConfig) serverURL() string {
	switch c.ServerURL {
	case "":
		return defaultCertificateServerURL
	case "none":

		return ""
	}
	return c.ServerURL
}

func (c CertificateConfig) acmeDirectory() string {
	if c.ACMEDirectory == "" {
		return defaultACMEDirectory
	}
	return c.ACMEDirectory
}
