package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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
func (a *App) speechLists(name, direction, search, language, typedKey string) (dashboard.SpeechLists, error) {
	out := dashboard.SpeechLists{Provider: name, Direction: direction, Search: search, Language: language}
	dir := speech.STT
	switch direction {
	case "stt":
	case "tts":
		dir = speech.TTS
	default:
		return out, fmt.Errorf("direction %q is stt or tts", direction)
	}
	reg, err := a.loadProviders()
	if err != nil {
		return out, err
	}
	e := speechEntryNamed(reg, name)
	var o *speechOffer
	if e != nil {
		o = e.Speech.offer(dir)
	}
	if o == nil {
		return out, fmt.Errorf("%s is not a %s service", name, dir)
	}
	if o.ListModels == nil && o.ListVoices == nil {
		return out, nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	env := a.configSnapshot().Speech.STT.APIKeyEnv
	if dir == speech.TTS {
		env = a.configSnapshot().Speech.TTS.APIKeyEnv
	}
	key := providerAPIKey(*e, strings.TrimSpace(typedKey), env)
	if key == "" && shippedSpeechVendor(name) {
		// .
		out.NeedsKey = true
		return out, nil
	}

	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := a.configSnapshot()
	timeout := checkTimeout(0, cfg.LLM.ProbeTimeoutSeconds)
	svc := o.Service
	ask := speech.Ask{Search: search, Language: language}
	read := func(l *speech.List) ([]speech.Item, bool, string) {
		if l.Spec != "" {
			items, err := a.specListed(ctx, svc, dir, l, timeout)
			if err != nil {
				return nil, false, listRefusal(name, err)
			}
			return items, true, ""
		}
		items, whole, err := svc.ListItems(ctx, dir, l, e.URL, key, timeout, ask)
		if err != nil {
			return nil, false, listRefusal(name, err)
		}
		return items, whole, ""
	}
	var wg sync.WaitGroup
	var models, voices []speech.Item
	if o.ListModels != nil {
		wg.Go(func() {
			items, whole, why := read(o.ListModels)
			models, out.ModelsComplete, out.ModelsError, out.ModelsListed = items, whole, why, why == ""
		})
	}
	if o.ListVoices != nil {
		wg.Go(func() {
			items, whole, why := read(o.ListVoices)
			voices, out.VoicesComplete, out.VoicesError, out.VoicesListed = items, whole, why, why == ""
		})
	}
	wg.Wait()
	out.Models, out.Voices = speechItems(models), speechItems(voices)
	out.Languages = spokenLanguages(models, voices)
	out.Searched = search != "" && ((o.ListVoices != nil && o.ListVoices.Search != "") || (o.ListModels != nil && o.ListModels.Search != ""))
	return out, nil
}

// .
// .
type speechItem = speech.Item

// .
// .
// .
// .
func (a *App) specListed(ctx context.Context, svc speech.Service, dir speech.Direction, l *speech.List, timeout time.Duration) ([]speech.Item, error) {
	a.specMu.Lock()
	said, known := a.specSaid[l.Spec]
	a.specMu.Unlock()
	if known {
		return said, nil
	}
	items, _, err := svc.ListItems(ctx, dir, l, "", "", timeout, speech.Ask{})
	if err != nil {
		return nil, err
	}
	a.specMu.Lock()
	if a.specSaid == nil {
		a.specSaid = map[string][]speech.Item{}
	}
	a.specSaid[l.Spec] = items
	a.specMu.Unlock()
	return items, nil
}

// .
// .
// .
// .
func shippedSpeechVendor(name string) bool {
	for _, e := range embeddedRegistry().Providers {
		if e.Name == name && e.Speech != nil {
			return true
		}
	}
	return false
}

func speechItems(in []speech.Item) []dashboard.SpeechItem {
	out := make([]dashboard.SpeechItem, 0, len(in))
	for _, it := range in {
		item := dashboard.SpeechItem{ID: it.ID, Name: it.Name, Detail: it.Detail}
		for _, l := range it.Languages {
			item.Languages = append(item.Languages, l.ID)
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// .
// .
// .
func spokenLanguages(lists ...[]speech.Item) []dashboard.SpeechLanguage {
	named := map[string]string{}
	for _, items := range lists {
		for _, it := range items {
			for _, l := range it.Languages {
				if l.ID == "" {
					continue
				}
				if named[l.ID] == "" {
					named[l.ID] = l.Name
				}
			}
		}
	}
	out := make([]dashboard.SpeechLanguage, 0, len(named))
	for id, name := range named {
		out = append(out, dashboard.SpeechLanguage{ID: id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) == 0 {
		return nil
	}
	return out
}

// .
// .
func listRefusal(service string, err error) string {
	var refusal *speech.Refusal
	if errors.As(err, &refusal) {
		return fmt.Sprintf("%s refused the list (%d): %s", service, refusal.Status, refusal.Said)
	}
	return strings.TrimPrefix(err.Error(), "speech: ")
}
