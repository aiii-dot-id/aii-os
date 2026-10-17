package logsink

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// secret is the shape the design names as the mechanism: a
// credential-bearing type renders itself redacted, at the source, so
// every sink gets the redaction rather than each sink being scrubbed.
type secret struct{ raw string }

func (s secret) LogValue() slog.Value { return slog.StringValue("[redacted]") }

func TestNoCredentialShapeReachesTheOperationalSinks(t *testing.T) {
	// Shapes that have actually turned up around this project: a vendor
	// key, a bearer token, and a PEM header.
	raws := []string{
		"sk-or-v1-0123456789abcdef0123456789abcdef",
		"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"-----BEGIN PRIVATE KEY-----MIIEvQIBADANBg",
	}

	// Every level must reach BOTH sinks, or the scan below inspects an
	// empty buffer and proves nothing. The threshold decides what is
	// SHOWN; this test is about what a shown line may CONTAIN, so it
	// opens the console all the way and restores it after.
	defDefault, catDefault := Levels()
	t.Cleanup(func() { SetLevels(defDefault, catDefault) })
	SetLevels(LevelTrace, nil)

	for _, level := range []slog.Level{LevelTrace, slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		var file, console bytes.Buffer
		h := newHandler(&file, &console)
		log := slog.New(h)
		for _, raw := range raws {
			log.Log(context.Background(), level, "a call carried a credential",
				slog.String(categoryKey, "probe"),
				slog.Any("token", secret{raw: raw}),
				slog.Group("nested", slog.Any("inner", secret{raw: raw})),
			)
		}
		for name, sink := range map[string]*bytes.Buffer{"file": &file, "console": &console} {
			got := sink.String()
			for _, raw := range raws {
				if strings.Contains(got, raw) {
					t.Fatalf("level %v: the %s sink carries a credential verbatim: %q", level, name, raw)
				}
			}
			// The control half: prove the probe was looking at a line
			// that really was written. A sink that stayed empty would
			// pass the scan above while testing nothing — which is the
			// failure this whole facility keeps producing.
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("level %v: the %s sink shows no redaction marker, so the scan above proved nothing: %q", level, name, got)
			}
		}
	}
}
