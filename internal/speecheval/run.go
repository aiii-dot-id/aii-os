package speecheval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var readClip = os.ReadFile

type Transcriber func(ctx context.Context, pcm []byte, sampleRate, channels int) (string, error)

func Run(ctx context.Context, m *Manifest, dir string, send Transcriber, progress func(done, total int, file string)) ([]ClipResult, error) {
	type loaded struct {
		file string
		wav  WAV
	}
	clips := make([]loaded, 0, len(m.Clips))
	for _, c := range m.Clips {

		blob, err := readClip(filepath.Join(dir, c.File))
		if err != nil {
			return nil, err
		}
		if err := m.VerifyBytes(c, blob); err != nil {
			return nil, fmt.Errorf("corpus drift: %w", err)
		}
		w, err := DecodeWAV(blob)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.File, err)
		}
		clips = append(clips, loaded{c.File, w})
	}
	results := make([]ClipResult, 0, len(clips))
	for i, c := range clips {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		started := time.Now()
		hyp, err := send(ctx, c.wav.PCM, c.wav.SampleRate, c.wav.Channels)
		r := ClipResult{File: c.file, Hyp: hyp, Duration: c.wav.Duration, Elapsed: time.Since(started)}
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return results, cerr
			}
			r.Hyp, r.Err = "", err.Error()
		}
		results = append(results, r)
		if progress != nil {
			progress(i+1, len(clips), c.file)
		}
	}
	return results, nil
}
