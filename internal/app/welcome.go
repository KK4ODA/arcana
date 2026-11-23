package app

import (
	"context"
	"errors"
	"fmt"
)

// Getting started: the first-run wizard turns a few choices (topics,
// language, a map region, extras such as the medicines reference or a
// question-answering model, and how much of the drive to use) into a
// download plan, using the same recommender as Collections.

// Size tiers cap how much of the free space the plan may use.
var welcomeTiers = map[string]int64{
	"essential": 8_000_000_000,
	"standard":  40_000_000_000,
	// "comprehensive": everything above the reserve.
}

// WelcomeRequest is what the person chose.
type WelcomeRequest struct {
	Presets  []string `json:"presets"`
	Language string   `json:"language"`
	Tier     string   `json:"tier"`
	// Extras are specific catalog resources (a map region, a road network,
	// the drug label reference, a model, a language pack).
	Extras []string `json:"extras"`
}

// WelcomeItem is one resource of the plan.
type WelcomeItem struct {
	ResourceID string `json:"resourceId"`
	Title      string `json:"title"`
	Group      string `json:"group"`
	Reason     string `json:"reason,omitempty"`
	Size       int64  `json:"size"`
	Priority   int    `json:"priority,omitempty"`
}

// WelcomePlan is the proposed download.
type WelcomePlan struct {
	Tier    string        `json:"tier"`
	Budget  int64         `json:"budget"`
	Total   int64         `json:"total"`
	Items   []WelcomeItem `json:"items"`
	Skipped []WelcomeItem `json:"skipped"`
	Storage StorageReport `json:"storage"`
}

// PlanWelcome builds the download plan for the wizard. Extras come first
// (each was chosen by name), then each topic's recommendations within what
// is left of the budget.
func (a *App) PlanWelcome(ctx context.Context, req WelcomeRequest) (WelcomePlan, error) {
	if req.Language == "" {
		req.Language = "en"
	}
	if len(req.Presets) > 12 || len(req.Extras) > 30 {
		return WelcomePlan{}, errors.New("too many choices")
	}
	rep, err := a.Storage(ctx, nil)
	if err != nil {
		return WelcomePlan{}, err
	}
	plan := WelcomePlan{Tier: req.Tier, Items: []WelcomeItem{}, Skipped: []WelcomeItem{}}
	budget := rep.Plan.Headroom
	if c, ok := welcomeTiers[req.Tier]; ok && c < budget {
		budget = c
	} else if !ok {
		plan.Tier = "comprehensive"
	}
	if budget < 0 {
		budget = 0
	}
	plan.Budget = budget
	remaining := budget
	chosen := map[string]bool{}
	present := map[string]bool{}
	if installed, err := a.Store.ListInstalled(ctx); err == nil {
		for _, in := range installed {
			present[in.ResourceID] = true
		}
	}
	if jobs, err := a.Store.ListJobs(ctx); err == nil {
		for _, j := range jobs {
			if j.Active() {
				present[j.ResourceID] = true
			}
		}
	}
	// addWithDeps adds a resource and what it needs, if it all fits.
	var addWithDeps func(id, group, reason string, depth int) error
	addWithDeps = func(id, group, reason string, depth int) error {
		if chosen[id] || present[id] {
			return nil
		}
		r, err := a.Store.GetResource(ctx, id)
		if err != nil {
			return fmt.Errorf("%s is not in the catalog (refresh the catalog first)", id)
		}
		need := r.DownloadSize
		var deps []string
		for _, d := range r.Dependencies {
			if !chosen[d] && !present[d] && depth < 3 {
				if dr, err := a.Store.GetResource(ctx, d); err == nil {
					need += dr.DownloadSize
					deps = append(deps, d)
				}
			}
		}
		item := WelcomeItem{ResourceID: id, Title: r.Title, Group: group, Reason: reason, Size: r.DownloadSize}
		if need > remaining {
			plan.Skipped = append(plan.Skipped, item)
			return nil
		}
		chosen[id] = true
		remaining -= r.DownloadSize
		plan.Items = append(plan.Items, item)
		for _, d := range deps {
			if err := addWithDeps(d, group, "Needed by "+r.Title+".", depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range req.Extras {
		if err := addWithDeps(id, "Your choices", "", 0); err != nil {
			plan.Skipped = append(plan.Skipped, WelcomeItem{ResourceID: id, Title: id, Group: "Your choices", Reason: err.Error()})
		}
	}
	presets, _ := a.Presets()
	titles := map[string]string{}
	for _, p := range presets {
		titles[p.ID] = p.Title
	}
	for _, pid := range req.Presets {
		if titles[pid] == "" {
			return WelcomePlan{}, fmt.Errorf("unknown collection %q", pid)
		}
		rec, err := a.recommendWithin(ctx, pid, req.Language, remaining, chosen)
		if err != nil {
			return WelcomePlan{}, err
		}
		for _, it := range rec.Items {
			item := WelcomeItem{ResourceID: it.ResourceID, Title: it.Title, Group: titles[pid], Reason: it.Reason, Size: it.Size, Priority: it.Priority}
			switch it.Status {
			case "selected":
				if chosen[it.ResourceID] {
					continue
				}
				chosen[it.ResourceID] = true
				remaining -= it.Size
				plan.Items = append(plan.Items, item)
			case "skipped":
				plan.Skipped = append(plan.Skipped, item)
			}
		}
	}
	var ids []string
	for _, it := range plan.Items {
		plan.Total += it.Size
		ids = append(ids, it.ResourceID)
	}
	plan.Storage, err = a.Storage(ctx, ids)
	return plan, err
}
