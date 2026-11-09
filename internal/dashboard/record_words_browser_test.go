//go:build !windows

package dashboard

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// typedRecordFixture is a record of the kinds the Record names and filters
// by, each row {type, ring, days before now, payload}: a belief, an
// experience whose words run past the first view, AII OS's witness entry,
// the charter, an intention, a type the page does not know, a payload of
// more than 300 KiB that carries no words, read in ranges that cut its
// characters in two, and a note written with the old private member.
func typedRecordFixture(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	big := `{"n":30.0,"e":1e3,"face":"😀","long":"` + strings.Repeat("Ω界😀a", 300<<10/10) + `"}`
	long := `{"content":"` + strings.Repeat("They rolled the deploy back and wrote down why. ", 14) + `That is the end of their words."}`
	note := `{"id":"exp_old","content":"words a note once called sealed","category":"observation","private":true}`
	rows := []struct {
		typ     string
		ring    int
		days    int
		payload string
	}{
		{"ring0.genesis", 0, 40, `{"name":"Fixture"}`},
		{"belief.upsert", 3, 10, `{"statement":"A deploy goes to staging first."}`},
		{"experience.create", 3, 0, long},
		{"system.witnessed", 0, 0, `{"anchored_seq":3}`},
		{"relationship.upsert", 1, 0, `{"note":"Working with you."}`},
		{"intention.create", 3, 0, `{"description":"Stage the next deploy."}`},
		{"fixture.unknown", 2, 0, `{"i":7}`},
		{"fixture.big", 2, 0, big},
		{"experience.create", 3, 0, note},
	}
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC()
	for i, r := range rows {
		ts := now.Add(time.Duration(i-len(rows))*time.Second).AddDate(0, 0, -r.days).Format(time.RFC3339Nano)
		if _, err := st.DB().Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, ?, ?, ?, '')`, i+1, ts, r.typ, r.ring, r.payload); err != nil {
			t.Fatal(err)
		}
	}
	s := recordServer(t, st, "a-token-of-honest-length-for-this-test-0123456789", RecordSettings{PageRows: 10, FirstViewBytes: 512, Dir: "/srv/an identity"})
	s.rangeCap = 4099
	return s, big, note, long
}

// A RECORD ENTRY, AS THE BOARD DRAWS IT (step 11). In every engine, over
// the dashboard's own routes:
//   - an entry's head says what kind it is in the annex's words (its type in
//     the code face where there is none), who wrote it, when and its ring,
//     with their words as far as its head holds them, "…" where it was cut;
//   - opened, it says its words read whole (an experience longer than the
//     head among them) and, as written, its envelope above its payload;
//   - one with no words of its own says so; Copy carries the payload as
//     recorded, read whole, byte for byte, and answers beside itself; Copy
//     link is the entry's own address;
//   - Kind, the date (the board's choices, From and To among them) and who
//     wrote it apply to what is loaded and say so; who wrote it is read
//     from the type, never "You, quoted"; an empty filtered page says
//     nothing loaded matches; an address of one entry opens it and clears
//     every filter;
//   - the witness's line is Status's own words from its three fields.
func TestARecordEntryOpensInWordsAndCopiesAsRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("browser engines are not run in -short")
	}
	s, big, note, long := typedRecordFixture(t)
	wantBig, _ := json.Marshal(base64.StdEncoding.EncodeToString([]byte(big)))
	wantNote, _ := json.Marshal(note)
	driver := `
import { run, until, assert } from '/__harness.js';
import { S } from '/state.js';
import { renderRecord, RECORD_WORDS as W, whoWrote, wordsOf } from '/views/identity/record.js';
import { shortWhen } from '/views/identity/when.js';
const WI = (await import('/strings/identity.js')).default, WC = (await import('/strings/components.js')).default, WS = (await import('/strings/status.js')).default;
const bigB64 = ` + string(wantBig) + `, note = ` + string(wantNote) + `, longBytes = ` + strconv.Itoa(len(long)) + `;
const bytesOf = b64 => Uint8Array.from(atob(b64), c => c.charCodeAt(0));
const same = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
const rows = () => [...document.querySelectorAll('article[aria-label^="Entry "]')];
const shown = () => rows().filter(r => !r.parentElement.hidden).map(r => r.dataset.seq).join(',');
const row = seq => document.getElementById('record-' + seq);
const button = (el, text) => [...el.querySelectorAll('button')].find(b => b.textContent === text);
const field = label => [...document.querySelectorAll('label.c-filter')].find(l => (l.querySelector('.c-filter__label') || {}).textContent === label)?.querySelector('select, input');
const choose = (label, value) => { const f = field(label); f.value = value; f.dispatchEvent(new Event('change')); };
const head = seq => row(seq).querySelector('.record__line').textContent;
const opened = async seq => { const b = row(seq).querySelector('.record__open'); if (b.getAttribute('aria-expanded') !== 'true') b.click(); await until(() => row(seq).querySelector('.record__in-words'), 30000); };
const day = iso => { const t = new Date(iso); return t.getFullYear() + '-' + String(t.getMonth() + 1).padStart(2, '0') + '-' + String(t.getDate()).padStart(2, '0'); };
const copied = [];
if (navigator.clipboard) navigator.clipboard.writeText = async t => { copied.push(t); };
document.execCommand = () => { copied.push(document.activeElement && document.activeElement.value); return true; };
run(async () => {
  S.stats = { name: 'Taylor' };
  const witnessedAt = new Date().toISOString();
  S.cont = { witness_url: 'https://witness.test/w', witnessed_at: witnessedAt, anchored_seq: 4 };
  document.getElementById('view-record').classList.add('on');
  location.hash = '#/identity/record';
  renderRecord();
  await until(() => rows().length === 9, 10000);
  assert(shown() === '9,8,7,6,5,4,3,2,1', 'the page shows ' + shown());

  // The head: kind in words, who, when; their words as far as the head holds them. The ring and the type are
  // in the opened entry's raw line only (the first-session review, change 3).
  const when = seq => shortWhen(Date.parse(row(seq).dataset.when));
  assert(head('2') === WI.kindNewBelief + ' · Taylor · ' + when('2'), 'a belief\'s head is ' + head('2'));
  assert(head('4') === WI.kindChecked + ' · ' + WI.whoOs + ' · ' + when('4'), 'AII OS\'s entry\'s head is ' + head('4'));
  assert(head('7') === WI.kindRecorded + ' · ' + when('7'), 'an entry of a type the page has no word for is not "Recorded", or names an author: ' + head('7'));
  assert(rows().every(r => !/Ring \d|[a-z]+\.[a-z_]+/.test(r.querySelector('.record__line').textContent)), 'a row says a ring or a type: ' + rows().map(r => r.querySelector('.record__line').textContent).join(' | '));
  assert(row('2').querySelector('.record__words')?.textContent === 'A deploy goes to staging first.', 'a belief\'s words are not in its head');
  assert((row('3').querySelector('.record__words')?.textContent || '').endsWith(' …'), 'a head cut inside their words does not say so: ' + row('3').querySelector('.record__words')?.textContent);
  assert(row('2').querySelector('.record__head button.record__link')?.textContent === WI.copyLink, 'Copy link is not at the head');

  // Opened: their words read whole, beside the envelope and the payload as written.
  await opened('3');
  const words3 = row('3').querySelector('.record__in-words')?.textContent || '';
  assert(words3.endsWith('That is the end of their words.'), 'the entry\'s words were not read whole: ' + words3.slice(-60));
  const raw3 = row('3').querySelector('.record__raw')?.textContent;
  assert(raw3 === '#3 · ' + row('3').dataset.when + ' · experience.create · ring 3 · ' + longBytes + ' bytes', 'the envelope does not carry the time, the type, the ring and the bytes: ' + raw3);
  await opened('9');
  assert(row('9').querySelector('.record__in-words')?.textContent === 'words a note once called sealed', 'the note\'s words are ' + row('9').querySelector('.record__in-words')?.textContent);
  assert(row('9').querySelector('pre').textContent === note, 'as written is not the payload beside its words');
  await opened('8');
  assert(row('8').querySelector('.record__in-words')?.textContent === W.noWords, 'an entry with no words of its own said ' + row('8').querySelector('.record__in-words')?.textContent);
  assert(wordsOf('{"statement":"**firm**"}') === '**firm**' && wordsOf('{"n":30.0}') === '' && wordsOf('not json') === '', 'wordsOf does not read an entry\'s own words');

  // Copy: the payload as recorded, read whole; Copy link: its address.
  button(row('8'), WI.copyEntry).click();
  await until(() => copied.length, 30000);
  assert(same(new TextEncoder().encode(copied.at(-1)), bytesOf(bigB64)), 'Copy is not the payload byte for byte: ' + (copied.at(-1) || '').length);
  assert(button(row('8'), WI.copyEntry).nextElementSibling?.textContent === WC.copied, 'the copy did not answer beside itself');
  button(row('2'), WI.copyLink).click();
  await until(() => copied.length === 2, 10000);
  assert(copied.at(-1).endsWith('#/identity/record/2'), 'Copy link is ' + copied.at(-1));
  assert(button(row('2'), WI.copyLink).nextElementSibling?.textContent === WC.linkCopied, 'Copy link did not answer beside itself');

  // Who wrote it, from the type; no "You, quoted" here.
  assert(whoWrote('belief.upsert') === 'identity' && whoWrote('experience.create') === 'identity' && whoWrote('system.witnessed') === 'os' && whoWrote('fixture.1') === '', 'who wrote an entry is not read from its type');
  assert([...field(WI.filterWho).options].map(o => o.textContent).join('|') === [WI.anyone, 'Taylor', WI.whoOs].join('|'), 'Written by offers ' + [...field(WI.filterWho).options].map(o => o.textContent));

  // Kind, date and who, over what is loaded, said.
  const scope = [...document.querySelectorAll('.record__scope')].find(p => p.textContent === W.scope);
  const none = [...document.querySelectorAll('.record__scope')].find(p => p.textContent === W.noneLoaded);
  assert(scope && scope.hidden && none.hidden, 'a scope line shows with no filter set');
  choose(WI.filterKind, 'beliefs');
  assert(shown() === '2' && !scope.hidden, 'Beliefs shows ' + shown() + ', or does not say it filters what is loaded');
  choose(WI.filterKind, 'experiences'); assert(shown() === '9,3', 'Experiences shows ' + shown());
  choose(WI.filterKind, 'between'); assert(shown() === '5', 'Between you shows ' + shown());
  choose(WI.filterKind, 'work'); assert(shown() === '6', 'Work and promises shows ' + shown());
  choose(WI.filterKind, 'verification'); assert(shown() === '4', 'Verification shows ' + shown());
  choose(WI.filterWho, 'identity');
  assert(shown() === '' && !none.hidden, 'an empty filtered page does not say nothing loaded matches: ' + shown());
  choose(WI.filterKind, ''); choose(WI.filterWho, 'identity');
  assert(shown() === '9,6,5,3,2', 'Taylor\'s entries are ' + shown());
  choose(WI.filterWho, 'os'); assert(shown() === '4', 'AII OS\'s entries are ' + shown());
  choose(WI.filterWho, '');
  assert(shown() === '9,8,7,6,5,4,3,2,1' && scope.hidden && none.hidden, 'Anyone does not show every entry loaded');
  assert([...field(WI.filterDate).options].map(o => o.textContent).join('|') === [WI.last30, WI.last7, WI.sinceBirth, WI.chooseDates].join('|') && field(WI.filterDate).value === 'birth', 'the date choices are not the board\'s, opening on Since birth');
  choose(WI.filterDate, '7'); assert(shown() === '9,8,7,6,5,4,3', 'the last 7 days show ' + shown());
  choose(WI.filterDate, '30'); assert(shown() === '9,8,7,6,5,4,3,2', 'the last 30 days show ' + shown());
  choose(WI.filterDate, 'dates');
  choose(WI.dateFrom, day(row('2').dataset.when)); choose(WI.dateTo, day(row('2').dataset.when));
  assert(shown() === '2', 'From and To show ' + shown());

  // The witness's line: Status's words, from the same three fields.
  const witness = document.querySelector('.record__witnessed')?.textContent;
  assert(witness === WS.verifiedThrough.replace('{seq}', '4').replace('{when}', shortWhen(Date.parse(witnessedAt))).replace('{witness}', 'witness.test'), 'the witness\'s line is ' + witness);

  // An address of one entry opens it, and clears every filter.
  location.hash = '#/identity/record/3';
  renderRecord();
  await until(() => rows().length && rows()[0].dataset.seq === '3', 10000);
  assert(shown() === '3,2,1' && field(WI.filterDate).value === 'birth' && field(WI.filterKind).value === '' && scope.hidden, 'the entry\'s address kept a filter that hides it: ' + shown());
  assert(row('3').getAttribute('aria-current') === 'true' && row('3').querySelector('.record__open').getAttribute('aria-expanded') === 'true', 'the entry\'s address did not open it');
});
`
	runRecordInEngines(t, s, recordPageShell, driver, 0, 0)
}
