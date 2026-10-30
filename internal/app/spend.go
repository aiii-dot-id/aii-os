package app

import (
	"cmp"
	"slices"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (a *App) spendView(days int) (*dashboard.SpendView, error) {
	zone := a.speechZone()
	now := time.Now().In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	first := today.AddDate(0, 0, 1-max(days, 1))
	view := &dashboard.SpendView{Zone: zone.String(), Days: []dashboard.SpendDay{}}
	if a.store == nil {
		return view, nil
	}
	rows, err := a.store.SpendSince(first.UnixMilli())
	if err != nil {
		return nil, err
	}
	type key struct{ day, role, provider, model string }
	lines := map[key]*dashboard.SpendLine{}
	for _, r := range rows {
		k := key{time.UnixMilli(r.TsMs).In(zone).Format(time.DateOnly), spendRole(r), r.Provider, r.Model}
		l := lines[k]
		if l == nil {
			l = &dashboard.SpendLine{Role: k.role, Provider: k.provider, Model: k.model}
			lines[k] = l
		}
		l.Calls += r.Calls
		if !r.Known {
			l.Unknown++
			continue
		}
		l.Tokens += r.Tokens
		l.Input += r.Input
		l.Output += r.Output
		l.Cached += r.Cached
		l.Floor = l.Floor || !r.Complete
	}
	byDay := map[string]*dashboard.SpendDay{}
	for k, l := range lines {
		d := byDay[k.day]
		if d == nil {
			d = &dashboard.SpendDay{Day: k.day}
			byDay[k.day] = d
		}
		d.Lines = append(d.Lines, *l)
		d.Tokens += l.Tokens
		d.Floor = d.Floor || l.Floor
		d.Unknown += l.Unknown
	}
	for _, d := range byDay {
		slices.SortFunc(d.Lines, func(x, y dashboard.SpendLine) int {
			return cmp.Or(cmp.Compare(y.Tokens, x.Tokens), cmp.Compare(x.Role, y.Role), cmp.Compare(x.Provider, y.Provider), cmp.Compare(x.Model, y.Model))
		})
		view.Days = append(view.Days, *d)
	}
	slices.SortFunc(view.Days, func(x, y dashboard.SpendDay) int { return cmp.Compare(y.Day, x.Day) })
	return view, nil
}

func spendRole(r store.SpendRow) string {
	switch r.Kind {
	case store.SpendFacility:
		return "facility:" + r.Name
	case store.SpendSubagent:
		if r.Name == "" {
			return "sub-agent"
		}
		return "sub-agent:" + r.Name
	}
	return "turns"
}
