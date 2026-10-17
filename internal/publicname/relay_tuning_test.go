package publicname

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/relay"
)

// THE RELAY CONNECTION'S FIGURES ARE SETTINGS, EACH HELD TO ITS BOUNDS. The
// ping interval, the two reconnect waits and the wait for an answer were
// constants in the relay client. Unset, each is the default the client was
// built with; set at the config door, each is refused outside its bounds
// with the key and the bounds named; and the first wait is never longer
// than the longest.
func TestTheRelayConnectionsSettingsAreHeldToTheirBounds(t *testing.T) {
	var unset CertificateConfig
	if tune, held := unset.RelayTuning(); tune != (relay.Tuning{}) || held != nil {
		t.Fatalf("unset, the client is told %+v (held %v), want nothing: its own defaults", tune, held)
	}
	for key, b := range relayTuningBounds {
		for _, ok := range []int{0, b[0], b[1]} {
			if err := CheckRelayTuning(key, ok); err != nil {
				t.Errorf("%s = %d was refused: %v", key, ok, err)
			}
		}
		for _, bad := range []int{-1, b[0] - 1, b[1] + 1} {
			if bad == 0 {
				continue // zero is the default, for a key whose least is one
			}
			var out *RelayTuningOutOfBounds
			err := CheckRelayTuning(key, bad)
			if !errors.As(err, &out) || out.Key != key || out.Min != b[0] || out.Max != b[1] || !strings.Contains(err.Error(), key) {
				t.Errorf("%s = %d: %v, want a refusal naming the key and its bounds", key, bad, err)
			}
		}
	}
	if err := CheckRelayTuning("certificate.relay_endpoint", 5); err == nil {
		t.Error("a key that is not one of the connection's figures was held as one")
	}

	set := CertificateConfig{RelayPingSeconds: 45, RelayBackoffBaseMS: 500, RelayBackoffMaxMS: 120000, RelayAnswerSeconds: 30}
	tune, held := set.RelayTuning()
	want := relay.Tuning{PingSeconds: 45, BackoffBase: 500 * time.Millisecond, BackoffMax: 2 * time.Minute, Answer: 30 * time.Second}
	if tune != want || held != nil {
		t.Fatalf("set, the client is told %+v (held %v), want %+v", tune, held, want)
	}

	// The first wait against the longest, each against the other's default.
	for _, c := range []struct {
		base, max int
		refused   bool
	}{{0, 0, false}, {2000, 0, false}, {0, 500, true}, {60000, 60000, false}, {60000, 30000, true}, {0, 1000, false}} {
		err := CertificateConfig{RelayBackoffBaseMS: c.base, RelayBackoffMaxMS: c.max}.CheckRelayBackoff()
		var out *RelayTuningOutOfBounds
		if (err != nil) != c.refused || (c.refused && (!errors.As(err, &out) || !out.BaseOverMax)) {
			t.Errorf("first wait %d ms, longest %d ms (0: the default): %v, want refused: %v", c.base, c.max, err, c.refused)
		}
	}
}

// A HAND-EDITED FILE IS NOT THE DOOR, AND WHAT IT CARRIES IS SAID. A value
// outside a key's bounds is held to the nearer bound, and the holding is
// named for the log: it is never used as written, and never changed in
// silence.
func TestAHandEditedRelaySettingIsHeldAndSaid(t *testing.T) {
	edited := CertificateConfig{RelayPingSeconds: 2, RelayBackoffBaseMS: 90000, RelayBackoffMaxMS: 50, RelayAnswerSeconds: 999}
	tune, held := edited.RelayTuning()
	want := relay.Tuning{PingSeconds: 10, BackoffBase: time.Minute, BackoffMax: 100 * time.Millisecond, Answer: 2 * time.Minute}
	if tune != want {
		t.Fatalf("held to %+v, want %+v", tune, want)
	}
	said := strings.Join(held, "\n")
	for _, key := range []string{"certificate.relay_ping_seconds", "certificate.relay_backoff_base_ms", "certificate.relay_backoff_max_ms", "certificate.relay_answer_seconds", "the first wait cannot be longer"} {
		if !strings.Contains(said, key) {
			t.Errorf("what was held does not name %q:\n%s", key, said)
		}
	}
}

func TestTheCarryBoundsReachTheRelayClient(t *testing.T) {
	set := CertificateConfig{RelayCarriedMax: 16, RelayAttachesPerSecond: 2, RelayAttachBurst: 8}
	tune, held := set.RelayTuning()
	if want := (relay.Tuning{Carried: 16, AttachesPerSecond: 2, AttachBurst: 8}); tune != want || held != nil {
		t.Fatalf("set, the client is told %+v (held %v), want %+v", tune, held, want)
	}
	if tune, held := (CertificateConfig{}).RelayTuning(); tune != (relay.Tuning{}) || held != nil {
		t.Fatalf("unset, the client is told %+v (held %v), want nothing: the defaults are its own", tune, held)
	}
	edited := CertificateConfig{RelayCarriedMax: 100000, RelayAttachesPerSecond: -4, RelayAttachBurst: 5000}
	tune, held = edited.RelayTuning()
	if tune.Carried != 256 || tune.AttachesPerSecond != 1 || tune.AttachBurst != 1000 {
		t.Fatalf("held to %+v, want 256 at once, 1 a second, bursts of 1000", tune)
	}
	said := strings.Join(held, "\n")
	for _, key := range []string{"certificate.relay_carried_max", "certificate.relay_attaches_per_second", "certificate.relay_attach_burst"} {
		if !strings.Contains(said, key) {
			t.Errorf("what was held does not name %q:\n%s", key, said)
		}
	}
}
