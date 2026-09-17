package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/speech"
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
const (
	// .
	// .
	// .
	sayBoundChars = 8000
	// .
	sayBoundPCM = 24 << 20
	// .
	// .
	// .
	sayFirstChars = 90
	sayPieceChars = 600
	// .
	// .
	// .
	sayKept  = 6
	sayFresh = 2 * time.Minute
)

// .
// .
// .
const sampleWords = "This is the voice I will answer in."

// .
// .
type spokenReply struct {
	id   string
	key  string
	say  dashboard.SpeakText
	tc   TTSConfig
	born time.Time

	mu       sync.Mutex
	cond     *sync.Cond
	started  bool
	done     bool
	err      error
	rate     int
	channels int
	pieces   [][]byte
	total    int
	// .
	// .
	ctx    context.Context
	cancel context.CancelFunc
	// .
	// .
	reserved int
}

// .
// .
var errHushed = errors.New("hushed")

// .
// .
func (a *App) hushSpoken(id string) {
	a.spokenMu.Lock()
	r := a.spoken[id]
	a.spokenMu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancel
	if !r.done {
		r.done, r.err = true, errHushed
		r.cond.Broadcast()
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *spokenReply) add(au speech.Audio) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rate == 0 {
		r.rate, r.channels = au.Rate, au.Channels
		if r.channels <= 0 {
			r.channels = 1
		}
	}
	if au.Rate != r.rate || (au.Channels > 0 && au.Channels != r.channels) {
		return fmt.Errorf("%s changed format inside one reply: %d Hz %d channels, then %d Hz %d channels",
			r.say.Provider, r.rate, r.channels, au.Rate, au.Channels)
	}
	if r.total+len(au.PCM) > sayBoundPCM {
		return fmt.Errorf("%s sent more than %d bytes of audio for one reply", r.say.Provider, sayBoundPCM)
	}
	r.pieces = append(r.pieces, au.PCM)
	r.total += len(au.PCM)
	r.cond.Broadcast()
	return nil
}

func (r *spokenReply) finish(err error) {
	r.mu.Lock()
	if !r.done {
		// .
		r.done, r.err = true, err
	}
	// .
	// .
	r.say.APIKey = ""
	r.cond.Broadcast()
	r.mu.Unlock()
}

// .
// .
func (r *spokenReply) settle(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n < 0 || n > r.reserved {
		n = r.reserved
	}
	r.reserved -= n
	return n
}

// .
// .
func (a *App) replyVoice() string {
	return strings.TrimSpace(a.configSnapshot().Speech.TTS.Provider)
}

// .
// .
// .
// .
// .
// .
// .
func (a *App) speakMint(say dashboard.SpeakText) (string, error) {
	r, err := a.mint(say)
	if err != nil {
		return "", err
	}
	return r.id, nil
}

// .
// .
// .
// .
func (a *App) mint(say dashboard.SpeakText) (*spokenReply, error) {
	text := strings.TrimSpace(say.Text)
	if say.Sample {
		text = sampleWords
	}
	if text == "" {
		return nil, errors.New("there is nothing to speak")
	}
	// .
	// .
	if n := utf8.RuneCountInString(text); n > sayBoundChars {
		return nil, fmt.Errorf("a spoken reply is bounded at %d characters and this one is %d", sayBoundChars, n)
	}
	// .
	// .
	// .
	// .
	tc := a.configSnapshot().Speech.TTS
	candidate := strings.TrimSpace(say.Provider) != ""
	if candidate {
		tc = TTSConfig{Provider: strings.TrimSpace(say.Provider), Model: strings.TrimSpace(say.Model),
			Voice: strings.TrimSpace(say.Voice), TimeoutSeconds: tc.TimeoutSeconds}
	}
	if strings.TrimSpace(tc.Provider) == "" {
		return nil, errors.New("no speaking service is configured")
	}
	reg, err := a.loadProviders()
	if err != nil {
		return nil, err
	}
	entry := speechEntryNamed(reg, tc.Provider)
	if entry == nil {
		return nil, fmt.Errorf("%s is not a speech service this identity knows", tc.Provider)
	}
	typed := ""
	if candidate {
		typed = strings.TrimSpace(say.APIKey)
	}
	if providerAPIKey(*entry, typed, tc.APIKeyEnv) == "" && shippedSpeechVendor(tc.Provider) {
		return nil, fmt.Errorf("no API key is stored for %s — enter one in Settings → Speech", tc.Provider)
	}
	if _, err := synthesizerForEntry(tc, entry, typed); err != nil {
		return nil, err
	}
	say.Text, say.APIKey = text, typed
	key := spokenKey(text, tc)

	// .
	// .
	// .
	// .
	// .
	a.spokenMu.Lock()
	a.sweepSpoken()
	if !candidate {
		for _, r := range a.spoken {
			r.mu.Lock()
			reusable := r.key == key && r.err == nil && r.say.Provider == ""
			r.mu.Unlock()
			if reusable {
				a.spokenMu.Unlock()
				return r, nil
			}
		}
	}
	a.spokenMu.Unlock()
	// .
	characters := utf8.RuneCountInString(text)
	if err := a.reserveSpeaking(characters); err != nil {
		return nil, err
	}
	id, err := speakID()
	if err != nil {
		a.settleSpeaking(characters)
		return nil, err
	}
	r := &spokenReply{id: id, key: key, say: say, tc: tc, born: time.Now(), reserved: characters}
	r.cond = sync.NewCond(&r.mu)
	a.spokenMu.Lock()
	if a.spoken == nil {
		a.spoken = map[string]*spokenReply{}
	}
	a.spoken[id] = r
	a.spokenMu.Unlock()
	return r, nil
}

// .
// .
// .
func (a *App) speakAhead(text string) string {
	if a.replyVoice() == "" {
		return ""
	}
	r, err := a.mint(dashboard.SpeakText{Text: text})
	if err != nil {
		// .
		// .
		log.Printf("VOICE: this reply will not be spoken aloud: %v", err)
		return ""
	}
	a.speakStart(r)
	return r.id
}

// .
func (a *App) speakStart(r *spokenReply) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	base := a.bgCtx
	if base == nil {
		base = context.Background()
	}
	r.ctx, r.cancel = context.WithCancel(base)
	r.mu.Unlock()
	// .
	// .
	// .
	// .
	go func() {
		r.finish(a.speakInto(r))
		// .
		// .
		a.settleSpeaking(r.settle(-1))
	}()
}

// .
// .
func (a *App) speakInto(r *spokenReply) error {
	r.mu.Lock()
	ctx := r.ctx
	r.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	reg, err := a.loadProviders()
	if err != nil {
		return err
	}
	entry := speechEntryNamed(reg, r.tc.Provider)
	if entry == nil {
		return fmt.Errorf("%s is not a speech service this identity knows", r.tc.Provider)
	}
	syn, err := synthesizerForEntry(r.tc, entry, r.say.APIKey)
	if err != nil {
		return err
	}
	keyless := providerAPIKey(*entry, r.say.APIKey, r.tc.APIKeyEnv) == ""
	for _, piece := range speech.Segments(r.say.Text, sayFirstChars, sayPieceChars) {
		if err := syn.Synthesize(ctx, piece, r.add); err != nil {
			// .
			// .
			// .
			return errors.New(checkRefusal(r.tc.Provider, "did not speak", keyless, err))
		}
		// .
		// .
		// .
		said := utf8.RuneCountInString(piece)
		a.meterSpeech(r.tc.Provider, "tts", said, 0)
		a.settleSpeaking(r.settle(said))
	}
	r.mu.Lock()
	empty := r.total == 0 || r.rate <= 0
	r.mu.Unlock()
	if empty {
		return fmt.Errorf("%s answered with no audio", r.tc.Provider)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
func (a *App) speakPlay(ctx context.Context, id string, w io.Writer) error {
	a.spokenMu.Lock()
	r := a.spoken[id]
	a.spokenMu.Unlock()
	if r == nil {
		return errors.New("that reply is no longer here to be spoken")
	}
	a.speakStart(r)

	// .
	// .
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-stop:
		}
	}()

	sent, wrote := 0, false
	flush := func() {
		if f, ok := w.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
	for {
		r.mu.Lock()
		for sent == len(r.pieces) && !r.done && ctx.Err() == nil {
			r.cond.Wait()
		}
		pieces := append([][]byte(nil), r.pieces[sent:]...)
		rate, channels, done, err := r.rate, r.channels, r.done, r.err
		r.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(pieces) > 0 && !wrote {
			if _, werr := w.Write(wavStreamHeader(rate, channels)); werr != nil {
				return werr
			}
			wrote = true
		}
		for _, p := range pieces {
			if _, werr := w.Write(p); werr != nil {
				return werr
			}
			sent++
		}
		if len(pieces) > 0 {
			flush()
		}
		if done {
			if err != nil && !wrote {
				return err
			}
			if err != nil {
				log.Printf("VOICE: a reply stopped speaking part way through: %v", err)
			}
			return nil
		}
	}
}

// .
// .
func (a *App) spokenAudio(ctx context.Context, id string) ([]byte, error) {
	var buf bytes.Buffer
	if err := a.speakPlay(ctx, id, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// .
func (a *App) sweepSpoken() {
	drop := func(id string, r *spokenReply) {
		delete(a.spoken, id)
		// .
		a.settleSpeaking(r.settle(-1))
	}
	for id, r := range a.spoken {
		if time.Since(r.born) > sayFresh {
			drop(id, r)
		}
	}
	for len(a.spoken) > sayKept {
		oldest, at := "", time.Now()
		var stale *spokenReply
		for id, r := range a.spoken {
			if r.born.Before(at) {
				oldest, at, stale = id, r.born, r
			}
		}
		if oldest == "" {
			return
		}
		drop(oldest, stale)
	}
}

// .
// .
func speakID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// .
func spokenKey(text string, tc TTSConfig) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tc.Provider, tc.Model, tc.Voice, text}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// .
// .
// .
// .
func wavStreamHeader(rate, channels int) []byte {
	if channels <= 0 {
		channels = 1
	}
	const endless = 0xFFFFFFFF
	out := make([]byte, 44)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], endless)
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(out[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(out[28:32], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(out[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], endless)
	return out
}
