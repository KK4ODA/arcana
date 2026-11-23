package app

import (
	"context"
	"testing"
)

func TestPlanWelcome(t *testing.T) {
	a := openTestApp(t)
	ctx := context.Background()
	plan, err := a.PlanWelcome(ctx, WelcomeRequest{Presets: []string{"amateur-radio"}, Language: "en", Tier: "essential",
		Extras: []string{"arcana.assistant-qwen2.5-0.5b"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]WelcomeItem{}
	for _, it := range plan.Items {
		got[it.ResourceID] = it
	}
	// The model brings its engine; the radio collection's bundled
	// regulations are chosen; nothing is listed twice.
	for _, id := range []string{"arcana.assistant-qwen2.5-0.5b", "arcana.llamafile", "arcana.us-cfr47-part97", "arcana.us-cfr47-part95"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("%s missing from %+v", id, plan.Items)
		}
	}
	if len(got) != len(plan.Items) || plan.Budget > 8_000_000_000 || plan.Total <= 0 || plan.Storage.Plan.Selected != plan.Total {
		t.Fatalf("plan: %+v", plan)
	}
	if got["arcana.llamafile"].Reason == "" {
		t.Fatal("dependency not explained")
	}
	// An unknown extra is reported, not fatal; an unknown collection is.
	plan, err = a.PlanWelcome(ctx, WelcomeRequest{Extras: []string{"nope.missing"}})
	if err != nil || len(plan.Skipped) != 1 {
		t.Fatalf("unknown extra: %+v %v", plan, err)
	}
	if _, err := a.PlanWelcome(ctx, WelcomeRequest{Presets: []string{"nope"}}); err == nil {
		t.Fatal("unknown collection accepted")
	}
	// A tiny budget skips what does not fit.
	welcomeTiers["tiny"] = 1000
	defer delete(welcomeTiers, "tiny")
	plan, _ = a.PlanWelcome(ctx, WelcomeRequest{Tier: "tiny", Extras: []string{"arcana.assistant-qwen2.5-0.5b"}})
	if len(plan.Items) != 0 || len(plan.Skipped) != 1 {
		t.Fatalf("tiny budget: %+v", plan)
	}
}
