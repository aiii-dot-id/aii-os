package identity

import "github.com/aiii-dot-id/aii-os/internal/memory"

// .
// .
// .
func (e *Engine) Instruments() *memory.Facility { return e.instruments }

// .
// .
// .
func (e *Engine) SetEmbedder(em memory.Embedder) { e.instruments.SetEmbedder(em) }
