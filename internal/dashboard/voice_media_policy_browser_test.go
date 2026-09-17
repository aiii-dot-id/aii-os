//go:build !windows

package dashboard

import (
	"strings"
	"testing"
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
const mediaProbeModule = `
import { report } from '/__harness.js';

// A real, short, decodable WAV — the shape the host answers /speech/say
// with: 16-bit little-endian samples at 24 kHz, mono.
function wav() {
  const samples = 480, header = 44;
  const buf = new ArrayBuffer(header + samples * 2), v = new DataView(buf);
  const put = (off, s) => { for (let i = 0; i < s.length; i++) v.setUint8(off + i, s.charCodeAt(i)); };
  put(0, 'RIFF'); v.setUint32(4, 36 + samples * 2, true); put(8, 'WAVE'); put(12, 'fmt ');
  v.setUint32(16, 16, true); v.setUint16(20, 1, true); v.setUint16(22, 1, true);
  v.setUint32(24, 24000, true); v.setUint32(28, 48000, true); v.setUint16(32, 2, true); v.setUint16(34, 16, true);
  put(36, 'data'); v.setUint32(40, samples * 2, true);
  for (let i = 0; i < samples; i++) v.setInt16(header + i * 2, Math.round(3000 * Math.sin(i / 6)), true);
  return new Uint8Array(buf);
}

const url = URL.createObjectURL(new Blob([wav()], { type: 'audio/wav' }));
const el = new Audio();
const outcome = await new Promise(resolve => {
  el.addEventListener('loadedmetadata', () => resolve('loaded'));
  el.addEventListener('error', () => resolve('refused'));
  setTimeout(() => resolve('timeout'), 4000);
  el.src = url;
  el.load();
});
report(WANT === outcome ? 'OK' : 'FAIL wanted ' + WANT + ', got ' + outcome);
`

const mediaProbePage = `<!doctype html><html><head><meta charset="utf-8"></head><body>
<script type="module" src="/probe.js"></script>
</body></html>`

func TestTheIdentitysVoiceCanPlayUnderTheUIPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	probe := func(want string) map[string][]byte {
		return map[string][]byte{"/probe.js": []byte("const WANT = '" + want + "';" + mediaProbeModule)}
	}
	// .
	t.Run("under-uiCSP-the-voice-loads", func(t *testing.T) {
		runPageInEnginesWithHeaders(t, mediaProbePage, probe("loaded"),
			map[string]string{"Content-Security-Policy": uiCSP})
	})
	// .
	// .
	t.Run("without-media-src-it-is-refused", func(t *testing.T) {
		runPageInEnginesWithHeaders(t, mediaProbePage, probe("refused"),
			map[string]string{"Content-Security-Policy": "default-src 'none'; script-src 'self'; connect-src 'self'"})
	})
}

// .
// .
func TestTheUIPolicyAllowsTheVoiceItPlays(t *testing.T) {
	if !strings.Contains(uiCSP, "media-src") || !strings.Contains(uiCSP, "blob:") {
		t.Fatalf("uiCSP does not admit the audio the page plays: %s", uiCSP)
	}
}
