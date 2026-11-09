package dashboard

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// continuity_test.go — the person's door onto backups and the escrow, at
// the server: what each act reaches, what it never echoes, and when a
// passphrase may cross the socket at all.

type backupsRig struct {
	created, checked [][]byte // the passphrases the hooks were handed (copies)
	files            [][]byte
	takes, verifies  []string
	owned            int  // slow acts handed to the owner of their lifetime
	stopping         bool // the owner refuses new work
	noOwner          bool // no owner is wired at all
}

func (r *backupsRig) hooks() *ContinuityHooks {
	view := func() ContinuityView { return ContinuityView{Enabled: true, BackupKeep: 8} }
	background := func(work func()) bool {
		if r.stopping {
			return false
		}
		r.owned++
		work() // the owner runs it; a test needs no second goroutine to see it ran
		return true
	}
	if r.noOwner {
		background = nil
	}
	return &ContinuityHooks{
		Background: background,
		View:       view,
		Take: func(context.Context) (ContinuityView, error) {
			r.takes = append(r.takes, "take")
			return view(), nil
		},
		Verify: func(_ context.Context, name string) (string, error) {
			r.verifies = append(r.verifies, name)
			if name == "rotted" {
				return "", errors.New("rotted does NOT prove")
			}
			return "proved: " + name, nil
		},
		EscrowCreate: func(pass []byte) ([]byte, string, error) {
			r.created = append(r.created, append([]byte(nil), pass...))
			return []byte("sealed-bytes"), "escrow-abc-20260920.age", nil
		},
		EscrowCheck: func(sealed, pass []byte) (ContinuityView, error) {
			r.files = append(r.files, append([]byte(nil), sealed...))
			r.checked = append(r.checked, append([]byte(nil), pass...))
			v := view()
			v.Encrypting, v.EscrowCovers = true, true
			return v, nil
		},
	}
}

func backupsConn(t *testing.T, rig *backupsRig) *websocket.Conn {
	t.Helper()
	conn, _ := backupsConnAs(t, rig, true)
	return conn
}

// backupsConnAs is a page on a dashboard that asks for its token (and the
// page signed in with it) or asks for none.
func backupsConnAs(t *testing.T, rig *backupsRig, token bool) (*websocket.Conn, *Server) {
	t.Helper()
	s := New("127.0.0.1", 0, &WSHandler{Continuity: rig.hooks()})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	if !token {
		return dialWS(t, addr), s
	}
	s.SetAccessToken(true, "the-backups-tests-token")
	conn, err := dialWSToken(addr, s.accessHash(), pageKeyFor(s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, s
}

func askBackups(t *testing.T, conn *websocket.Conn, id string, req ContinuityRequest) *ContinuityReply {
	t.Helper()
	sendMsg(t, conn, ClientMessage{RequestID: id, Type: "backups", Backups: &req})
	m := drainUntil(t, conn, "backups")
	if m.RequestID != id || m.Backups == nil {
		t.Fatalf("reply to %s: %+v", id, m)
	}
	return m.Backups
}

// EACH ACT REACHES ITS HOOK AND ANSWERS ITS ASKER, and the view rides every
// answer — refusals too — so the page shows the state the act left.
func TestTheBackupsDoorReachesEachAct(t *testing.T) {
	rig := &backupsRig{}
	conn := backupsConn(t, rig)
	if r := askBackups(t, conn, "s1", ContinuityRequest{Action: "status"}); r.View == nil || !r.View.Enabled || r.View.BackupKeep != 8 || !r.View.SecretsOK {
		t.Fatalf("status over a loopback connection: %+v", r)
	}
	if r := askBackups(t, conn, "t1", ContinuityRequest{Action: "take"}); r.Error != "" || r.View == nil || len(rig.takes) != 1 {
		t.Fatalf("take: %+v %v", r, rig.takes)
	}
	if r := askBackups(t, conn, "v1", ContinuityRequest{Action: "verify", Name: "ledger-x"}); r.Said != "proved: ledger-x" || r.Error != "" {
		t.Fatalf("verify: %+v", r)
	}
	if r := askBackups(t, conn, "v2", ContinuityRequest{Action: "verify", Name: "rotted"}); !strings.Contains(r.Error, "does NOT prove") || r.View == nil {
		t.Fatalf("a snapshot that does not prove: %+v", r)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "x1", Type: "backups", Backups: &ContinuityRequest{Action: "restore"}})
	if m := drainUntil(t, conn, "error"); m.RequestID != "x1" {
		t.Fatalf("an act this door does not have must be refused to its asker: %+v", m)
	}
}

// THE ESCROW ACTS: two passphrases are held to each other HERE — the page's
// comparison is a courtesy — the sealed file is what goes back and the
// passphrase never does; the picked file arrives byte for byte.
func TestTheEscrowActsNeverEchoAPassphrase(t *testing.T) {
	rig := &backupsRig{}
	conn := backupsConn(t, rig)
	const pass = "correct horse battery staple"

	r := askBackups(t, conn, "c0", ContinuityRequest{Action: "escrow_create", Passphrase: pass, Again: pass + "x"})
	if !strings.Contains(r.Error, "differ") || len(rig.created) != 0 || r.FileB64 != "" {
		t.Fatalf("two passphrases that differ must be refused by the server, whatever the page did: %+v", r)
	}
	r = askBackups(t, conn, "c1", ContinuityRequest{Action: "escrow_create", Passphrase: pass, Again: pass})
	file, _ := base64.StdEncoding.DecodeString(r.FileB64)
	if r.Error != "" || string(file) != "sealed-bytes" || r.FileName != "escrow-abc-20260920.age" || len(rig.created) != 1 || string(rig.created[0]) != pass {
		t.Fatalf("create: %+v, hook got %q", r, rig.created)
	}
	picked := []byte{0, 1, 2, 250, 255}
	r2 := askBackups(t, conn, "k1", ContinuityRequest{Action: "escrow_check", Passphrase: pass, FileB64: base64.StdEncoding.EncodeToString(picked)})
	if r2.Error != "" || r2.View == nil || !r2.View.Encrypting || len(rig.files) != 1 || string(rig.files[0]) != string(picked) || string(rig.checked[0]) != pass {
		t.Fatalf("check: %+v files=%v", r2, rig.files)
	}
	for _, reply := range []*ContinuityReply{r, r2} {
		if raw := reply.Error + reply.Said + reply.FileName; strings.Contains(raw, pass) {
			t.Errorf("A REPLY ECHOED THE PASSPHRASE: %q", raw)
		}
	}
	// A file past the bound, and one that is not base64, never reach the hook.
	if r := askBackups(t, conn, "k2", ContinuityRequest{Action: "escrow_check", Passphrase: pass, FileB64: strings.Repeat("A", maxEscrowFileB64+4)}); r.Error == "" || len(rig.files) != 1 {
		t.Errorf("a file larger than any escrow reached the hook: %+v", r)
	}
	if r := askBackups(t, conn, "k3", ContinuityRequest{Action: "escrow_check", Passphrase: pass, FileB64: "not base64 !!"}); r.Error == "" || len(rig.files) != 1 {
		t.Errorf("a file that did not arrive whole reached the hook: %+v", r)
	}
}

// A PASSPHRASE CROSSES ONLY A PRIVATE CONNECTION. Over TLS or from this
// machine it is accepted; on any other connection the escrow acts are refused
// BEFORE the hook sees a byte of it, the refusal says what to change, and
// every view tells the page not to offer the forms.
func TestAPassphraseIsRefusedOnAConnectionThatIsNotPrivate(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:51000": true, "[::1]:51000": true, "192.168.1.20:51000": false, "10.0.0.5:4000": false, "garbage": false} {
		if got := remoteIsLoopback(addr); got != want {
			t.Errorf("remoteIsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
	rig := &backupsRig{}
	s := New("127.0.0.1", 0, &WSHandler{Continuity: rig.hooks()})
	for _, action := range []string{"escrow_create", "escrow_check"} {
		var got *ContinuityReply
		s.answerBackupsForTest(ClientMessage{RequestID: "p", Type: "backups", Backups: &ContinuityRequest{Action: action, Passphrase: "correct horse battery", Again: "correct horse battery", FileB64: "AAEC"}}, false, func(r ContinuityReply) { got = &r })
		if got == nil || !strings.Contains(got.Error, "isn't private") || !strings.Contains(got.Error, "Serve over HTTPS") || got.FileB64 != "" {
			t.Errorf("%s on a connection that is not private: %+v", action, got)
		}
		if got != nil && (got.View == nil || got.View.SecretsOK) {
			t.Errorf("%s: the view must tell the page not to offer the forms: %+v", action, got.View)
		}
	}
	if len(rig.created)+len(rig.checked) != 0 {
		t.Fatalf("A PASSPHRASE FROM A CONNECTION THAT IS NOT PRIVATE REACHED THE HOST: %q %q", rig.created, rig.checked)
	}
	// The other acts carry no secret and are answered on any admitted connection.
	var got *ContinuityReply
	s.answerBackupsForTest(ClientMessage{Type: "backups", Backups: &ContinuityRequest{Action: "status"}}, false, func(r ContinuityReply) { got = &r })
	if got == nil || got.View == nil || got.View.SecretsOK {
		t.Fatalf("status on a connection that is not private: %+v", got)
	}
}

// answerBackupsForTest asks the door as a connection of the given privacy
// would, without a socket: a test cannot dial from another machine.
func (s *Server) answerBackupsForTest(msg ClientMessage, secretsOK bool, got func(ContinuityReply)) {
	s.answerBackups(context.Background(), msg, secretsOK, false, got, func(string) {})
}

// A RESTORE AT THE DOOR: the plan is read, a refusal restarts nothing, and a
// request that was written is answered BEFORE the runtime goes down — so the
// page can say what is happening.
func TestTheRestoreActsAtTheDoor(t *testing.T) {
	rig := &backupsRig{}
	hooks := rig.hooks()
	var asked [][3]string
	hooks.RestorePlan = func(source, name string) (RestorePlan, error) {
		return RestorePlan{Source: source, Name: name, RestoredTo: 1300, LiveThrough: 1402, SetAside: 102, Witnessed: 1368, WitnessedSetAside: 68, ConfirmText: "Example"}, nil
	}
	hooks.Restore = func(source, name, confirm string) error {
		asked = append(asked, [3]string{source, name, confirm})
		if confirm != "Example" {
			return errors.New(`To confirm, type "Example" exactly.`)
		}
		return nil
	}
	restarts := 0
	s := New("127.0.0.1", 0, &WSHandler{Continuity: hooks, Restart: func() error { restarts++; return nil }})
	var got []ContinuityReply
	send := func(req ContinuityRequest) {
		s.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false, func(r ContinuityReply) { got = append(got, r) }, func(string) {})
	}
	send(ContinuityRequest{Action: "restore_plan", Source: "snapshot", Name: "ledger-x"})
	if len(got) != 1 || got[0].Plan == nil || got[0].Plan.SetAside != 102 || got[0].Plan.WitnessedSetAside != 68 || got[0].View == nil {
		t.Fatalf("the plan: %+v", got)
	}
	send(ContinuityRequest{Action: "restore", Source: "snapshot", Name: "ledger-x", Confirm: "example"})
	if restarts != 0 || got[1].Error == "" || got[1].Restarting {
		t.Fatalf("A RESTORE THAT WAS REFUSED RESTARTED THE RUNTIME: restarts=%d %+v", restarts, got[1])
	}
	send(ContinuityRequest{Action: "restore", Source: "set-aside", Name: "Example-x", Confirm: "Example"})
	if restarts != 1 || !got[2].Restarting || len(asked) != 2 || asked[1] != [3]string{"set-aside", "Example-x", "Example"} {
		t.Fatalf("a confirmed restore: restarts=%d %+v asked=%v", restarts, got[2], asked)
	}
}

// FIRST BOOT'S RESTORE DOOR AT THE SERVER: offered only where the runtime
// gives it, private connections only — refused BEFORE the passphrase is read
// — and the upload route walled like every other state-changing route.
func TestTheNewMachineDoorAtTheServer(t *testing.T) {
	var gotKeys [][2]string
	var uploads []string
	hooks := &RestoreNewHooks{
		Keys: func(sealed, pass []byte) (RestoreNewKeys, error) {
			gotKeys = append(gotKeys, [2]string{string(sealed), string(pass)})
			return RestoreNewKeys{Identity: "5012", HoldsSnapshotKey: true}, nil
		},
		Upload: func(snapshot, member string, body io.Reader) error {
			raw, _ := io.ReadAll(body)
			uploads = append(uploads, snapshot+"|"+member+"|"+string(raw))
			if snapshot == "bad" {
				return errors.New("That is not a snapshot's name.")
			}
			return nil
		},
		Restore: func(context.Context, string, *GenesisRequest) (RestoreNewDone, error) {
			return RestoreNewDone{Identity: "5012", RestoredTo: 9}, nil
		},
	}
	s := New("127.0.0.1", 0, &WSHandler{RestoreNew: hooks})
	ask := func(private bool, req RestoreNewRequest) (got *RestoreNewReply, refused string) {
		s.answerRestoreNew(context.Background(), ClientMessage{Type: "restore_new", RestoreNew: &req}, private,
			func(r RestoreNewReply) { got = &r }, func(text string) { refused = text })
		return got, refused
	}
	file := base64.StdEncoding.EncodeToString([]byte("sealed"))
	if r, _ := ask(false, RestoreNewRequest{Action: "keys", FileB64: file, Passphrase: "correct horse battery"}); r == nil || !strings.Contains(r.Error, "not private") || r.SecretsOK || len(gotKeys) != 0 {
		t.Fatalf("A PASSPHRASE FROM A CONNECTION THAT IS NOT PRIVATE REACHED THE HOST: %+v %v", r, gotKeys)
	}
	if r, _ := ask(false, RestoreNewRequest{Action: "hello"}); r == nil || r.SecretsOK || r.Error != "" {
		t.Fatalf("hello must say whether the connection may carry the door: %+v", r)
	}
	r, _ := ask(true, RestoreNewRequest{Action: "keys", FileB64: file, Passphrase: "correct horse battery"})
	if r == nil || r.Keys == nil || r.Keys.Identity != "5012" || len(gotKeys) != 1 || gotKeys[0] != [2]string{"sealed", "correct horse battery"} {
		t.Fatalf("keys over a private connection: %+v %v", r, gotKeys)
	}
	if strings.Contains(r.Error+r.Keys.Identity, "correct horse") {
		t.Error("a reply echoed the passphrase")
	}
	if r, _ := ask(true, RestoreNewRequest{Action: "keys", FileB64: strings.Repeat("A", maxEscrowFileB64+4), Passphrase: "x"}); r == nil || r.Error == "" || len(gotKeys) != 1 {
		t.Errorf("a file larger than any escrow reached the hook: %+v", r)
	}
	if _, refused := ask(true, RestoreNewRequest{Action: "delete"}); refused == "" {
		t.Error("an act this door does not have was answered")
	}
	// Once an identity lives here the door is gone.
	live := New("127.0.0.1", 0, &WSHandler{})
	var refused string
	live.answerRestoreNew(context.Background(), ClientMessage{Type: "restore_new", RestoreNew: &RestoreNewRequest{Action: "hello"}}, true, func(RestoreNewReply) {}, func(text string) { refused = text })
	if !strings.Contains(refused, "first boot only") {
		t.Errorf("the door on a machine that holds an identity: %q", refused)
	}

	// THE UPLOAD ROUTE.
	post := func(srv *Server, remote, origin, query, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/restore/upload?"+query, strings.NewReader(body))
		req.RemoteAddr = remote
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		srv.handleRestoreUpload(deadlineRecorder{w}, req)
		return w
	}
	// A connection that takes no read deadline is refused before anything
	// is read: nothing would end an upload over it that stopped.
	bare, plain := httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "http://127.0.0.1/restore/upload?snapshot=ledger-20260920T101500Z-seq7.tar.age", strings.NewReader("bytes"))
	plain.RemoteAddr = "127.0.0.1:5000"
	s.handleRestoreUpload(bare, plain)
	if bare.Code != http.StatusInternalServerError || !strings.Contains(bare.Body.String(), "time limit") || len(uploads) != 0 {
		t.Fatalf("an upload over a connection with no deadline reached the host: %d %q %v", bare.Code, bare.Body.String(), uploads)
	}
	if w := post(s, "127.0.0.1:5000", "", "snapshot=ledger-20260920T101500Z-seq7.tar.age", "bytes"); w.Code != http.StatusNoContent || len(uploads) != 1 || uploads[0] != "ledger-20260920T101500Z-seq7.tar.age||bytes" {
		t.Fatalf("an upload from this machine: %d %v", w.Code, uploads)
	}
	if w := post(s, "127.0.0.1:5000", "", "snapshot=ledger-x&member=witness-keys%2Fk.json", "k"); w.Code != http.StatusNoContent || uploads[1] != "ledger-x|witness-keys/k.json|k" {
		t.Fatalf("a folder's member: %d %v", w.Code, uploads)
	}
	if w := post(s, "192.168.1.9:5000", "", "snapshot=ledger-x", "bytes"); w.Code != http.StatusForbidden || len(uploads) != 2 {
		t.Fatalf("AN IDENTITY IN THE CLEAR WAS ACCEPTED FROM ANOTHER MACHINE OVER PLAIN HTTP: %d %v", w.Code, uploads)
	}
	if w := post(s, "127.0.0.1:5000", "https://evil.example", "snapshot=ledger-x", "bytes"); w.Code != http.StatusForbidden || len(uploads) != 2 {
		t.Fatalf("an upload from another site: %d", w.Code)
	}
	if w := post(s, "127.0.0.1:5000", "", "snapshot=bad", "bytes"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not a snapshot") {
		t.Fatalf("a refusal must reach the page in the host's words: %d %q", w.Code, w.Body.String())
	}
	if w := post(live, "127.0.0.1:5000", "", "snapshot=ledger-x", "bytes"); w.Code != http.StatusNotFound {
		t.Fatalf("the route on a machine that holds an identity: %d", w.Code)
	}
}

// SLOW WORK BELONGS TO WHOEVER CAN WAIT FOR IT. Taking and proving a snapshot
// run for as long as a database takes to copy, and read the store again when
// they finish. The door does not launch them itself: it hands them to the
// owner of their lifetime, says so when that owner is stopping, and starts
// nothing when there is no owner — a goroutine nobody joins is how Stop came
// to close the store under a reader.
func TestSlowBackupActsAreOwnedNotDetached(t *testing.T) {
	ask := func(rig *backupsRig, action, name string) (replies []ContinuityReply, refused []string) {
		s := New("127.0.0.1", 0, &WSHandler{Continuity: rig.hooks()})
		req := ContinuityRequest{Action: action, Name: name}
		s.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false,
			func(r ContinuityReply) { replies = append(replies, r) }, func(why string) { refused = append(refused, why) })
		return replies, refused
	}
	for _, act := range []struct{ action, name string }{{"take", ""}, {"verify", "ledger-x"}} {
		rig := &backupsRig{}
		replies, refused := ask(rig, act.action, act.name)
		if rig.owned != 1 || len(replies) != 1 || len(refused) != 0 || len(rig.takes)+len(rig.verifies) != 1 {
			t.Errorf("%s: handed to its owner %d times, %d replies, refused %v", act.action, rig.owned, len(replies), refused)
		}

		rig = &backupsRig{stopping: true}
		replies, refused = ask(rig, act.action, act.name)
		if len(rig.takes)+len(rig.verifies) != 0 || len(replies) != 0 || len(refused) != 1 || !strings.Contains(refused[0], "stopping") {
			t.Errorf("%s WHILE STOPPING must be refused and not started: ran=%d replies=%d refused=%v", act.action, len(rig.takes)+len(rig.verifies), len(replies), refused)
		}

		rig = &backupsRig{noOwner: true}
		replies, refused = ask(rig, act.action, act.name)
		if len(rig.takes)+len(rig.verifies) != 0 || len(replies) != 0 || len(refused) != 1 {
			t.Errorf("%s WITH NO OWNER ran detached: ran=%d replies=%d refused=%v", act.action, len(rig.takes)+len(rig.verifies), len(replies), refused)
		}
	}
}

func TestTheKeysLeaveOnlyForAPageSignedInWithTheToken(t *testing.T) {
	const pass = "correct horse battery staple"
	create := ContinuityRequest{Action: "escrow_create", Passphrase: pass, Again: pass}

	rig := &backupsRig{}
	open, _ := backupsConnAs(t, rig, false)
	if r := askBackups(t, open, "s0", ContinuityRequest{Action: "status"}); r.View == nil || r.View.ExportOK || !strings.Contains(r.View.ExportWhyNot, "Ask every device to sign in") {
		t.Fatalf("a page under no token was offered the export: %+v", r.View)
	}
	r := askBackups(t, open, "c0", create)
	if r.FileB64 != "" || !strings.HasPrefix(r.Error, "To export the keys, first turn on") || len(rig.created) != 0 {
		t.Fatalf("the keys were exported, or the passphrase reached the host, under no token: %+v created=%q", r, rig.created)
	}
	if r := askBackups(t, open, "k0", ContinuityRequest{Action: "escrow_check", Passphrase: pass, FileB64: "AAEC"}); r.Error != "" {
		t.Fatalf("checking a file the operator has, which exports nothing, was refused: %+v", r)
	}

	signed, s := backupsConnAs(t, rig, true)
	if r := askBackups(t, signed, "s1", ContinuityRequest{Action: "status"}); r.View == nil || !r.View.ExportOK || r.View.ExportWhyNot != "" {
		t.Fatalf("the signed-in page was not offered the export: %+v", r.View)
	}
	if r := askBackups(t, signed, "c1", create); r.Error != "" || r.FileB64 == "" || len(rig.created) != 1 {
		t.Fatalf("the signed-in page could not export the keys: %+v", r)
	}

	var got *ContinuityReply
	s.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &create}, true, false, func(r ContinuityReply) { got = &r }, func(string) {})
	if got == nil || got.FileB64 != "" || !strings.Contains(got.Error, "isn't signed in") || len(rig.created) != 1 {
		t.Fatalf("a page not signed in with the token in force exported the keys: %+v", got)
	}
}

// deadlineRecorder is a recorder whose connection takes a read deadline,
// as a served one does (http.ResponseController).
type deadlineRecorder struct{ *httptest.ResponseRecorder }

func (deadlineRecorder) SetReadDeadline(time.Time) error { return nil }

func TestASnapshotDownloadIsWalledAndHandsOverTheKeptFile(t *testing.T) {
	const name = "ledger-20261020T040005Z-seq1402.tar.age"
	const kept = "age-encryption.org/v1 sealed snapshot bytes"
	for _, tc := range []struct {
		what, method, origin, site string
		signedIn                   bool
		form                       string
		status                     int
		asked                      bool
	}{
		{"the operator's page", http.MethodPost, "http://localhost", "same-origin", true, "name=" + name, 200, true},
		{"not signed in", http.MethodPost, "http://localhost", "same-origin", false, "name=" + name, 401, false},
		{"another site", http.MethodPost, "https://foreign.invalid", "cross-site", true, "name=" + name, 403, false},
		{"a sibling site", http.MethodPost, "http://localhost", "same-site", true, "name=" + name, 403, false},
		{"a link", http.MethodGet, "", "", true, "", 405, false},
		{"a snapshot the runtime refuses", http.MethodPost, "http://localhost", "same-origin", true, "name=plain", 409, true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			var asked []string
			s := New("localhost", 0, &WSHandler{Continuity: &ContinuityHooks{Download: func(n string) (io.ReadCloser, int64, error) {
				asked = append(asked, n)
				if n != name {
					return nil, 0, errors.New("That snapshot is not encrypted, so it is not offered for download.")
				}
				return io.NopCloser(strings.NewReader(kept)), int64(len(kept)), nil
			}}})
			s.SetAccessToken(true, "test-token")
			r := httptest.NewRequest(tc.method, "http://localhost/backups/download", strings.NewReader(tc.form))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.signedIn {
				r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
				r.Header.Set(pageKeyHeader, pageKeyFor(s))
			}
			w := httptest.NewRecorder()
			s.AllowHost(r.Host)
			s.server.Handler.ServeHTTP(w, r)
			if w.Code != tc.status && !(tc.method == http.MethodGet && w.Code == http.StatusNotFound) {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
			if (len(asked) > 0) != tc.asked {
				t.Fatalf("the runtime was asked %v", asked)
			}
			if tc.status == 409 && !strings.Contains(w.Body.String(), "not encrypted") {
				t.Errorf("the runtime's refusal is not said: %s", w.Body)
			}
			if tc.status != 200 {
				return
			}
			if asked[0] != name || w.Body.String() != kept || w.Header().Get("Content-Length") != strconv.Itoa(len(kept)) ||
				w.Header().Get("Content-Disposition") != `attachment; filename="`+name+`"` {
				t.Fatalf("not the kept file: asked %v, %v %q", asked, w.Header(), w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Error("a snapshot download can be cached")
			}
		})
	}
}
