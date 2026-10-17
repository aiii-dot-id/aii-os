//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestASettingSaysWhetherTheFilesItsValueNeedsAreHereInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="root"></div>
<script type="module">
import { run } from '/__harness.js';
import { settingHTML } from '/views/plugin-setting.js';
const setting = models => ({ key: 'tts_language', type: 'enum', title: 'Speaking language', default: 'en', value: 'fr', values: ['en', 'fr'], models });
const line = models => { const d = document.createElement('div'); d.innerHTML = settingHTML('org.example.speaks', setting(models)); return d.querySelector('[data-setting-models]'); };
run(async (assert) => {
  assert(line(undefined) === null, 'a setting no model depends on has no line');
  assert(line({ state: 'ready', files_present: 0, files_total: 0, bytes_present: 0, bytes_total: 0 }) === null, 'a value that needs no file has no line');
  assert(line({ state: 'ready', files_present: 23, files_total: 23, bytes_present: 900, bytes_total: 900 }) === null, 'with every file here there is nothing to say');

  const fetching = line({ state: 'fetching', files_present: 12, files_total: 23, bytes_present: 1048576, bytes_total: 4194304 });
  assert(fetching && fetching.dataset.settingModels === 'fetching', 'a fetch in flight is said');
  assert(fetching.textContent.includes('12 of 23 files') && fetching.textContent.includes('no restart is needed'), 'it says how much is here and that no restart is needed: ' + fetching.textContent);

  const waiting = line({ state: 'waiting', files_present: 0, files_total: 1, bytes_present: 0, bytes_total: 2048, last_error: 'the download host did not answer.' });
  assert(waiting && waiting.dataset.settingModels === 'waiting', 'a stopped attempt is said');
  assert(waiting.textContent.includes('0 of 1 file,') && waiting.textContent.includes('The last attempt stopped: the download host did not answer. It is tried again'), 'with its reason, one full stop, and that it is tried again: ' + waiting.textContent);

  const absent = line({ state: 'absent', files_present: 0, files_total: 23, bytes_present: 0, bytes_total: 4194304 });
  assert(absent && absent.dataset.settingModels === 'absent' && absent.textContent.includes('not installed yet'), 'files nobody is fetching are said to be not installed yet');

  const hostile = line({ state: 'waiting', files_present: 0, files_total: 1, bytes_present: 0, bytes_total: 1, last_error: '<img src=x onerror=alert(1)>' });
  assert(hostile.querySelector('img') === null && hostile.textContent.includes('<img'), 'a reason is text, never markup');
});
</script>`
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { stats: { name: 'TestIdentity' }, config: { secrets_ok: true } };`),
	}
	for _, path := range []string{"static/util.js", "static/views/plugin-setting.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	runPageInEngines(t, page, modules)
}
