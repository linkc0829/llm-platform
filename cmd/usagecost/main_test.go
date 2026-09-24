package main

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/linkc0829/llm-platform/internal/shared"
)

func ptr(v float64) *float64 { return &v }

// The price math is what a dollar figure would be reported on; a unit slip
// (per token vs per MTok) or a cache ratio applied to output would move X by
// orders of magnitude without anyone noticing.
func TestCost(t *testing.T) {
	m := modelPrice{Name: "m", InputPerMTok: 2, OutputPerMTok: 10, CacheReadPerMTok: 0.2}
	tests := []struct {
		name       string
		tally      tally
		cacheHit   float64
		tokenRatio float64
		want       float64
	}{
		{name: "list_price", tally: tally{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}, tokenRatio: 1, want: 12},
		{name: "all_cached_input_output_unchanged", tally: tally{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}, cacheHit: 1, tokenRatio: 1, want: 10.2},
		{name: "half_cached", tally: tally{PromptTokens: 1_000_000}, cacheHit: 0.5, tokenRatio: 1, want: 1.1},
		{name: "token_ratio_scales_both", tally: tally{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}, tokenRatio: 1.3, want: 15.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cost(tt.tally, m, tt.cacheHit, tt.tokenRatio); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cost = %v, want %v", got, tt.want)
			}
		})
	}
}

// Y must not be reported from a partial set of inputs: a missing electricity
// price silently read as 0 would understate Y and overstate Z.
func TestMonthlyOperatingCost(t *testing.T) {
	full := operatingCost{
		HardwareUSD: ptr(36000), AmortizationMonths: ptr(36),
		PowerKW: ptr(1), PowerHoursPerMonth: ptr(730), PowerPricePerKWhUSD: ptr(0.1),
		OpsHoursPerMonth: ptr(10), OpsRateUSD: ptr(50),
	}
	y, missing := monthlyOperatingCost(full)
	if y == nil || math.Abs(*y-(1000+73+500)) > 1e-9 || len(missing) != 0 {
		t.Fatalf("Y = %v missing %v, want 1573 and none", y, missing)
	}

	partial := full
	partial.PowerPricePerKWhUSD = nil
	if y, missing := monthlyOperatingCost(partial); y != nil || len(missing) != 1 || missing[0] != "operating_cost.power_price_per_kwh_usd" {
		t.Errorf("partial: Y = %v missing %v, want nil and the power price", y, missing)
	}
}

// The shipped example has no prices on purpose; used as-is it must fail
// rather than report a $0 cloud equivalent.
func TestExampleConfigIsRejectedUntilFilled(t *testing.T) {
	b, err := os.ReadFile("pricing.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.validate(); err == nil {
		t.Fatal("example config validated; it would price everything at $0")
	}
}

// Test and service principals must stay out of the real-user baseline (it is what A4
// extrapolates from), and only KB traffic (workload rag) feeds tokens per
// question. Without -config no dollar figure may appear.
func TestBuild(t *testing.T) {
	lines := []usageLine{
		{TS: "2026-09-22T10:00:00", UserID: "p1", Workload: "rag", Path: "/v1/chat/completions", PromptTokens: 1000, CompletionTokens: 100},
		{TS: "2026-09-22T10:00:01", UserID: "p1", Workload: "rag", Path: "/v1/embeddings", InputCount: 1, InputChars: 5},
		{TS: "2026-09-22T11:00:00", UserID: "p2", Workload: "rag", Path: "/v1/chat/completions", PromptTokens: 3000, CompletionTokens: 300},
		{TS: "2026-09-23T11:00:00", UserID: "p2", Workload: "chat", Path: "/v1/chat/completions", PromptTokens: 500, CompletionTokens: 50},
		{TS: "2026-09-23T12:00:00", UserID: "p3", Workload: "chat", Path: "/v1/chat/completions", PromptTokens: 20, CompletionTokens: 2},
	}
	questions := []question{
		{Owner: "p1", Day: "2026-09-22"},
		{Owner: "p2", Day: "2026-09-22"},
		{Owner: "p2", Day: "2026-09-22"},
		{Owner: "p2", Day: "2026-09-23"},
	}
	rep := build(lines, questions, map[string]shared.Principal{"p1": {Name: "eval-support"}, "p2": {Name: "alice"}, "p3": {Name: "kb-service", Trusted: true}}, nil)

	if rep.Chat.Requests != 4 || rep.Embeddings.Chars != 5 || len(rep.ByDay) != 2 {
		t.Errorf("chat %d, embed chars %d, days %d", rep.Chat.Requests, rep.Embeddings.Chars, len(rep.ByDay))
	}
	// KB chat excludes the workload "chat" call: 4000 prompt / 4 questions.
	if rep.PromptTokensPerQuestion != 1000 || rep.CompletionTokensPerQuestion != 100 {
		t.Errorf("per question = %v/%v, want 1000/100", rep.PromptTokensPerQuestion, rep.CompletionTokensPerQuestion)
	}
	// alice: 3 questions over 2 active days, 3850 tokens.
	if rep.Real.Users != 1 || rep.Real.ActiveUserDays != 2 || rep.Real.QuestionsPerActiveUserDay != 1.5 || rep.Real.TokensPerActiveUserDay != 1925 {
		t.Errorf("real baseline = %+v", rep.Real)
	}
	if rep.Test.Users != 2 || rep.Test.Questions != 1 {
		t.Errorf("test baseline = %+v", rep.Test)
	}
	if rep.Cost != nil {
		t.Errorf("cost computed without a config: %+v", rep.Cost)
	}
}
