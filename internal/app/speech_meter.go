package app

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
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
// .
func (a *App) speechMonth(now time.Time) string {
	return now.In(a.speechZone()).Format("2006-01")
}

func (a *App) speechZone() *time.Location {
	name := strings.TrimSpace(a.configSnapshot().Timezone)
	if name == "" {
		return time.UTC
	}
	loc, err := timeLoadLocation(name)
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

// .
func (a *App) speechResets(now time.Time) time.Time {
	in := now.In(a.speechZone())
	return time.Date(in.Year(), in.Month(), 1, 0, 0, 0, 0, in.Location()).AddDate(0, 1, 0)
}

// .
// .
// .
func (a *App) meterSpeech(provider, direction string, characters int, heard time.Duration) {
	if a.store == nil || strings.TrimSpace(provider) == "" {
		return
	}
	ms := heard.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	if err := a.store.AddSpeechUse(store.SpeechUse{
		Provider: provider, Direction: direction, Period: a.speechMonth(time.Now()),
		Requests: 1, Characters: characters, Ms: ms,
	}); err != nil {
		// .
		// .
		logsink.Warn("voice.error", "the speech meter could not record %s: %v", provider, err)
	}
}

// .
// .
// .
func (a *App) speechSpent(direction string) (characters int, heard time.Duration) {
	if a.store == nil {
		return 0, 0
	}
	used, err := a.store.SpeechUsed(a.speechMonth(time.Now()))
	if err != nil {
		return 0, 0
	}
	for _, u := range used {
		if u.Direction != direction {
			continue
		}
		characters += u.Characters
		heard += time.Duration(u.Ms) * time.Millisecond
	}
	return characters, heard
}

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
func (a *App) reserveSpeaking(characters int) error {
	a.speechReserveMu.Lock()
	defer a.speechReserveMu.Unlock()
	if limit := a.configSnapshot().Speech.TTS.MonthlyCharacters; limit > 0 {
		spent, _ := a.speechSpent("tts")
		if spent+a.speechReservedChars+characters > limit {
			way := ""
			if a.speechReservedChars > 0 {
				way = fmt.Sprintf(" and %d on their way", a.speechReservedChars)
			}
			return fmt.Errorf("this month's ceiling for spoken replies (%d characters) is reached — %d spent%s, and it lifts on %s",
				limit, spent, way, a.speechResets(time.Now()).Format("2 January"))
		}
	}
	a.speechReservedChars += characters
	return nil
}

// .
// .
func (a *App) settleSpeaking(characters int) {
	if characters <= 0 {
		return
	}
	a.speechReserveMu.Lock()
	a.speechReservedChars -= characters
	if a.speechReservedChars < 0 {
		a.speechReservedChars = 0
	}
	a.speechReserveMu.Unlock()
}

// .
func (a *App) reserveHearing(d time.Duration) error {
	a.speechReserveMu.Lock()
	defer a.speechReserveMu.Unlock()
	if limit := a.configSnapshot().Speech.STT.MonthlyMinutes; limit > 0 {
		_, spent := a.speechSpent("stt")
		if spent+a.speechReservedHeard+d > time.Duration(limit)*time.Minute {
			way := ""
			if a.speechReservedHeard > 0 {
				way = fmt.Sprintf(" and %s on its way", a.speechReservedHeard.Round(time.Second))
			}
			return fmt.Errorf("this month's ceiling for the microphone (%d minutes) is reached — %s spent%s, and it lifts on %s",
				limit, spent.Round(time.Second), way, a.speechResets(time.Now()).Format("2 January"))
		}
	}
	a.speechReservedHeard += d
	return nil
}

// .
// .
func (a *App) settleHearing(d time.Duration) {
	if d <= 0 {
		return
	}
	a.speechReserveMu.Lock()
	a.speechReservedHeard -= d
	if a.speechReservedHeard < 0 {
		a.speechReservedHeard = 0
	}
	a.speechReserveMu.Unlock()
}

// .
func (a *App) speechSpend() []dashboard.SpeechSpent {
	if a.store == nil {
		return nil
	}
	used, err := a.store.SpeechUsed(a.speechMonth(time.Now()))
	if err != nil {
		return nil
	}
	out := make([]dashboard.SpeechSpent, 0, len(used))
	for _, u := range used {
		out = append(out, dashboard.SpeechSpent{
			Provider: u.Provider, Direction: u.Direction, Requests: u.Requests,
			Characters: u.Characters, Seconds: int((time.Duration(u.Ms) * time.Millisecond).Round(time.Second) / time.Second),
		})
	}
	return out
}
