package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
)

type imagePolicy struct {
	takes capState

	unwritten bool
	tokens    int
	maxBytes  int64
	maxTotal  int64
	maxSide   int
	maxCount  int
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
	p := imagePolicy{tokens: defaultImageTokens, maxBytes: defaultImageMaxBytes, maxTotal: defaultImagesMaxBytes,
		maxSide: defaultImageMaxSide, maxCount: defaultImagesMaxCount}
	if model == "" || reg == nil {
		return p
	}
	var entry *providerEntry
	for i := range reg.Providers {
		if reg.Providers[i].Name == provider {
			entry = &reg.Providers[i]
			break
		}
	}
	if entry == nil {
		return p
	}

	if !llm.WritesImages(entryDialect(*entry)) {
		p.takes, p.unwritten = capNo, true
		return p
	}
	row, _ := reg.effective().capabilityFor(model)
	if row.ImageTokens > 0 {
		p.tokens = row.ImageTokens
	}
	if row.ImageMaxBytes > 0 {
		p.maxBytes = row.ImageMaxBytes
	}
	if row.ImagesMaxBytes > 0 {
		p.maxTotal = row.ImagesMaxBytes
	}
	if row.ImageMaxSide > 0 {
		p.maxSide = row.ImageMaxSide
	}
	if row.ImagesMaxCount > 0 {
		p.maxCount = row.ImagesMaxCount
	}
	images := reg.ModelCapabilities[model].Images
	if images == nil {
		images = a.discoveredImageSupport(*entry, model)
	}
	if images == nil {
		mods := a.modalities.last(modelModalitiesURL(*entry, model, reg.modelCatalogueURL()))
		if mods.in != nil {
			value := slices.Contains(mods.in, "image")
			images = &value
		}
	}
	if images == nil {
		images = row.Images
	}
	if images != nil {
		p.takes = capNo
		if *images {
			p.takes = capYes
		}
	}
	return p
}

type turnImages struct {
	provider, model string

	mu      sync.Mutex
	bytes   int64
	count   int
	refused bool

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
		return "this model's provider refused a request that carried pictures earlier in this turn"
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
	if policy.takes != capYes {
		return nil, nil
	}
	decided := len(t.Shown)+len(t.NotShown) > 0
	var images []llm.Image
	for _, path := range t.Attached {
		if decided && !slices.Contains(t.Shown, path) {
			continue
		}
		picture, why, isPicture := a.pictureAt(ctx, path, policy)
		switch {
		case !isPicture:
		case picture == nil && decided:
			logsink.Info("turn.decision", "the image %s, sent once in this turn, is not sent again: %s", path, why)
		case picture == nil:
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
			images = append(images, *picture)
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
	sent, unanswered := t.sent, t.unanswered
	t.sent = nil
	t.mu.Unlock()
	answered := turnErr == nil || result.ToolCallsUsed > 0 || result.Spoken != ""
	for _, s := range sent {
		reason, content := store.ReasonShown, s.path
		switch {
		case unanswered:
			reason, content = store.ReasonNotShown, s.path+", the model's provider refused the request that carried it; read the file to see it"
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
	if len(paths) == 0 || a.toolReg == nil || a.imagePolicy(ctx).takes != capYes {
		return ""
	}
	var notes []string
	for _, path := range paths {
		if !a.holdsPicture(path) {
			continue
		}
		note := path + ", it came while a turn was running, which is told of a file and not shown it; read the file to see it"
		if err := a.notePicture(ctx, messageID, store.ReasonNotShown, note); err != nil {
			logsink.Warn("steering.error", "%v", err)
		}
		notes = append(notes, note)
	}
	return notShownLines(notes)
}

func (a *App) holdsPicture(path string) bool {
	root, rel, err := a.toolReg.Admit("show", path)
	if err != nil {
		return false
	}
	rz, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	defer rz.Close()
	if st, err := rz.Stat(rel); err != nil || !st.Mode().IsRegular() {
		return false
	}
	f, err := rz.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	_, _, _, ok := tools.ImageConfig(f)
	return ok
}

func (a *App) pictureAt(ctx context.Context, path string, policy imagePolicy) (picture *llm.Image, why string, isPicture bool) {
	root, rel, err := a.toolReg.Admit("show", path)
	if err != nil {
		return nil, "", false
	}
	rz, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", false
	}
	defer rz.Close()
	if st, err := rz.Stat(rel); err != nil || !st.Mode().IsRegular() {
		return nil, "", false
	}
	f, err := rz.Open(rel)
	if err != nil {
		return nil, "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, "", false
	}
	mediaType, width, height, ok := tools.ImageConfig(f)
	if !ok {
		return nil, "", false
	}
	switch {
	case st.Size() > policy.maxBytes:
		return nil, fmt.Sprintf("its %d bytes are past this model's bound of %d for one picture", st.Size(), policy.maxBytes), true
	case max(width, height) > policy.maxSide:
		return nil, fmt.Sprintf("%d by %d pixels is past this model's bound of %d a side", width, height, policy.maxSide), true
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, "it could not be read: " + err.Error(), true
	}
	data, err := io.ReadAll(io.LimitReader(f, policy.maxBytes+1))
	if err != nil || int64(len(data)) > policy.maxBytes {
		return nil, "it changed while it was read", true
	}
	if why := turnImagesOf(ctx).admit(int64(len(data)), policy); why != "" {
		return nil, why, true
	}
	return &llm.Image{MediaType: mediaType, Data: data, Tokens: policy.tokens}, "", true
}

func (a *App) imagesRefused(ctx context.Context, refusal error, answered bool) {
	if t := turnImagesOf(ctx); t != nil {
		t.mu.Lock()
		t.refused = true
		t.unanswered = t.unanswered || !answered
		t.mu.Unlock()
	}
	status, said := 0, ""
	var refused *llm.HTTPError
	if errors.As(refusal, &refused) {
		status, said = refused.StatusCode, strings.Join(strings.Fields(refused.Body), " ")
		if len(said) > picturesRefusedExcerpt {
			said = strings.ToValidUTF8(said[:picturesRefusedExcerpt], "") + "…"
		}
	}
	note := fmt.Sprintf("the model's provider refused the request that carried this turn's picture(s) (HTTP %d); it was sent again without them, and the model was not shown them. The provider said: %s. If this model does not take images, write images: false in its row of the providers registry",
		status, said)
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

func (a *App) readPicture(ctx context.Context, result tools.Result) (string, *llm.Image) {
	text := result.Text()
	if result.Image == nil || result.Failed() {
		return text, nil
	}
	policy := a.imagePolicy(ctx)
	switch {
	case policy.unwritten:
		return text + "\n[it is not shown to you: this host does not send pictures in this provider's dialect]", nil
	case policy.takes == capNo:
		return text + "\n[it is not shown to you: this model does not take images]", nil
	case policy.takes == capUnknown:
		return text + "\n[it is not shown to you: nobody has said this model takes images]", nil
	}
	picture, why, isPicture := a.pictureAt(ctx, result.Image.Path, policy)
	switch {
	case !isPicture:
		return text + "\n[it is not shown to you: the file is gone or holds no picture now]", nil
	case picture == nil:
		return text + "\n[it is not shown to you: " + why + "]", nil
	}
	return text + "\n" + pictureSent, picture
}
