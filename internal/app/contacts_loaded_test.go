package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// refusedBook is a book with four lines at fault, one for each rule the
// row names, under a first line the door keeps.
func refusedBook() []Contact {
	return []Contact{
		{Name: "Quinn", Channel: "telegram", Address: "111", Wake: true, Operator: true},
		{Name: "Robin", Channel: "Telegram", Address: "222"},
		{Name: "ContactB", Channel: "telegram", Address: "111"},
		{Name: "operator", Channel: "mail", Address: "op@example.test"},
		{Name: "Ada", Channel: "mail", Address: "ada@example.test", Operator: true},
	}
}

// doorNotices are the operator's notices about the loaded book, oldest
// first.
func doorNotices(t *testing.T, a *App) []string {
	t.Helper()
	all, err := a.store.PageNotices()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range all {
		if strings.HasPrefix(n.ID, "contacts_door_") {
			out = append(out, n.Content)
		}
	}
	return out
}

// THE LOADER ASKS THE DOOR'S OWN RULE. For each fault the row names, what
// is said of the line is the refusal the door itself gives the same book,
// line, field and words; and where the door stops at the first line at
// fault, every such line is found.
func TestALoadedBookIsAskedTheDoorsOwnRuleLineByLine(t *testing.T) {
	first := Contact{Name: "Quinn", Channel: "telegram", Address: "111", Operator: true}
	for _, tc := range []struct {
		name  string
		line  Contact
		field string
	}{
		{"two people marked as the operator", Contact{Name: "Ada", Channel: "mail", Address: "ada@example.test", Operator: true}, "operator"},
		{"one address under two names", Contact{Name: "ContactB", Channel: "telegram", Address: "111"}, "address"},
		{"a contact named operator", Contact{Name: "Operator", Channel: "mail", Address: "op@example.test"}, "name"},
		{"a channel not in lowercase", Contact{Name: "Robin", Channel: "Telegram", Address: "222"}, "channel"},
	} {
		book := []Contact{first, tc.line}
		_, err := contactsFromChange(book)
		var door *ContactsRefusal
		if !errors.As(err, &door) {
			t.Fatalf("%s: the door kept the book: %v", tc.name, err)
		}
		said := contactLinesTheDoorRefuses(book)
		if len(said) != 1 || !reflect.DeepEqual(said[0], door) || door.Line != 2 || door.Field != tc.field {
			t.Errorf("%s: the door says %+v and the loaded book is said to hold %+v", tc.name, door, said)
		}
	}

	var lines []int
	for _, r := range contactLinesTheDoorRefuses(refusedBook()) {
		lines = append(lines, r.Line)
	}
	if !reflect.DeepEqual(lines, []int{2, 3, 4, 5}) {
		t.Fatalf("the lines at fault are 2, 3, 4 and 5; found %v", lines)
	}
	kept := []Contact{first, {Name: " quinn ", Channel: "mail", Address: " quinn@example.test ", Operator: true}}
	if said := contactLinesTheDoorRefuses(kept); said != nil {
		t.Fatalf("a book the door keeps (spaces dropped, one person on two lines) is said to hold %+v", said[0])
	}
}

// ONCE ON THE PAGE FOR EACH SUCH BOOK, IN THE LOG EACH TIME. The same book
// at the next start adds no notice; a book corrected in part is another
// book and is said; a book the door would keep says nothing.
func TestTheOperatorIsToldOnceWhichLinesOfALoadedBookTheDoorWouldRefuse(t *testing.T) {
	a := liveApp(t)
	cfg := a.configSnapshot()
	cfg.Contacts = refusedBook()

	logs := logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	said := doorNotices(t, a)
	if len(said) != 1 {
		t.Fatalf("one notice for one book, got %d: %q", len(said), said)
	}
	for _, want := range []string{
		"has 4 line(s) that Settings would refuse", "It is in force as written: nothing was changed or left out.",
		`Line 2 ("Robin" on "Telegram"): a channel is the name its adapter gives`,
		`Line 3 ("ContactB" on "telegram"): line 1 has the same channel and address`,
		`Line 4 ("operator" on "mail"): the name operator is taken`,
		`Line 5 ("Ada" on "mail"): line 1 marks Quinn as you, the operator, and this line marks Ada`,
		"Settings → Messages → Contacts",
	} {
		if !strings.Contains(said[0], want) {
			t.Errorf("the notice does not say %q:\n%s", want, said[0])
		}
	}
	// The whole of it, word for word: the operator reads all of it, and the
	// log carries the same sentence after the file it is about.
	const whole = `[contacts] The address book in config.json has 4 line(s) that Settings would refuse. It is in force as written: nothing was changed or left out. Line 2 ("Robin" on "Telegram"): a channel is the name its adapter gives (such as telegram): lowercase letters, digits, dot, underscore or hyphen, at most 32. Line 3 ("ContactB" on "telegram"): line 1 has the same channel and address: an address is one person's, listed once. Line 4 ("operator" on "mail"): the name operator is taken: sending to operator reaches this dashboard; use your own name and mark the line as you. Line 5 ("Ada" on "mail"): line 1 marks Quinn as you, the operator, and this line marks Ada: the operator is one person. Correct each on Settings → Messages → Contacts or in the file; until then Settings refuses any save of the address book.`
	if said[0] != whole {
		t.Errorf("the notice:\n got %s\nwant %s", said[0], whole)
	}
	if !strings.Contains(logs.String(), cfg.SourcePath+": "+whole) {
		t.Errorf("the log does not carry the sentence after the file it is about:\n%s", logs.String())
	}

	// The next start, the same book: nothing more, on the page or in the
	// log. The row says once, on the page and in the log.
	logs = logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	if again := doorNotices(t, a); len(again) != 1 || strings.Contains(logs.String(), "[contacts]") {
		t.Fatalf("THE SAME BOOK AT THE NEXT START WAS SAID AGAIN: one notice still, and no line in the log: %d notice(s)\n%s", len(again), logs.String())
	}

	// The next start with dashboard.operator_ascii the other way: the store
	// would keep the sentence in its other spelling under the same id, so
	// the notice is there already. No second notice, and the log does not
	// say the notice could not be written.
	a.store.SetOperatorASCII(true)
	logs = logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	if again := doorNotices(t, a); len(again) != 1 || again[0] != said[0] || strings.Contains(logs.String(), "[contacts]") || strings.Contains(logs.String(), "config.error") {
		t.Fatalf("the same book with the operator's spelling switched is the one notice still, and nothing in the log: %d notice(s)\n%s", len(again), logs.String())
	}
	a.store.SetOperatorASCII(false)

	// One line put right in the file: another book, said for what is left,
	// on the page and in the log.
	cfg.Contacts[3].Name = "Olive"
	logs = logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	if !strings.Contains(logs.String(), "has 3 line(s)") {
		t.Fatalf("a book corrected in part is not said in the log:\n%s", logs.String())
	}
	left := ""
	for _, n := range doorNotices(t, a) {
		if n != said[0] {
			left += n
		}
	}
	if len(doorNotices(t, a)) != 2 || !strings.Contains(left, "has 3 line(s)") || strings.Contains(left, "Line 4") || !strings.Contains(left, "Line 5") {
		t.Fatalf("a book corrected in part is said once more, for the lines still at fault: %q", left)
	}

	// Every line put right: nothing more is said, in either place.
	cfg.Contacts = []Contact{refusedBook()[0]}
	logs = logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	if len(doorNotices(t, a)) != 2 || strings.Contains(logs.String(), "[contacts]") {
		t.Fatalf("a book the door would keep was spoken of:\n%s", logs.String())
	}
}

// A RELOAD TAKES SUCH A BOOK WHOLE AND SAYS IT WHEN THE BOOK CHANGED. An
// edit of the file by hand puts the four lines at fault in force as
// written, with one notice; the reload that follows with the file
// unchanged says nothing more.
func TestAReloadKeepsABookTheDoorWouldRefuseWholeAndSaysItOnce(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(cfg)
	a.store = st
	a.live.Store(true)

	raw, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file["contacts"], err = json.Marshal(refusedBook()); err != nil {
		t.Fatal(err)
	}
	if raw, err = json.Marshal(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.SourcePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	logs := logsink.CaptureForTest(t)
	a.reloadConfig()
	if got := a.configSnapshot().Contacts; !reflect.DeepEqual(got, refusedBook()) {
		t.Fatalf("THE BOOK IN FORCE IS NOT THE FILE'S: %+v\n%s", got, logs.String())
	}
	said := doorNotices(t, a)
	if len(said) != 1 || !strings.Contains(said[0], "has 4 line(s) that Settings would refuse") || !strings.Contains(logs.String(), said[0]) {
		t.Fatalf("the reload that took the book did not say its lines once, on the page and in the log: %q\n%s", said, logs.String())
	}

	logs = logsink.CaptureForTest(t)
	a.reloadConfig()
	if len(doorNotices(t, a)) != 1 || strings.Contains(logs.String(), "[contacts]") {
		t.Fatalf("a reload of the unchanged file spoke of the book again:\n%s", logs.String())
	}
}

// WHAT THE FILE HOLDS IS QUOTED AND BOUNDED IN A LINE'S LABEL. A loaded
// line's name and channel are whatever the file holds, of any length: the
// label shows each within the bound the door holds a name to, the cut
// marked, beside the door's own requirement for the line.
func TestTheLabelOfALoadedLineBoundsWhatTheFileHolds(t *testing.T) {
	head, tail := maxContactName-maxContactName/4-1, maxContactName/4
	long := strings.Repeat("a", maxContactName) + strings.Repeat("z", maxContactName)
	shown := strings.Repeat("a", head) + "…" + strings.Repeat("z", tail)
	said := contactLinesRefusedSaid([]Contact{
		{Name: "Quinn", Channel: "telegram", Address: "111", Operator: true},
		{Name: long, Channel: "mail", Address: "a@example.test"},
		{Name: "Robin", Channel: long, Address: "222"},
	})
	for _, want := range []string{
		"has 2 line(s) that Settings would refuse",
		fmt.Sprintf(` Line 2 (%q on "mail"): a name is one line of at most %d characters.`, shown, maxContactName),
		fmt.Sprintf(` Line 3 ("Robin" on %q): a channel is the name its adapter gives (such as telegram): lowercase letters, digits, dot, underscore or hyphen, at most 32.`, shown),
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the sentence does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, long) {
		t.Errorf("a name or a channel of %d characters is in the sentence whole:\n%s", len(long), said)
	}
}

// THE LOG SAYS IT WHERE THE PAGE CANNOT. With no store there is no page to
// write to, and the log line is said all the same. With a store that
// refuses the write, the log says after it that the notice could not be
// written and why, and that the line above says the lines: it does not
// claim to be the only record, because an earlier start may have written
// the notice.
func TestTheLogSaysTheBooksLinesWhenTheNoticeCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{SourcePath: filepath.Join(dir, "config.json"), Contacts: refusedBook()}
	said := cfg.SourcePath + ": " + contactLinesRefusedSaid(cfg.Contacts)

	a := New(&Config{SourcePath: cfg.SourcePath})
	logs := logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	if !strings.Contains(logs.String(), said) || strings.Contains(logs.String(), "config.error") {
		t.Fatalf("with no store the log carries the sentence and no failure:\n%s", logs.String())
	}

	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	a.store = st
	logs = logsink.CaptureForTest(t)
	a.sayContactLinesTheDoorRefuses(cfg)
	out := logs.String()
	at := strings.Index(out, said)
	if at < 0 {
		t.Fatalf("the log does not carry the sentence:\n%s", out)
	}
	after := out[at+len(said):]
	open, shut := "the operator's notice of the address book's lines could not be written (", "): the line above says them"
	from, to := strings.Index(after, open), strings.Index(after, shut)
	if !strings.Contains(after, "config.error") || from < 0 || to < from+len(open)+1 {
		t.Fatalf("a notice that could not be written is not said after the sentence, with why:\n%s", out)
	}
}

// A LIVE START SAYS IT, AND A SAFE BOOT DOES NOT. The book the file holds is
// in force whole after a live start, with one notice of the lines the door
// would refuse and the sentence in the log after the file's path. A start
// that goes to SAFE says nothing of the book, in the log or on the page:
// it runs no plugin and sends nothing, so the book is not used there, and
// its database is read-only.
func TestALiveStartKeepsABookTheDoorWouldRefuseWholeAndSaysItAndASafeBootSaysNothing(t *testing.T) {
	boot := func(damaged bool) (*App, string) {
		t.Helper()
		dir := t.TempDir()
		key, record, database := birthFixture(t, dir, "Booked")
		buildPriorProjection(t, record, database)
		if damaged {
			tamperChain(t, key, record)
		}
		cfg := safebootConfig(t, dir, "Booked", key, record, database)
		cfg.Contacts = refusedBook()
		a := New(cfg)
		t.Cleanup(func() { stopApp(t, a) })
		logs := logsink.CaptureForTest(t)
		if err := a.startLive(); err != nil {
			t.Fatal(err)
		}
		return a, logs.String()
	}
	whole := contactLinesRefusedSaid(refusedBook())

	live, logs := boot(false)
	if reason, safe := live.SafeMode(); safe {
		t.Fatalf("the fixture must start live: %s", reason)
	}
	if got := live.configSnapshot().Contacts; !reflect.DeepEqual(got, refusedBook()) {
		t.Fatalf("THE BOOK IN FORCE AFTER A LIVE START IS NOT THE FILE'S: %+v", got)
	}
	if said := doorNotices(t, live); len(said) != 1 || said[0] != whole {
		t.Fatalf("a live start over a book the door would refuse leaves one notice of its lines, got %d: %q", len(said), said)
	}
	if !strings.Contains(logs, live.configSnapshot().SourcePath+": "+whole) {
		t.Fatalf("a live start did not say the lines in the log after the file's path:\n%s", logs)
	}

	safe, logs := boot(true)
	if _, is := safe.SafeMode(); !is {
		t.Fatal("the fixture must go to SAFE: the record was damaged")
	}
	if got := safe.configSnapshot().Contacts; !reflect.DeepEqual(got, refusedBook()) {
		t.Fatalf("THE BOOK A SAFE BOOT HOLDS IS NOT THE FILE'S: %+v", got)
	}
	if strings.Contains(logs, "[contacts]") {
		t.Fatalf("a SAFE boot spoke of the book in the log:\n%s", logs)
	}
	if safe.store != nil {
		if said := doorNotices(t, safe); len(said) != 0 {
			t.Fatalf("a SAFE boot wrote a notice of the book: %q", said)
		}
	}
}
