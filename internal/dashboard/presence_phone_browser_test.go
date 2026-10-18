//go:build !windows

package dashboard

import "testing"

func TestAPageOnAnotherDeviceIsToldThePhoneIsAsleep(t *testing.T) {
	page := `<!doctype html><html><body>
<script type="module">
import { phoneAsleep } from '/presence.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const phone = { hosted_on_phone: true }, computer = { name: 'x' };
  assert(phoneAsleep(false, phone, 'abc.id.example.test'), 'a remote page of an identity on a phone that stopped answering is not told it is asleep');
  assert(!phoneAsleep(true, phone, 'abc.id.example.test'), 'an identity that is answering was called asleep');
  assert(!phoneAsleep(false, computer, 'abc.id.example.test') && !phoneAsleep(false, null, 'abc.id.example.test'), 'an identity on a computer, or one that never said, was called asleep');
  for (const own of ['127.0.0.1', 'localhost', '[::1]']) assert(!phoneAsleep(false, phone, own), 'the phone\'s own page was told its phone is asleep: ' + own);
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/presence.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+path[len("static/"):]] = data
	}
	modules["/state.js"] = []byte(`export const S = {};`)
	modules["/views/chat.js"] = []byte(`export function toggleThinkingDots() {}`)
	runPageInEngines(t, page, modules)
}
