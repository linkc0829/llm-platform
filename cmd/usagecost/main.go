// Command usagecost is the A3 unit baseline. From gateway_usage and kb_query
// lines it measures tokens per KB question and questions and tokens per user
// per day, keeping eval and load-test traffic apart from real users.
//
// Dollar figures are opt-in (-config): the cloud-equivalent spend X depends
// entirely on which cloud model it is priced at, and that choice is only fair
// once a model has been shown to answer the KB eval as well as the on-prem one.
// Until then, and until real users and an operating-cost quote exist, only the
// token baseline is reported.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/linkc0829/llm-platform/internal/auth"
	"github.com/linkc0829/llm-platform/internal/shared"
)

type modelPrice struct {
	Name             string  `json:"name"`
	InputPerMTok     float64 `json:"input_per_mtok"`
	OutputPerMTok    float64 `json:"output_per_mtok"`
	CacheReadPerMTok float64 `json:"cache_read_per_mtok"`
}

// operatingCost fields are nil until someone has a real number for them.
type operatingCost struct {
	HardwareUSD         *float64 `json:"hardware_usd"`
	AmortizationMonths  *float64 `json:"amortization_months"`
	PowerKW             *float64 `json:"power_kw"`
	PowerHoursPerMonth  *float64 `json:"power_hours_per_month"`
	PowerPricePerKWhUSD *float64 `json:"power_price_per_kwh_usd"`
	OpsHoursPerMonth    *float64 `json:"ops_hours_per_month"`
	OpsRateUSD          *float64 `json:"ops_rate_usd"`
}

type projection struct {
	Users                  *float64 `json:"users"`
	QuestionsPerUserPerDay *float64 `json:"questions_per_user_per_day"`
	WorkdaysPerMonth       *float64 `json:"workdays_per_month"`
}

// config enables the dollar figures. See pricing.example.json.
type config struct {
	PricesAsOf   string       `json:"prices_as_of"`
	PricesSource string       `json:"prices_source"`
	Models       []modelPrice `json:"models"`
	// CacheHitRatio is the share of prompt tokens a cloud deployment would
	// serve from prompt cache. 0 gives the upper bound: the KB resends the same
	// system prompt every call, so a real deployment would cache much of it.
	CacheHitRatio float64 `json:"cache_hit_ratio"`
	// TokenRatio converts backend tokens to the priced model's tokens. The
	// logged counts come from the on-prem model's tokenizer, not the priced model's.
	TokenRatio    float64       `json:"token_ratio"`
	OperatingCost operatingCost `json:"operating_cost"`
	Projection    projection    `json:"projection"`
}

// validate rejects a config copied from the example without real prices: a
// zero price would report a cloud-equivalent spend of $0 instead of failing.
func (c *config) validate() error {
	if len(c.Models) == 0 {
		return fmt.Errorf("no models to price")
	}
	for _, m := range c.Models {
		if m.InputPerMTok <= 0 || m.OutputPerMTok <= 0 {
			return fmt.Errorf("model %q has no input/output price", m.Name)
		}
	}
	if c.PricesAsOf == "" || c.PricesSource == "" {
		return fmt.Errorf("prices_as_of and prices_source are required so the report says where prices came from")
	}
	if c.TokenRatio == 0 {
		c.TokenRatio = 1
	}
	return nil
}

type usageLine struct {
	TS               string `json:"ts"`
	Msg              string `json:"msg"`
	UserID           string `json:"user_id"`
	Workload         string `json:"workload"`
	Path             string `json:"path"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	InputCount       int64  `json:"input_count"`
	InputChars       int64  `json:"input_chars"`
}

// question is one kb_query line: who asked, and on which day.
type question struct {
	Owner string
	Day   string
}

type tally struct {
	Requests         int64 `json:"requests"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

func (t *tally) add(l usageLine) {
	t.Requests++
	t.PromptTokens += l.PromptTokens
	t.CompletionTokens += l.CompletionTokens
}

func (t tally) tokens() int64 { return t.PromptTokens + t.CompletionTokens }

type userRow struct {
	User       string `json:"user"`
	Test       bool   `json:"test"`
	Questions  int64  `json:"questions"`
	ActiveDays int    `json:"active_days"`
	tally
}

// baseline is the per-question and per-user-day figures for one population.
type baseline struct {
	Users                     int     `json:"users"`
	ActiveUserDays            int     `json:"active_user_days"`
	Questions                 int64   `json:"questions"`
	QuestionsPerActiveUserDay float64 `json:"questions_per_active_user_day"`
	TokensPerActiveUserDay    float64 `json:"tokens_per_active_user_day"`
}

type costReport struct {
	PricesAsOf              string             `json:"prices_as_of"`
	PricesSource            string             `json:"prices_source"`
	Assumptions             map[string]float64 `json:"assumptions"`
	ChatUSD                 map[string]float64 `json:"chat_usd"`
	PerQuestionUSD          map[string]float64 `json:"per_question_usd,omitempty"`
	OperatingCostMonthlyUSD *float64           `json:"operating_cost_monthly_usd"`
	ProjectedMonthlyXUSD    map[string]float64 `json:"projected_monthly_x_usd,omitempty"`
	ProjectedMonthlyZUSD    map[string]float64 `json:"projected_monthly_z_usd,omitempty"`
	Missing                 []string           `json:"missing_parameters,omitempty"`
}

type report struct {
	Generated  string    `json:"generated"`
	Window     [2]string `json:"window"`
	Chat       tally     `json:"chat"`
	TestShare  float64   `json:"test_token_share"`
	Embeddings struct {
		Requests int64 `json:"requests"`
		Inputs   int64 `json:"inputs"`
		Chars    int64 `json:"chars"`
	} `json:"embeddings"`

	// KBChat is chat traffic the KB made (workload "rag"), divided by the
	// kb_query count for the per-question figures.
	KBChat                      tally   `json:"kb_chat"`
	KBQuestions                 int64   `json:"kb_questions"`
	PromptTokensPerQuestion     float64 `json:"prompt_tokens_per_question"`
	CompletionTokensPerQuestion float64 `json:"completion_tokens_per_question"`

	Real baseline `json:"real_users"`
	// Test covers eval, load-test and trusted service principals.
	Test   baseline         `json:"not_users"`
	ByDay  map[string]tally `json:"by_day"`
	ByUser []userRow        `json:"by_user"`

	Cost *costReport `json:"cost,omitempty"`
}

func main() {
	gatewayLog := flag.String("gateway-log", "log/gateway.log", "gateway log with gateway_usage lines")
	kbLog := flag.String("kb-log", "log/kb.log", "KB log with kb_query lines")
	authFile := flag.String("auth", "auth.json", "auth file to name principals; IDs are shown if it cannot be read")
	configPath := flag.String("config", "", "prices and operating-cost parameters; without it no dollar figures are computed")
	since := flag.String("since", "", "first day to include, YYYY-MM-DD")
	until := flag.String("until", "", "last day to include, YYYY-MM-DD")
	out := flag.String("out", "", "JSON report path (default metrics/usagecost-<date>.json)")
	flag.Parse()

	var cfg *config
	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("usagecost: %v", err)
		}
		cfg = &config{}
		if err := json.Unmarshal(b, cfg); err != nil {
			log.Fatalf("usagecost: config: %v", err)
		}
		if err := cfg.validate(); err != nil {
			log.Fatalf("usagecost: config: %v", err)
		}
	}

	lines, err := readUsage(*gatewayLog, *since, *until)
	if err != nil {
		log.Fatalf("usagecost: %v", err)
	}
	if len(lines) == 0 {
		log.Fatalf("usagecost: no gateway_usage lines in %s for the window", *gatewayLog)
	}
	questions, err := readQuestions(*kbLog, lines[0].TS, lines[len(lines)-1].TS)
	if err != nil {
		log.Printf("usagecost: %v (per-question figures skipped)", err)
	}

	rep := build(lines, questions, principals(*authFile), cfg)
	printReport(os.Stdout, rep)

	if *out == "" {
		*out = filepath.Join("metrics", "usagecost-"+time.Now().Format("20060102")+".json")
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("usagecost: %v", err)
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		log.Fatalf("usagecost: %v", err)
	}
	fmt.Println("report:", *out)
}

// readUsage returns gateway_usage lines in log order whose day falls in
// [since, until]; empty bounds are open.
func readUsage(path, since, until string) ([]usageLine, error) {
	var lines []usageLine
	err := scanJSON(path, func(b []byte) {
		var l usageLine
		if json.Unmarshal(b, &l) != nil || l.Msg != "gateway_usage" || len(l.TS) < 10 {
			return
		}
		day := l.TS[:10]
		if (since != "" && day < since) || (until != "" && day > until) {
			return
		}
		lines = append(lines, l)
	})
	return lines, err
}

// readQuestions returns kb_query lines between the first and last gateway line.
// Timestamps share one format and zone, so string order is time order.
func readQuestions(path, from, to string) ([]question, error) {
	var qs []question
	err := scanJSON(path, func(b []byte) {
		var l struct {
			TS      string `json:"ts"`
			Msg     string `json:"msg"`
			OwnerID string `json:"owner_id"`
		}
		if json.Unmarshal(b, &l) == nil && l.Msg == "kb_query" && l.TS >= from && l.TS <= to {
			qs = append(qs, question{Owner: l.OwnerID, Day: l.TS[:10]})
		}
	})
	return qs, err
}

func scanJSON(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fn(sc.Bytes())
	}
	return sc.Err()
}

func principals(path string) map[string]shared.Principal {
	known := map[string]shared.Principal{}
	store, err := auth.LoadFile(path, zap.NewNop())
	if err != nil {
		return known
	}
	records, err := store.ListTokens(context.Background())
	if err != nil {
		return known
	}
	for _, r := range records {
		known[r.ID] = r.Principal
	}
	return known
}

func isChat(path string) bool { return strings.HasSuffix(path, "/completions") }

func build(lines []usageLine, questions []question, known map[string]shared.Principal, cfg *config) *report {
	rep := &report{
		Generated:   time.Now().Format(time.RFC3339),
		Window:      [2]string{lines[0].TS, lines[len(lines)-1].TS},
		ByDay:       map[string]tally{},
		KBQuestions: int64(len(questions)),
	}
	users := map[string]*userRow{}
	days := map[string]map[string]bool{}
	row := func(id string) *userRow {
		p := known[id]
		n := p.Name
		if n == "" {
			n = id
		}
		if users[n] == nil {
			users[n] = &userRow{User: n, Test: p.Kind() != shared.KindUser}
			days[n] = map[string]bool{}
		}
		return users[n]
	}

	var testTokens int64
	for _, l := range lines {
		if !isChat(l.Path) {
			rep.Embeddings.Requests++
			rep.Embeddings.Inputs += l.InputCount
			rep.Embeddings.Chars += l.InputChars
			continue
		}
		rep.Chat.add(l)
		d := rep.ByDay[l.TS[:10]]
		d.add(l)
		rep.ByDay[l.TS[:10]] = d
		if l.Workload == "rag" {
			rep.KBChat.add(l)
		}
		u := row(l.UserID)
		u.add(l)
		days[u.User][l.TS[:10]] = true
		if u.Test {
			testTokens += l.PromptTokens + l.CompletionTokens
		}
	}
	for _, q := range questions {
		u := row(q.Owner)
		u.Questions++
		days[u.User][q.Day] = true
	}
	if t := rep.Chat.tokens(); t > 0 {
		rep.TestShare = float64(testTokens) / float64(t)
	}
	if rep.KBQuestions > 0 {
		rep.PromptTokensPerQuestion = float64(rep.KBChat.PromptTokens) / float64(rep.KBQuestions)
		rep.CompletionTokensPerQuestion = float64(rep.KBChat.CompletionTokens) / float64(rep.KBQuestions)
	}

	var realTokens, testTokensAll int64
	for n, u := range users {
		u.ActiveDays = len(days[n])
		b := &rep.Real
		if u.Test {
			b = &rep.Test
			testTokensAll += u.tokens()
		} else {
			realTokens += u.tokens()
		}
		b.Users++
		b.ActiveUserDays += u.ActiveDays
		b.Questions += u.Questions
		rep.ByUser = append(rep.ByUser, *u)
	}
	perDay := func(b *baseline, tokens int64) {
		if b.ActiveUserDays > 0 {
			b.QuestionsPerActiveUserDay = float64(b.Questions) / float64(b.ActiveUserDays)
			b.TokensPerActiveUserDay = float64(tokens) / float64(b.ActiveUserDays)
		}
	}
	perDay(&rep.Real, realTokens)
	perDay(&rep.Test, testTokensAll)
	sort.Slice(rep.ByUser, func(i, j int) bool { return rep.ByUser[i].tokens() > rep.ByUser[j].tokens() })

	if cfg != nil {
		rep.Cost = priceReport(*cfg, rep)
	}
	return rep
}

// cost prices one tally at one model's list price in USD.
func cost(t tally, m modelPrice, cacheHit, tokenRatio float64) float64 {
	in := float64(t.PromptTokens) * tokenRatio
	out := float64(t.CompletionTokens) * tokenRatio
	return (in*((1-cacheHit)*m.InputPerMTok+cacheHit*m.CacheReadPerMTok) + out*m.OutputPerMTok) / 1e6
}

// monthlyOperatingCost is Y, or nil with the missing parameter names.
func monthlyOperatingCost(o operatingCost) (*float64, []string) {
	var missing []string
	need := func(name string, v *float64) float64 {
		if v == nil {
			missing = append(missing, "operating_cost."+name)
			return 0
		}
		return *v
	}
	hw := need("hardware_usd", o.HardwareUSD)
	months := need("amortization_months", o.AmortizationMonths)
	kw := need("power_kw", o.PowerKW)
	hours := need("power_hours_per_month", o.PowerHoursPerMonth)
	kwh := need("power_price_per_kwh_usd", o.PowerPricePerKWhUSD)
	opsHours := need("ops_hours_per_month", o.OpsHoursPerMonth)
	rate := need("ops_rate_usd", o.OpsRateUSD)
	if len(missing) > 0 || months == 0 {
		return nil, missing
	}
	y := hw/months + kw*hours*kwh + opsHours*rate
	return &y, nil
}

func priceReport(cfg config, rep *report) *costReport {
	c := &costReport{
		PricesAsOf:   cfg.PricesAsOf,
		PricesSource: cfg.PricesSource,
		Assumptions:  map[string]float64{"cache_hit_ratio": cfg.CacheHitRatio, "token_ratio": cfg.TokenRatio},
		ChatUSD:      map[string]float64{},
	}
	for _, m := range cfg.Models {
		c.ChatUSD[m.Name] = cost(rep.Chat, m, cfg.CacheHitRatio, cfg.TokenRatio)
		if rep.KBQuestions > 0 {
			if c.PerQuestionUSD == nil {
				c.PerQuestionUSD = map[string]float64{}
			}
			c.PerQuestionUSD[m.Name] = cost(rep.KBChat, m, cfg.CacheHitRatio, cfg.TokenRatio) / float64(rep.KBQuestions)
		}
	}

	y, missing := monthlyOperatingCost(cfg.OperatingCost)
	c.OperatingCostMonthlyUSD = y
	c.Missing = missing
	p := cfg.Projection
	for n, v := range map[string]*float64{"users": p.Users, "questions_per_user_per_day": p.QuestionsPerUserPerDay, "workdays_per_month": p.WorkdaysPerMonth} {
		if v == nil {
			c.Missing = append(c.Missing, "projection."+n)
		}
	}
	sort.Strings(c.Missing)
	if p.Users == nil || p.QuestionsPerUserPerDay == nil || p.WorkdaysPerMonth == nil || c.PerQuestionUSD == nil {
		return c
	}
	monthlyQuestions := *p.Users * *p.QuestionsPerUserPerDay * *p.WorkdaysPerMonth
	c.ProjectedMonthlyXUSD = map[string]float64{}
	for n, perQ := range c.PerQuestionUSD {
		c.ProjectedMonthlyXUSD[n] = perQ * monthlyQuestions
	}
	if y != nil {
		c.ProjectedMonthlyZUSD = map[string]float64{}
		for n, x := range c.ProjectedMonthlyXUSD {
			c.ProjectedMonthlyZUSD[n] = x - *y
		}
	}
	return c
}

func printReport(w io.Writer, r *report) {
	fmt.Fprintf(w, "window        %s .. %s\n", r.Window[0], r.Window[1])
	fmt.Fprintf(w, "chat          %d requests, prompt %d, completion %d tokens (%.1f%% not from users)\n",
		r.Chat.Requests, r.Chat.PromptTokens, r.Chat.CompletionTokens, r.TestShare*100)
	fmt.Fprintf(w, "embeddings    %d requests, %d inputs, %d chars\n",
		r.Embeddings.Requests, r.Embeddings.Inputs, r.Embeddings.Chars)
	if r.KBQuestions > 0 {
		fmt.Fprintf(w, "per question  %.0f prompt + %.0f completion tokens (%d KB questions, %d KB chat calls)\n",
			r.PromptTokensPerQuestion, r.CompletionTokensPerQuestion, r.KBQuestions, r.KBChat.Requests)
	}
	for _, b := range []struct {
		label string
		b     baseline
	}{{"real users", r.Real}, {"not users", r.Test}} {
		fmt.Fprintf(w, "%-13s %d principals, %d active user-days, %.1f questions and %.0f tokens per active user-day\n",
			b.label, b.b.Users, b.b.ActiveUserDays, b.b.QuestionsPerActiveUserDay, b.b.TokensPerActiveUserDay)
	}
	fmt.Fprintln(w, "by user")
	for _, u := range r.ByUser {
		tag := ""
		if u.Test {
			tag = " (not a user)"
		}
		fmt.Fprintf(w, "  %-40s %5d q %3d d %6d req %10d prompt %8d completion%s\n",
			u.User, u.Questions, u.ActiveDays, u.Requests, u.PromptTokens, u.CompletionTokens, tag)
	}
	if r.Cost == nil {
		fmt.Fprintln(w, "cost          not computed (pass -config to price; see pricing.example.json)")
		return
	}
	c := r.Cost
	fmt.Fprintf(w, "prices        %s (%s), cache_hit_ratio=%.2f token_ratio=%.2f\n",
		c.PricesAsOf, c.PricesSource, c.Assumptions["cache_hit_ratio"], c.Assumptions["token_ratio"])
	for n, x := range c.ChatUSD {
		fmt.Fprintf(w, "  X at %-16s $%.2f, per question $%.6f\n", n, x, c.PerQuestionUSD[n])
	}
	if c.OperatingCostMonthlyUSD != nil {
		fmt.Fprintf(w, "Y monthly     $%.2f\n", *c.OperatingCostMonthlyUSD)
	}
	for n, z := range c.ProjectedMonthlyZUSD {
		fmt.Fprintf(w, "Z monthly at %-16s $%.2f (X $%.2f)\n", n, z, c.ProjectedMonthlyXUSD[n])
	}
	if len(c.Missing) > 0 {
		fmt.Fprintf(w, "not computed  Y/Z need: %s\n", strings.Join(c.Missing, ", "))
	}
}
