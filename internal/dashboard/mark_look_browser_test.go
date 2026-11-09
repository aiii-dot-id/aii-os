//go:build !windows

package dashboard

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// THEIR LOOK IS SET IN SETTINGS → APP, AND EVERY PAGE DRAWS IT. Once their
// mark is known, App has a part for it: their own mark, live, beside four
// sliders that start at theme.json's values. Moving one redraws the preview
// and nothing else; letting it go sends its own token (theme_set), says
// "Saving…", then "Saved." once the host answers under its request, and the
// bar's mark takes the new look from the theme the host sent. A refusal is
// said in the host's words under the sliders. At a phone's width the part
// stays inside the page.
func TestTheirLookIsSetInSettingsAndEveryPageDrawsIt(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	// A server of its own for each engine and width: a look one saved is
	// not the next one's starting look.
	for _, e := range engines {
		for _, size := range [][2]int{{1280, 800}, {390, 844}} {
			t.Run(e.engine.name, func(t *testing.T) {
				t.Setenv("AII_BROWSER_ENGINES", e.engine.name)
				theirLookAt(t, size)
			})
		}
	}
}

func theirLookAt(t *testing.T, size [2]int) {
	{
		s, _, _ := recordBrowserFixture(t)
		var mu sync.Mutex
		var sent []map[string]string
		tokens := map[string]string{}
		h := s.currentHandler()
		h.GetStats = func() (*StatsResponse, error) {
			return &StatsResponse{Name: "Mira", LedgerSeq: 25, LifetimeTicks: 480}, nil
		}
		h.GetConfig = func() *ConfigState { return &ConfigState{Cognition: CognitionConfigState{PulseSeconds: 300}} }
		h.SetThemeTokens = func(set map[string]string) error {
			mu.Lock()
			defer mu.Unlock()
			if set["--mark-folds"] == "24" {
				return errors.New("theme.json is not a valid theme as it stands, so it was left as it is")
			}
			sent = append(sent, set)
			for k, v := range set {
				tokens[k] = v
			}
			return nil
		}
		s.SetThemeSource(func() []byte {
			mu.Lock()
			defer mu.Unlock()
			if len(tokens) == 0 {
				return nil
			}
			b, _ := json.Marshal(map[string]any{"v": 1, "tokens": tokens})
			return b
		})
		driver := `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
(async () => {
  if (location.hash !== '#/settings/app') { location.hash = '#/settings/app'; location.reload(); return; }
  const part = () => document.querySelector('[data-mark-part]');
  const preview = () => part() && part().querySelector('[data-mark-preview] svg.c-mark');
  await until(() => preview() && document.querySelector('#bar-mark svg.c-mark'), 15000);
  if (!preview()) return report('FAIL: Settings → App has no part for their mark, drawn');
  const heading = part().querySelector('h3').textContent;
  if (heading !== 'Mira\'s mark') return report('FAIL: the part is not named for them: ' + heading);
  const at = [...part().querySelectorAll('[data-mark-look]')].map(i => i.dataset.markLook + '=' + i.value).join();
  if (at !== 'shape=0,cx=0,cy=0,folds=12') return report('FAIL: the sliders do not start at the theme\'s look: ' + at);
  const slider = k => part().querySelector('[data-mark-look="' + k + '"]');
  if (!slider('folds').disabled) return report('FAIL: the brain\'s folds are offered while the shape is the spiral');
  const said = () => part().querySelector('[data-mark-said]').textContent;
  const move = (k, v) => { slider(k).value = v; slider(k).dispatchEvent(new Event('input', { bubbles: true })); };
  move('shape', '1');
  if (said() !== '') return report('FAIL: moving a slider saved before it was let go: ' + said());
  if (!preview().querySelector('path.c-mark__bound') || slider('folds').disabled) return report('FAIL: moving the shape does not redraw the preview as a brain, or keep the folds waiting');
  if (!document.querySelector('#bar-mark svg circle.c-mark__bound')) return report('FAIL: moving a slider changed the bar before anything was saved');
  slider('shape').dispatchEvent(new Event('change', { bubbles: true }));
  if (said() !== 'Saving…') return report('FAIL: letting go does not say it is saving: ' + said());
  await until(() => said() === 'Saved.' && document.querySelector('#bar-mark svg path.c-mark__bound'), 5000);
  if (said() !== 'Saved.') return report('FAIL: the host\'s answer is not said: ' + said());
  if (!document.querySelector('#bar-mark svg path.c-mark__bound')) return report('FAIL: the bar\'s mark did not take the saved look');
  move('folds', '24'); slider('folds').dispatchEvent(new Event('change', { bubbles: true }));
  await until(() => /^Not saved/.test(said()), 5000);
  if (said() !== 'Not saved: theme.json is not a valid theme as it stands, so it was left as it is') return report('FAIL: a refusal is not said in the host\'s words: ' + said());
  const box = part().getBoundingClientRect();
  if (box.right > document.documentElement.clientWidth + 1 || part().scrollWidth > part().clientWidth + 1) return report('FAIL: the part runs past the page at ' + innerWidth + ' px: ' + Math.round(box.right));
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`
		runRecordInEngines(t, s, "", driver, size[0], size[1])
		mu.Lock()
		// A slider sends its own token alone, so another page's change to the
		// others is kept by the merge.
		if len(sent) == 0 || sent[0]["--mark-shape"] != "1" || len(sent[0]) != 1 {
			t.Errorf("at %d px the first save did not send the shape alone: %v", size[0], sent)
		}
		for _, set := range sent {
			for k := range set {
				if !strings.HasPrefix(k, "--mark-") {
					t.Errorf("a save sent a token that is not the mark's: %s", k)
				}
			}
		}
		mu.Unlock()
	}
}
