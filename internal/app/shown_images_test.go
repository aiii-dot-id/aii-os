package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// shownPNG is a real picture, four pixels by three: what a file is, is
// read from what it holds.
var shownPNG = func() []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

// catalogueSaysImages makes a public catalogue's word for the running
// model "takes images", with no row of the operator's: the prior.
func catalogueSaysImages(t *testing.T, app *App) {
	t.Helper()
	app.modalities.put("fixture-model", modelModalities{in: []string{"text", "image"}, out: []string{"text"}})
	provider, model := app.currentProvider().Name, app.llmSwap.Current().ModelName()
	app.providers.runtime.mu.Lock()
	app.providers.runtime.substrateCap = substrateCapability{provider: provider, model: model, toolCalls: capYes, modalityURL: "fixture-model"}
	app.providers.runtime.mu.Unlock()
}

// imageApp is promptCacheApp with an image in the identity's uploads
// folder and, when row is not nil, a registry row for the model that
// answers.
func imageApp(t *testing.T, row *modelCapability) (*App, *contScript, string) {
	t.Helper()
	app, obs := promptCacheApp(t)
	home := app.configSnapshot().Tools.CWD
	if err := os.MkdirAll(filepath.Join(home, "uploads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "uploads", "proof.png"), shownPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if row != nil {
		setModelRow(t, app, "fake", *row)
	}
	return app, obs, "data:image/png;base64," + base64.StdEncoding.EncodeToString(shownPNG)
}

func setModelRow(t *testing.T, app *App, model string, row modelCapability) {
	t.Helper()
	reg, err := loadProvidersFile(app.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	if reg.ModelCapabilities == nil {
		reg.ModelCapabilities = map[string]modelCapability{}
	}
	reg.ModelCapabilities[model] = row
	if _, err := saveProvidersFile(app.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
}

func boolp(v bool) *bool { return &v }

// AN ATTACHED IMAGE IS SHOWN TO A MODEL THAT TAKES IMAGES, IN THE TURN IT
// ARRIVES. The request that answers the message carries the picture
// beside its words. The next turn carries the words and the file's line,
// and no picture: the record never held the bytes.
func TestAnAttachedImageIsShownToAModelThatTakesImages(t *testing.T) {
	app, obs, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
	operatorTurn(t, app, "and the next thing")
	reqs := obs.snapshot()
	if len(reqs) < 2 {
		t.Fatalf("two turns made %d requests", len(reqs))
	}
	if strings.Count(reqs[0], dataURL) != 1 {
		t.Fatalf("the turn that answers the message does not show the picture once (%d times)", strings.Count(reqs[0], dataURL))
	}
	if !strings.Contains(reqs[0], "your operator attached a file: uploads/proof.png") {
		t.Fatal("the picture came without its file's line")
	}
	last := reqs[len(reqs)-1]
	if strings.Contains(last, "image_url") || strings.Contains(last, base64.StdEncoding.EncodeToString(shownPNG)) {
		t.Fatal("a later turn sent the picture again")
	}
	if !strings.Contains(last, "your operator attached a file: uploads/proof.png") {
		t.Fatal("a later turn lost the file's line")
	}
}

// UNKNOWN AND NO ARE THE SAME ACT: the picture is not sent, and the file's
// line still is. So is a picture over the model's bound, and a file that
// is not an image.
func TestAnImageIsNotShownWhereNobodySaidTheModelTakesOne(t *testing.T) {
	for _, c := range []struct {
		name string
		row  *modelCapability
		path string
	}{
		{"nobody has said", nil, "uploads/proof.png"},
		{"the row says no", &modelCapability{Effort: []string{}, Images: boolp(false)}, "uploads/proof.png"},
		{"over the row's bound", &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxBytes: 8}, "uploads/proof.png"},
		{"not an image", &modelCapability{Effort: []string{}, Images: boolp(true)}, "uploads/notes.txt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, obs, _ := imageApp(t, c.row)
			if err := os.WriteFile(filepath.Join(app.configSnapshot().Tools.CWD, "uploads", "notes.txt"), []byte("notes"), 0o600); err != nil {
				t.Fatal(err)
			}
			attachedTurn(t, app, "what is this?", []string{c.path})
			req := obs.snapshot()[0]
			if strings.Contains(req, "image_url") {
				t.Fatal("a picture was sent")
			}
			if !strings.Contains(req, "your operator attached a file: "+c.path) {
				t.Fatal("the file's line was not sent")
			}
			// A picture a bound left out is declared with the bound, after
			// the file's line; nothing else is.
			if declared := strings.Contains(req, "[not shown to you: uploads/proof.png, its "); declared != (c.name == "over the row's bound") {
				t.Fatalf("declared as left out by a bound: %v", declared)
			}
		})
	}
}

// pictureNotices are the notices of one reason beside the operator
// message whose words are given, oldest first.
func pictureNotices(t *testing.T, a *App, reason, words string) []string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT n.content FROM conversations n JOIN conversations m ON n.related_id=m.id
		WHERE n.role='system' AND n.kind='notice' AND json_extract(n.details,'$.reason_code')=? AND m.role='operator' AND m.content=? ORDER BY n.turn_seq`, reason, words)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatal(err)
		}
		out = append(out, content)
	}
	return out
}

// THE ROW PRICES AND BOUNDS AN IMAGE, AND THE DEFAULTS STAND WHERE IT IS
// SILENT. A picture a bound leaves out is declared with the bound, in a
// line the message carries after the file's own.
func TestTheModelsRowPricesAndBoundsAnImage(t *testing.T) {
	app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	// decide asks which of a new message's files are sent as pictures.
	decide := func(paths ...string) ([]llm.Image, string) {
		t.Helper()
		ref, err := app.recordConversationRef(t.Context(), "operator", "look at these")
		if err != nil {
			t.Fatal(err)
		}
		row := store.ConversationTurn{ID: ref.ID, Attached: paths}
		images, err := app.picturesWith(withTurnImages(t.Context(), "", ""), &row)
		if err != nil {
			t.Fatal(err)
		}
		return images, notShownLines(row.NotShown)
	}
	if p := app.imagePolicy(t.Context()); p.takes != capYes || p.tokens != defaultImageTokens || p.maxBytes != defaultImageMaxBytes ||
		p.maxTotal != defaultImagesMaxBytes || p.maxSide != defaultImageMaxSide || p.maxCount != defaultImagesMaxCount {
		t.Fatalf("a row that says only yes: %+v", p)
	}
	setModelRow(t, app, "fake", modelCapability{Effort: []string{}, Images: boolp(true), ImageTokens: 4800, ImageMaxBytes: 1 << 20})
	if p := app.imagePolicy(t.Context()); p.tokens != 4800 || p.maxBytes != 1<<20 {
		t.Fatalf("the row's figures were not taken: %+v", p)
	}
	images, declared := decide("uploads/proof.png", "uploads/absent.png", "../outside.png")
	if len(images) != 1 || images[0].MediaType != "image/png" || images[0].Tokens != 4800 || string(images[0].Data) != string(shownPNG) || declared != "" {
		t.Fatalf("shown images: %d, declared %q", len(images), declared)
	}
	size := int64(len(shownPNG))
	for _, c := range []struct {
		name  string
		row   modelCapability
		shown int
		says  string
	}{
		{"one picture's bytes", modelCapability{ImageMaxBytes: size - 1}, 0, fmt.Sprintf("[not shown to you: uploads/proof.png, its %d bytes are past this model's bound of %d for one picture]", size, size-1)},
		{"one picture's longer side", modelCapability{ImageMaxSide: 3}, 0, "[not shown to you: uploads/proof.png, 4 by 3 pixels is past this model's bound of 3 a side]"},
		{"a request's sum", modelCapability{ImagesMaxBytes: size + 1}, 1, fmt.Sprintf("[not shown to you: uploads/proof.png, with it this turn's pictures pass the %d bytes one request carries; a later turn can read it]", size+1)},
		{"a request's count", modelCapability{ImagesMaxCount: 1}, 1, "[not shown to you: uploads/proof.png, this turn already carries 1 pictures, the most one request takes; a later turn can read it]"},
	} {
		c.row.Effort, c.row.Images = []string{}, boolp(true)
		setModelRow(t, app, "fake", c.row)
		images, declared := decide("uploads/proof.png", "uploads/proof.png")
		if len(images) != c.shown || !strings.Contains(declared, c.says) || strings.Count(declared, "[not shown to you: ") != 2-c.shown {
			t.Errorf("%s: %d picture(s) shown, declared %q; want %d and %q", c.name, len(images), declared, c.shown, c.says)
		}
	}
}

// WHAT BECOMES OF AN ATTACHED PICTURE IS ON THE RECORD, WHERE THE OPERATOR
// READS THE TURN. A picture sent to the model is one notice beside the
// message; a picture a bound left out is one notice with the bound, and
// the identity is told it from that notice, on the turn it arrives and on
// the turns after. Nothing the identity is told about a picture is told
// to the identity alone.
func TestWhatBecomesOfAnAttachedPictureIsOnTheRecord(t *testing.T) {
	t.Run("sent", func(t *testing.T) {
		app, obs, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
		attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
		if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 1 || got[0] != "uploads/proof.png" {
			t.Fatalf("the notice that the picture was sent: %q", got)
		}
		if got := pictureNotices(t, app, store.ReasonNotShown, "what is this?"); len(got) != 0 {
			t.Fatalf("a picture that was sent is also noted as not shown: %q", got)
		}
		if n := strings.Count(obs.snapshot()[0], dataURL); n != 1 {
			t.Fatalf("the request carries the picture %d times", n)
		}
	})
	t.Run("left out by a bound", func(t *testing.T) {
		app, obs, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxBytes: 8})
		attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
		operatorTurn(t, app, "and the next thing")
		notes := pictureNotices(t, app, store.ReasonNotShown, "what is this?")
		if len(notes) != 1 || !strings.HasPrefix(notes[0], "uploads/proof.png, its ") || !strings.Contains(notes[0], "bound of 8 for one picture") {
			t.Fatalf("the notice that the picture was not sent, with the bound: %q", notes)
		}
		if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 0 {
			t.Fatalf("a picture left out is noted as sent: %q", got)
		}
		reqs := obs.snapshot()
		line := "[your operator attached a file: uploads/proof.png]\\n[not shown to you: " + notes[0] + "]"
		for i, req := range []string{reqs[0], reqs[len(reqs)-1]} {
			if strings.Count(req, line) != 1 {
				t.Fatalf("request %d does not tell the identity what the record holds, once, after the file's line", i)
			}
		}
	})
}

// "SHOWN" IS WRITTEN OF A PICTURE A MODEL ANSWERED WITH, AND OF NO OTHER.
// A turn whose one request is never answered (the address is not found,
// the rate is refused) fails with its picture sent to nobody: the record
// says the picture was not shown and why, never that it was, and a later
// turn tells the identity so and does not send it again.
func TestAPictureWhoseRequestWasNotAnsweredIsNotOnTheRecordAsShown(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
			fail := true
			obs := &contScript{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				obs.add(string(body))
				if fail {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, textReply("noted"))
			}))
			t.Cleanup(srv.Close)
			app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))

			ctx, cancel := context.WithTimeout(dashboard.WithAttachments(context.Background(), []string{"uploads/proof.png"}), 60*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := app.observeChat(ctx, "what is this?", func(kind, name, args string) {}); err == nil {
				t.Fatal("the fixture provider did not fail the turn")
			}
			if first := obs.snapshot(); len(first) == 0 || !strings.Contains(first[0], dataURL) {
				t.Fatal("the fixture proves nothing: the failed request carried no picture")
			}
			if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 0 {
				t.Fatalf("a picture nobody answered is on the record as shown: %q", got)
			}
			notes := pictureNotices(t, app, store.ReasonNotShown, "what is this?")
			if len(notes) != 1 || !strings.HasPrefix(notes[0], "uploads/proof.png, the request that carried it was not answered") {
				t.Fatalf("the record beside the message: %q", notes)
			}
			fail = false
			before := len(obs.snapshot())
			operatorTurn(t, app, "are you there?")
			later := obs.snapshot()[before:]
			if strings.Contains(later[0], dataURL) || !strings.Contains(later[0], "[not shown to you: "+notes[0]+"]") {
				t.Fatal("the next turn sent the picture again, or does not tell the identity it was not shown")
			}
		})
	}
}

// A PICTURE DELIVERED INTO A RUNNING TURN IS TOLD, NOT SHOWN, AND SAYS
// SO. Words that arrive while a turn runs are delivered at a tool
// boundary; a picture that came with them is told by its line. The
// identity is told that too, and the operator reads it on the record: the
// chip was drawn before the turn began.
func TestAPictureDeliveredIntoARunningTurnIsToldNotShown(t *testing.T) {
	app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	const words = "and this picture, which came while you worked"
	steered := make(chan error, 1)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		if n == 1 {
			ok, err := app.steer(roleOperator, words, nil, dashboard.WithAttachments(t.Context(), []string{"uploads/proof.png"}))
			if err == nil && !ok {
				err = fmt.Errorf("the steer was not queued")
			}
			steered <- err
			fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
			return
		}
		fmt.Fprint(w, textReply("noted"))
	})
	operatorTurn(t, app, "start on the first one")
	if err := <-steered; err != nil {
		t.Fatal(err)
	}
	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("fixture rounds=%d", len(reqs))
	}
	text := requestText(t, reqs[1])
	if strings.Contains(reqs[1], dataURL) || !strings.Contains(text, "[your operator attached a file: uploads/proof.png]\n[not shown to you: uploads/proof.png, it came while a turn was running") {
		t.Fatalf("the delivered picture is sent, or the identity is not told it is not")
	}
	if got := pictureNotices(t, app, store.ReasonNotShown, words); len(got) != 1 || !strings.Contains(got[0], "it came while a turn was running") {
		t.Fatalf("the record beside the delivered message: %q", got)
	}
}

// refusingModel is a model whose provider refuses any request that
// carries a picture and answers any other; after is the tool call its
// first answer makes, or "" for none.
func refusingModel(t *testing.T, app *App, after string) *contScript {
	t.Helper()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		if strings.Contains(string(body), "image_url") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"this model does not support image input"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if after != "" && n == 1 {
			_, _ = io.WriteString(w, callReply("", after))
			return
		}
		_, _ = io.WriteString(w, textReply("noted"))
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))
	return obs
}

// A REFUSED REQUEST THAT HELD A PICTURE IS SENT ONCE MORE WITHOUT IT, AND
// THE OPERATOR CAN READ THAT IT WAS. The message that carried the picture
// tells the identity; one notice in the turn tells the operator, with the
// provider's own status and words. Nothing is kept past the turn: whoever
// said the model takes images, the next turn sends a picture again, and a
// refusal of a request is not read as a statement about pictures.
func TestAProviderThatRefusesAnImageIsAskedAgainWithoutIt(t *testing.T) {
	for _, c := range []struct {
		name string
		row  *modelCapability
	}{
		{"a catalogue said yes", nil},
		{"the operator's row said yes", &modelCapability{Effort: []string{}, Images: boolp(true)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, _, _ := imageApp(t, c.row)
			obs := refusingModel(t, app, "")
			if c.row == nil {
				catalogueSaysImages(t, app)
			}
			attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
			reqs := obs.snapshot()
			n := len(reqs)
			if n != 2 || !strings.Contains(reqs[0], "image_url") || strings.Contains(reqs[1], "image_url") {
				t.Fatalf("%d requests; want the picture sent once and the turn asked again without it", n)
			}
			if !strings.Contains(reqs[1], "your operator attached a file: uploads/proof.png") ||
				!strings.Contains(reqs[1], "the model's provider refused the request that carried the picture(s) sent here (HTTP 400), so it was sent again without them and you were not shown them") {
				t.Fatal("the second request lost the file's line, or does not say the picture was refused")
			}
			var note string
			if err := app.store.DB().QueryRow(`SELECT content FROM conversations WHERE role='system' AND kind='notice' AND json_extract(details,'$.reason_code')=?`, reasonPicturesRefused).Scan(&note); err != nil {
				t.Fatalf("the refusal is not on the record: %v", err)
			}
			if !strings.Contains(note, "HTTP 400") || !strings.Contains(note, "this model does not support image input") || !strings.Contains(note, "images: false") {
				t.Fatalf("the notice does not carry the provider's status and words and the way on: %q", note)
			}
			// The record says what happened: the picture was not shown,
			// and the identity reads that line on the turns after.
			if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 0 {
				t.Fatalf("a picture the provider refused is on the record as shown: %q", got)
			}
			if got := pictureNotices(t, app, store.ReasonNotShown, "what is this?"); len(got) != 1 || !strings.HasPrefix(got[0], "uploads/proof.png, the model's provider refused the request that carried it") {
				t.Fatalf("the record beside the message: %q", got)
			}
			if p := app.imagePolicy(t.Context()); p.takes != capYes {
				t.Fatalf("a refusal of one request changed what the model is said to take: %v", p.takes)
			}
			attachedTurn(t, app, "and this one?", []string{"uploads/proof.png"})
			if after := obs.snapshot()[n:]; !strings.Contains(after[0], "image_url") {
				t.Fatal("the next turn sent no picture: a refusal was kept past its turn")
			}
		})
	}
}

// A PICTURE THE IDENTITY READS DOES NOT END ITS TURN. The provider refuses
// the request that carries a picture read in the round before; the
// request is sent again without it, the boundary that carried it says so,
// and the turn is answered. A second read in that turn is told the
// provider refused one, and is not sent.
func TestAProviderThatRefusesAPictureAfterAReadDoesNotEndTheTurn(t *testing.T) {
	app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	obs := refusingModel(t, app, toolCall("c1", "read", `{"file_path":"uploads/proof.png"}`))
	operatorTurn(t, app, "look at the proof you made")
	reqs := obs.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("a read, a refused request and its second asking made %d requests", len(reqs))
	}
	if strings.Count(reqs[1], dataURL) != 1 || strings.Contains(reqs[2], "image_url") {
		t.Fatal("the picture was not sent once and then left out")
	}
	if !strings.Contains(reqs[2], "the model's provider refused the request that carried the picture(s) sent here (HTTP 400)") {
		t.Fatal("the request sent again does not say the picture was refused")
	}
	turn := withTurnImages(t.Context(), "", "")
	app.imagesRefused(turn, &llm.HTTPError{StatusCode: 400}, true)
	text, picture := app.readPicture(turn, tools.Result{Output: "x is an image.", Image: &tools.ResultImage{Path: "uploads/proof.png", MediaType: "image/png"}})
	if picture != nil || !strings.Contains(text, "it is not shown to you: this model's provider refused a request that carried pictures earlier in this turn") {
		t.Fatalf("a read after the refusal, in the same turn: %q, picture=%v", text, picture != nil)
	}
}

// A REQUEST CARRIES EVERY PICTURE OF ITS TURN, SO THE TURN IS WHAT IS
// BOUNDED. With room for one picture in a request, three reads in one
// round and a fourth in the next show one picture, in every request; each
// read past the bound is told so and that a later turn can read it.
func TestPicturesReadInOneTurnAreHeldToWhatOneRequestCarries(t *testing.T) {
	app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true), ImagesMaxBytes: int64(len(shownPNG)) + 1})
	obs := &contScript{}
	read := func(id string) string { return toolCall(id, "read", `{"file_path":"uploads/proof.png"}`) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch obs.add(string(body)) {
		case 1:
			_, _ = io.WriteString(w, callReply("", read("c1"), read("c2"), read("c3")))
		case 2:
			_, _ = io.WriteString(w, callReply("", read("c4")))
		default:
			_, _ = io.WriteString(w, textReply("four looks"))
		}
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))
	operatorTurn(t, app, "look at the proof four times")
	reqs := obs.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("two rounds of reads and an answer made %d requests", len(reqs))
	}
	for i, req := range reqs[1:] {
		if got := strings.Count(req, dataURL); got != 1 {
			t.Errorf("request %d carries %d pictures; one request has room for one", i+2, got)
		}
	}
	if got := strings.Count(reqs[2], "with it this turn's pictures pass the"); got != 3 {
		t.Errorf("%d of the three reads past the bound were told so", got)
	}
}

// A PICTURE IS SHOWN UNDER THE POLICY OF THE MODEL THAT READS IT. A
// sub-agent routed to another model is not answered by the identity's
// model's policy: a worker on a model nobody has spoken for is not sent
// the picture under an identity whose model takes images, and is told
// why; the identity's own turn, beside it, is sent it.
func TestASubagentsPictureIsHeldToItsOwnModel(t *testing.T) {
	app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	result := tools.Result{Output: "uploads/proof.png is an image.", Image: &tools.ResultImage{Path: "uploads/proof.png", MediaType: "image/png"}}
	if text, picture := app.readPicture(withTurnImages(t.Context(), "", ""), result); picture == nil || !strings.Contains(text, pictureSent) {
		t.Fatalf("the identity's own turn: %q, picture=%v", text, picture != nil)
	}
	routed := withTurnImages(t.Context(), "local", "text-worker")
	if text, picture := app.readPicture(routed, result); picture != nil || !strings.Contains(text, "it is not shown to you: nobody has said this model takes images") {
		t.Fatalf("a worker on a model nobody has spoken for: %q, picture=%v", text, picture != nil)
	}
	setModelRow(t, app, "text-worker", modelCapability{Effort: []string{}, Images: boolp(false)})
	if text, picture := app.readPicture(routed, result); picture != nil || !strings.Contains(text, "it is not shown to you: this model does not take images") {
		t.Fatalf("a worker whose row says no: %q, picture=%v", text, picture != nil)
	}
	// And the reverse: the identity's model takes none, the worker's does.
	setModelRow(t, app, "fake", modelCapability{Effort: []string{}, Images: boolp(false)})
	setModelRow(t, app, "text-worker", modelCapability{Effort: []string{}, Images: boolp(true)})
	if _, picture := app.readPicture(routed, result); picture == nil {
		t.Fatal("a worker whose model takes images was answered by the identity's model, which does not")
	}
}

// THE PICTURES THE PAGE DRAWS AND THE PICTURES READ ANSWERS ARE ONE LIST,
// kept in two packages that do not import each other: every type a file
// is found to be is one the page draws under some name, and the page
// draws no type a file is never found to be.
func TestTheImageTypesAreOneList(t *testing.T) {
	drawn := map[string]bool{}
	for _, mediaType := range dashboard.ImageTypes {
		drawn[mediaType] = true
	}
	for _, mediaType := range tools.PictureMediaTypes {
		if !drawn[mediaType] {
			t.Errorf("read answers %s as a picture and the page draws no file as it", mediaType)
		}
		delete(drawn, mediaType)
	}
	for mediaType := range drawn {
		t.Errorf("the page draws %s and read never finds a file to be it", mediaType)
	}
}

// AN IDENTITY CAN LOOK AT AN IMAGE IT MADE. Its read of an image file is
// answered in words, and a model that takes images is shown the picture
// at the round's boundary, in one user turn after the results. A model
// that does not is told so in the answer.
func TestAnIdentityCanLookAtAnImageItMade(t *testing.T) {
	for _, c := range []struct {
		name  string
		row   *modelCapability
		shown bool
		says  string
	}{
		{"a model that takes images", &modelCapability{Effort: []string{}, Images: boolp(true)}, true, "the picture is sent to you once, after the results of the round that read it; to see it in a later turn, read the file again"},
		{"a model that does not", &modelCapability{Effort: []string{}, Images: boolp(false)}, false, "it is not shown to you: this model does not take images"},
		{"nobody has said", nil, false, "it is not shown to you: nobody has said this model takes images"},
		{"over the model's bound", &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxBytes: 8}, false, "bytes are past this model's bound of 8 for one picture"},
		{"past the model's longer side", &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxSide: 3}, false, "it is not shown to you: 4 by 3 pixels is past this model's bound of 3 a side"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, _, dataURL := imageApp(t, c.row)
			obs := &contScript{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				if obs.add(string(body)) == 1 {
					_, _ = io.WriteString(w, callReply("", toolCall("c1", "read", `{"file_path":"uploads/proof.png"}`)))
					return
				}
				_, _ = io.WriteString(w, textReply("it is a page proof"))
			}))
			t.Cleanup(srv.Close)
			app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))
			operatorTurn(t, app, "look at the proof you made")
			reqs := obs.snapshot()
			if len(reqs) != 2 {
				t.Fatalf("a read and an answer made %d requests", len(reqs))
			}
			second := reqs[1]
			if !strings.Contains(second, "is an image: image/png") || !strings.Contains(second, c.says) {
				t.Fatalf("the read's answer does not say what it is and whether it is shown (%q)", c.says)
			}
			if got := strings.Count(second, dataURL) == 1; got != c.shown {
				t.Fatalf("the picture in the next request: %v, want %v", got, c.shown)
			}
			if c.shown && !strings.Contains(second, "sent here: the 1 picture(s) this round's results describe") {
				t.Fatal("the picture came with no line saying what it is")
			}
			var content string
			if err := app.store.DB().QueryRow(`SELECT content FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')='read'`).Scan(&content); err != nil {
				t.Fatalf("the read's record: %v", err)
			}
			if strings.Contains(content, "base64") || !strings.Contains(content, "is an image: image/png") {
				t.Fatalf("the record holds %q", content)
			}
		})
	}
}

// A DIALECT THAT WRITES NO PICTURE IS SENT NONE. The native Gemini dialect
// has no image part: a picture handed to it would be dropped without a
// word while the record said "shown". So the dialect is asked before the
// model's row: with a row that says the model takes images, an entry on
// that dialect is still sent no picture, a read is told why, and nothing
// is recorded as shown.
func TestADialectThatWritesNoPictureIsSentNone(t *testing.T) {
	app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	reg, err := loadProvidersFile(app.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	reg.Providers = append(reg.Providers, providerEntry{Name: "google-native", APIType: "gemini", URL: "https://example.invalid"})
	if reg.ModelCapabilities == nil {
		reg.ModelCapabilities = map[string]modelCapability{}
	}
	reg.ModelCapabilities["gem-vision"] = modelCapability{Effort: []string{}, Images: boolp(true)}
	if _, err := saveProvidersFile(app.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	if p := app.imagePolicyFor("google-native", "gem-vision"); p.takes != capNo || !p.unwritten {
		t.Fatalf("a model on a dialect that writes no picture: %+v, want not sent, by the dialect", p)
	}
	turn := withTurnImages(t.Context(), "google-native", "gem-vision")
	result := tools.Result{Output: "uploads/proof.png is an image.", Image: &tools.ResultImage{Path: "uploads/proof.png", MediaType: "image/png"}}
	if text, picture := app.readPicture(turn, result); picture != nil || !strings.Contains(text, "it is not shown to you: this host does not send pictures in this provider's dialect") {
		t.Fatalf("a read on that dialect: %q, picture=%v", text, picture != nil)
	}
	ref, err := app.recordConversationRef(t.Context(), "operator", "look at this on the other model")
	if err != nil {
		t.Fatal(err)
	}
	row := store.ConversationTurn{ID: ref.ID, Attached: []string{"uploads/proof.png"}}
	if images, err := app.picturesWith(turn, &row); err != nil || len(images) != 0 || len(row.NotShown) != 0 {
		t.Fatalf("an attached picture on that dialect: %d sent, noted %q, %v", len(images), row.NotShown, err)
	}
	if state := turnImagesOf(turn); len(state.sent) != 0 {
		t.Fatalf("a picture is waiting to be recorded as shown: %+v", state.sent)
	}
	for dialect, want := range map[llm.Dialect]bool{llm.DialectOpenAI: true, llm.DialectAnthropic: true, llm.DialectResponses: true, llm.DialectGemini: false} {
		if got := llm.WritesImages(dialect); got != want {
			t.Errorf("WritesImages(%s) = %v, want %v", dialect, got, want)
		}
	}
}
