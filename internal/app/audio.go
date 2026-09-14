package app

import (
	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
// .
func (a *App) AudioPlane() *audio.Plane {
	a.audioOnce.Do(func() {
		p := audio.NewPlane()
		p.SafeMode = a.SafeMode
		a.audio = p
	})
	return a.audio
}
