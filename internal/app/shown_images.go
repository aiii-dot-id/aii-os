package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const (
	defaultImageTokens = 4784

	defaultImageMaxBytes = 5 << 20

	defaultImagesMaxBytes = 20 << 20

	defaultImageMaxSide = 8000

	defaultImagesMaxCount = 20

	defaultFileTokens = 8000
)

type imagePolicy struct {
	takes capState
	kinds map[string]capState

	dialect    llm.Dialect
	tokens     int
	fileTokens int
	maxBytes   int64
	maxTotal   int64
	maxSide    int
	maxCount   int
}

func (a *App) imagePolicy(ctx context.Context) imagePolicy {
	if t := turnImagesOf(ctx); t != nil && t.model != "" {
		return a.imagePolicyFor(t.provider, t.model)
	}
	return a.runningImagePolicy()
}

func (a *App) runningImagePolicy() imagePolicy {
	entry, client := a.activeRuntime()
	if client == nil {
		return a.imagePolicyFor("", "")
	}
	return a.imagePolicyFor(entry.Name, client.ModelName())
}

func (a *App) imagePolicyFor(provider, model string) imagePolicy {
	var reg *providerRegistry
	if model != "" {

		reg, _ = a.loadProviders()
	}
	return a.imagePolicyForRegistry(reg, provider, model)
}

func (a *App) imagePolicyForRegistry(reg *providerRegistry, provider, model string) imagePolicy {
	p := imagePolicy{tokens: defaultImageTokens, fileTokens: defaultFileTokens, maxBytes: defaultImageMaxBytes, maxTotal: defaultImagesMaxBytes,
		maxSide: defaultImageMaxSide, maxCount: defaultImagesMaxCount}
	if model == "" || reg == nil {
		return p
	}
	entry := entryNamed(reg, provider)
	if entry == nil {
		return p
	}
	row, _ := reg.effective().capabilityFor(model)
	p.tokens = cmp.Or(row.ImageTokens, p.tokens)
	p.fileTokens = cmp.Or(row.FileTokens, p.fileTokens)
	p.maxBytes = cmp.Or(row.ImageMaxBytes, p.maxBytes)
	p.maxTotal = cmp.Or(row.ImagesMaxBytes, p.maxTotal)
	p.maxSide = cmp.Or(row.ImageMaxSide, p.maxSide)
	p.maxCount = cmp.Or(row.ImagesMaxCount, p.maxCount)
	caps := a.modelCapabilitiesFor(reg, *entry, model)
	p.kinds = make(map[string]capState, len(inputKinds))
	for _, kind := range inputKinds {
		p.kinds[kind] = caps.input[kind].state
	}
	p.takes, p.dialect = p.kinds["image"], entryDialect(*entry)
	return p
}

var kindNouns = map[string]string{"image": "images", "pdf": "PDFs", "audio": "audio", "video": "video"}

func (p imagePolicy) withheld(kind, mediaType string) string {
	switch {
	case p.dialect != "" && mediaType != "" && !llm.Writes(p.dialect, mediaType):
		return "this host does not send " + mediaType + " in this provider's dialect"
	case p.kinds[kind] == capNo:
		return "this model does not take " + kindNouns[kind]
	case p.kinds[kind] != capYes:
		return "nobody has said this model takes " + kindNouns[kind]
	}
	return ""
}

type turnImages struct {
	provider, model string

	mu      sync.Mutex
	bytes   int64
	count   int
	refused bool

	held string

	unanswered bool

	sent []sentPicture
}

type sentPicture struct{ message, path string }

type turnImagesKey struct{}

func withTurnImages(ctx context.Context, provider, model string) context.Context {
	return context.WithValue(ctx, turnImagesKey{}, &turnImages{provider: provider, model: model})
}

func turnImagesOf(ctx context.Context) *turnImages {
	t, _ := ctx.Value(turnImagesKey{}).(*turnImages)
	return t
}

func (t *turnImages) admit(size int64, p imagePolicy) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.refused:
		return "a request that carried pictures or files earlier in this turn " + t.held
	case t.count >= p.maxCount:
		return fmt.Sprintf("this turn already carries %d pictures, the most one request takes; a later turn can read it", p.maxCount)
	case t.bytes+size > p.maxTotal:
		return fmt.Sprintf("with it this turn's pictures pass the %d bytes one request carries; a later turn can read it", p.maxTotal)
	}
	t.bytes += size
	t.count++
	return ""
}

func currentMessages(turns []store.ConversationTurn, turn string) map[string]bool {
	rows := map[string]bool{}
	if turn == "" {
		return rows
	}
	for _, t := range turns {
		if t.TurnID == turn && t.Role != string(interaction.System) && t.Role != string(interaction.Resident) {
			rows[t.ID] = true
		}
	}
	return rows
}

func (a *App) picturesWith(ctx context.Context, t *store.ConversationTurn) ([]llm.Image, error) {
	if len(t.Attached) == 0 || a.toolReg == nil {
		return nil, nil
	}
	policy := a.imagePolicy(ctx)
	decided := len(t.Shown)+len(t.NotShown) > 0
	var images []llm.Image
	for _, path := range t.Attached {
		if decided && !slices.Contains(t.Shown, path) {
			continue
		}
		file, _, why := a.fileAt(ctx, path, policy)
		switch {
		case file == nil && why == "":

		case file == nil && decided:
			logsink.Info("turn.decision", "the file %s, sent once in this turn, is not sent again: %s", path, why)
		case file == nil:
			if err := a.notePicture(ctx, t.ID, store.ReasonNotShown, path+", "+why); err != nil {
				return nil, err
			}
			t.NotShown = append(t.NotShown, path+", "+why)
		default:
			if state := turnImagesOf(ctx); state != nil && !decided {
				state.mu.Lock()
				state.sent = append(state.sent, sentPicture{message: t.ID, path: path})
				state.mu.Unlock()
			}
			images = append(images, *file)
		}
	}
	return images, nil
}

func (a *App) settlePictures(ctx context.Context, result conversation.Result, turnErr error) {
	t := turnImagesOf(ctx)
	if t == nil {
		return
	}
	t.mu.Lock()
	sent, unanswered, held := t.sent, t.unanswered, t.held
	t.sent = nil
	t.mu.Unlock()
	answered := turnErr == nil || result.ToolCallsUsed > 0 || result.Spoken != ""
	for _, s := range sent {
		reason, content := store.ReasonShown, s.path
		switch {
		case unanswered:
			reason, content = store.ReasonNotShown, s.path+", the request that carried it "+held+"; read the file to see it"
		case !answered:
			reason, content = store.ReasonNotShown, s.path+", the request that carried it was not answered; read the file to see it"
		}
		if err := a.notePicture(context.WithoutCancel(ctx), s.message, reason, content); err != nil {
			logsink.Error("turn.error", "%v", err)
			if a.dashboard != nil {
				a.dashboard.BroadcastResponse("system", "What became of a picture you attached is not on the record: "+err.Error())
			}
		}
	}
}

func (a *App) notePicture(ctx context.Context, messageID, reason, content string) error {
	if a.engine == nil {
		return nil
	}
	if _, err := a.engine.RecordRelatedConversationRef(ctx, string(interaction.System), content, messageID,
		interaction.Details{ReasonCode: reason}); err != nil {
		return fmt.Errorf("the note of what became of an attached picture (%s) could not be recorded: %w", content, err)
	}
	return nil
}

func notShownLines(notes []string) string {
	lines := ""
	for _, note := range notes {
		lines += "\n[not shown to you: " + note + "]"
	}
	return lines
}

func (a *App) steeredPictures(ctx context.Context, messageID string, paths []string) string {
	if len(paths) == 0 || a.toolReg == nil {
		return ""
	}
	policy := a.imagePolicy(ctx)
	var notes []string
	for _, path := range paths {
		kind, mediaType := a.carriedKind(path)
		if kind == "" {
			continue
		}

		note := path + ", it came while a turn was running, which is told of a file and not shown it; read the file to see it"
		if why := policy.withheld(kind, mediaType); why != "" {
			note = path + ", " + why
		}
		if err := a.notePicture(ctx, messageID, store.ReasonNotShown, note); err != nil {
			logsink.Warn("steering.error", "%v", err)
		}
		notes = append(notes, note)
	}
	return notShownLines(notes)
}

func (a *App) openShown(path string) (*os.File, string) {
	root, rel, err := a.toolReg.Admit("show", path)
	if err != nil {
		return nil, ""
	}
	rz, err := os.OpenRoot(root)
	if err != nil {
		return nil, ""
	}
	defer rz.Close()
	if st, err := rz.Stat(rel); err != nil || !st.Mode().IsRegular() {
		return nil, ""
	}
	f, err := rz.Open(rel)
	if err != nil {
		return nil, ""
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, ""
	}
	return f, filepath.Base(rel)
}

func kindOf(f *os.File) (kind, mediaType string, width, height int) {
	if mediaType, width, height, ok := tools.ImageConfig(f); ok {
		return "image", mediaType, width, height
	}
	mediaType, kind = tools.MediaOf(f)
	return kind, mediaType, 0, 0
}

func kindOfType(mediaType string) string {
	if strings.HasPrefix(mediaType, "image/") {
		return "image"
	}
	for kind, types := range kindTypes {
		if slices.Contains(types, mediaType) {
			return kind
		}
	}
	return ""
}

func (a *App) carriedKind(path string) (kind, mediaType string) {
	f, _ := a.openShown(path)
	if f == nil {
		return "", ""
	}
	defer f.Close()
	kind, mediaType, _, _ = kindOf(f)
	return kind, mediaType
}

func (a *App) fileAt(ctx context.Context, path string, policy imagePolicy) (file *llm.Image, kind, why string) {
	f, name := a.openShown(path)
	if f == nil {
		return nil, "", ""
	}
	defer f.Close()
	kind, mediaType, width, height := kindOf(f)
	if kind == "" {
		return nil, "", ""
	}
	if why := policy.withheld(kind, mediaType); why != "" {
		return nil, kind, why
	}
	st, err := f.Stat()
	if err != nil {
		return nil, kind, "it could not be read: " + err.Error()
	}
	limit, tokens := policy.maxTotal, policy.fileTokens
	switch {
	case kind == "image" && st.Size() > policy.maxBytes:
		return nil, kind, fmt.Sprintf("its %d bytes are past this model's bound of %d for one picture", st.Size(), policy.maxBytes)
	case kind == "image" && max(width, height) > policy.maxSide:
		return nil, kind, fmt.Sprintf("%d by %d pixels is past this model's bound of %d a side", width, height, policy.maxSide)
	case kind == "image":
		limit, tokens = policy.maxBytes, policy.tokens
	case st.Size() > policy.maxTotal:
		return nil, kind, fmt.Sprintf("its %d bytes are past the %d one request carries to this model", st.Size(), policy.maxTotal)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, kind, "it could not be read: " + err.Error()
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, kind, "it changed while it was read"
	}
	if why := turnImagesOf(ctx).admit(int64(len(data)), policy); why != "" {
		return nil, kind, why
	}
	return &llm.Image{MediaType: mediaType, Name: name, Data: data, Tokens: tokens}, kind, ""
}

func (a *App) imagesRefused(ctx context.Context, refusal error, answered bool) {
	var refused *llm.HTTPError
	var tooBig *llm.ContextLimitError
	tooLarge := errors.As(refusal, &tooBig)
	if t := turnImagesOf(ctx); t != nil {
		t.mu.Lock()
		t.refused = true
		t.unanswered = t.unanswered || !answered
		t.held = "was refused by the model's provider"
		if tooLarge {
			t.held = "did not fit the model's window"
		}
		t.mu.Unlock()
	}
	status, said := 0, ""
	if tooLarge {
		note := fmt.Sprintf("this turn's picture(s) or file(s) did not fit the model's window with the rest of the request (about %d tokens, where it takes %d); it was sent without them, and the model was not shown them. A row's image_tokens and file_tokens in the providers registry say what one is priced at",
			tooBig.Required, tooBig.Limit)
		a.recordImagesRefused(ctx, note)
		return
	}
	if errors.As(refusal, &refused) {
		status, said = refused.StatusCode, strings.Join(strings.Fields(refused.Body), " ")
		if len(said) > picturesRefusedExcerpt {
			said = strings.ToValidUTF8(said[:picturesRefusedExcerpt], "") + "…"
		}
	}
	note := fmt.Sprintf("the model's provider refused the request that carried this turn's picture(s) or file(s) (HTTP %d); it was sent again without them, and the model was not shown them. The provider said: %s. If this model does not take one of their kinds, write so in its row of the providers registry, as \"input\": {\"image\": false} for pictures",
		status, said)
	a.recordImagesRefused(ctx, note)
}

func (a *App) recordImagesRefused(ctx context.Context, note string) {
	logsink.Warn("llm.refusal", "%s", note)
	if a.engine == nil {
		return
	}
	if _, err := a.engine.RecordRelatedConversationRef(context.WithoutCancel(ctx), string(interaction.System), note, "",
		interaction.Details{ReasonCode: reasonPicturesRefused}); err != nil {
		logsink.Error("turn.error", "the note that a provider refused this turn's pictures was not recorded, so the operator's page does not show it: %v", err)
	}
}

const (
	reasonPicturesRefused  = "pictures_refused"
	picturesRefusedExcerpt = 300
)

const pictureSent = "[the picture is sent to you once, after the results of the round that read it; to see it in a later turn, read the file again]"

const fileSent = "[the file is sent to you once, after the results of the round that read it; to see it in a later turn, read the file again]"

func (a *App) readPicture(ctx context.Context, result tools.Result) (string, *llm.Image) {
	text := result.Text()
	if result.Image == nil || result.Failed() {
		return text, nil
	}
	policy := a.imagePolicy(ctx)

	read := cmp.Or(kindOfType(result.Image.MediaType), "image")
	if why := policy.withheld(read, result.Image.MediaType); why != "" {
		return text + "\n[it is not shown to you: " + why + "]", nil
	}
	file, kind, why := a.fileAt(ctx, result.Image.Path, policy)
	switch {
	case kind != read && read == "image":
		return text + "\n[it is not shown to you: the file is gone or holds no picture now]", nil
	case kind != read:
		return text + "\n[it is not shown to you: the file is gone or no longer holds what it held]", nil
	case file == nil:
		return text + "\n[it is not shown to you: " + why + "]", nil
	case read != "image":
		return text + "\n" + fileSent, file
	}
	return text + "\n" + pictureSent, file
}
