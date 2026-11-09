package app

import (
	"errors"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const trailLinkLimit = 50

func (a *App) beliefTrailForDashboard(id string) (*dashboard.BeliefTrail, error) {
	if a.store == nil {

		return nil, dashboard.ErrRecordNotOpen
	}
	belief, err := a.store.BeliefForTrail(id)
	if err != nil {
		if errors.Is(err, store.ErrBeliefNotFound) {
			return nil, dashboard.ErrBeliefNotFound
		}
		return nil, err
	}

	trail := &dashboard.BeliefTrail{
		Belief: dashboard.TrailBelief{
			ID:            belief.ID,
			Statement:     belief.Statement,
			Ring:          belief.Ring,
			Confidence:    belief.Confidence,
			EvidenceCount: belief.EvidenceCount,
			Archived:      belief.Archived,
			LastSeq:       int64(belief.LastSeq),
		},
	}

	var recallCount int64
	accesses, accessErr := a.store.MemoryAccesses("beliefs", []string{belief.ID})
	if accessErr == nil {
		access := accesses[belief.ID]
		recallCount = access.Count
		count := access.Count
		trail.Belief.RecallCount = &count
		if access.Count > 0 {
			at := access.LastAt.UTC().Format(time.RFC3339)
			trail.Belief.RecalledAt = &at
		}
	}

	held, heldOK, err := a.store.LedgerTimeAt(belief.FirstSeq)
	if err != nil {
		return nil, err
	}
	if heldOK {
		trail.Belief.HeldSince = held.UTC().Format(time.RFC3339)
	}

	footings, footingErr := a.store.BeliefFootings(a.lifetime(), []string{belief.ID})
	if accessErr == nil && footingErr == nil {
		reading := memory.BeliefRecall(memory.RecallFacts{
			Ring:          belief.Ring,
			EvidenceCount: belief.EvidenceCount,
			Born:          held,
			Accesses:      recallCount,
			Archived:      belief.Archived,
			Superseded:    belief.SupersededBy != "",
			Footing:       footings[belief.ID],
		}, time.Now())
		trail.Belief.Recall = reading.Strength
		trail.Belief.Fading = reading.Fading
	}

	arms, err := a.store.TrailArmsForBelief(belief.ID, trailLinkLimit)
	if err != nil {
		return nil, err
	}
	if len(arms.Origins) > trailLinkLimit {
		arms.Origins = arms.Origins[:trailLinkLimit]
		trail.Truncated.Origins = true
	}
	if len(arms.Contradicts) > trailLinkLimit {
		arms.Contradicts = arms.Contradicts[:trailLinkLimit]
		trail.Truncated.Contradicts = true
	}
	if len(arms.Supersedes) > trailLinkLimit {
		arms.Supersedes = arms.Supersedes[:trailLinkLimit]
		trail.Truncated.Supersedes = true
	}

	if trail.Origins, err = a.trailLinks(arms.Origins, belief.ID); err != nil {
		return nil, err
	}
	if trail.Contradicts, err = a.trailLinks(arms.Contradicts, belief.ID); err != nil {
		return nil, err
	}
	if trail.Supersedes, err = a.trailLinks(arms.Supersedes, belief.ID); err != nil {
		return nil, err
	}
	if len(arms.SupersededBy) > 0 {
		links, err := a.trailLinks(arms.SupersededBy[:1], belief.ID)
		if err != nil {
			return nil, err
		}
		link := links[0]
		trail.SupersededBy = &link
	}
	return trail, nil
}

func (a *App) trailLinks(edges []store.TrailEdge, about string) ([]dashboard.TrailLink, error) {
	if len(edges) == 0 {
		return nil, nil
	}
	out := make([]dashboard.TrailLink, 0, len(edges))
	for _, e := range edges {
		link, err := a.trailLink(e, about)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, nil
}

func (a *App) trailLink(e store.TrailEdge, about string) (dashboard.TrailLink, error) {
	far := e.ToID
	if far == about {
		far = e.FromID
	}
	strength := e.Strength
	link := dashboard.TrailLink{
		EdgeType:   e.EdgeType,
		Strength:   &strength,
		CreatedSeq: int64(e.CreatedSeq),
		ID:         far,
	}
	if at, ok, err := a.store.LedgerTimeAt(e.CreatedSeq); err != nil {
		return link, err
	} else if ok {
		link.CreatedAt = at.UTC().Format(time.RFC3339)
	}

	b, err := a.store.GetBelief(far)
	switch {
	case err == nil && b != nil:
		link.Kind = "belief"
		link.Text = b.Statement
		return link, nil
	case err != nil && !errors.Is(err, store.ErrBeliefNotFound):
		return link, err
	}

	x, err := a.store.GetExperience(far)
	switch {
	case err == nil && x != nil:
		link.Kind = "experience"
		link.Text = x.Content
		link.Provenance = x.Provenance

		turn, cited, terr := a.store.ExperienceDreamedTurn(far)
		if terr != nil {
			return link, terr
		}
		if cited {
			link.Turn = &turn
		}
		return link, nil
	case err != nil && !errors.Is(err, store.ErrExperienceNotFound):
		return link, err
	}

	link.Kind = "other"
	return link, nil
}
