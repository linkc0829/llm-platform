package kb

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

var requiredMetadata = []string{"id", "team", "product", "doc_type", "version", "access_level", "owner", "last_reviewed"}

// ValidateCorpus reports missing required metadata and ids shared by different files.
func ValidateCorpus(sections []Section) []error {
	files := map[string]map[string]string{}
	ids := map[string]map[string]bool{}
	for _, section := range sections {
		if _, ok := files[section.File()]; !ok {
			files[section.File()] = section.Meta()
		}
		id := strings.TrimSpace(section.Meta()["id"])
		if id != "" {
			if ids[id] == nil {
				ids[id] = map[string]bool{}
			}
			ids[id][section.File()] = true
		}
	}
	problems := make([]error, 0)
	for file, meta := range files {
		for _, key := range requiredMetadata {
			if strings.TrimSpace(meta[key]) == "" {
				problems = append(problems, fmt.Errorf("%s: missing %s", file, key))
			}
		}
	}
	for id, files := range ids {
		if len(files) > 1 {
			problems = append(problems, fmt.Errorf("id %q is used by multiple files", id))
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return problems
}

// anchorVersion versions persisted section representation, including anchors and image paths.
const anchorVersion = 2

type Section struct {
	file    string
	heading string
	anchor  string
	body    string
	meta    map[string]string
	images  []string
	tier    SectionTier
}

// SectionTier is the retrieval visibility tier assigned during index audit.
// The zero value is invalid so an unclassified section cannot be treated as
// public by accident.
type SectionTier uint8

const (
	SectionTierInvalid SectionTier = iota
	SectionTierPublic
	SectionTierRestricted
)

var endpointRE = regexp.MustCompile(`(?m)\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE)\s+/[^\s)\]>,]+`)

var engineeringMarkers = []string{
	"**Endpoint Index**",
	"**Verified Services/APIs**",
	"**Verified Call Chain**",
}

// ClassifySection assigns a public or engineering-only tier and rejects
// exporter drift that places engineering evidence under an operation heading.
func ClassifySection(section Section) (SectionTier, error) {
	headingRestricted := strings.Contains(section.Heading(), "工程對應")
	contentRestricted := containsEngineeringEvidence(section.Body())
	docType := strings.TrimSpace(section.Meta()["doc_type"])
	if docType != "engineering_reference" && contentRestricted && !headingRestricted {
		return SectionTierInvalid, fmt.Errorf("%w: %s", ErrSectionAccessDrift, section.Citation())
	}

	switch docType {
	case "engineering_reference":
		if strings.TrimSpace(section.Meta()["access_level"]) != "internal-engineering" {
			return SectionTierInvalid, fmt.Errorf("%w: %s engineering_reference must use internal-engineering", ErrInvalidSectionAccess, section.Citation())
		}
		return SectionTierRestricted, nil
	case "ui_inventory", "playlist", "reference", "distilled":
		if headingRestricted {
			return SectionTierInvalid, fmt.Errorf("%w: %s has engineering heading for %q", ErrInvalidSectionAccess, section.Citation(), section.Meta()["doc_type"])
		}
		return SectionTierPublic, nil
	case "procedure":
		if headingRestricted {
			return SectionTierRestricted, nil
		}
		return SectionTierPublic, nil
	default:
		return SectionTierInvalid, fmt.Errorf("%w: %s has unknown doc_type %q", ErrInvalidSectionAccess, section.Citation(), section.Meta()["doc_type"])
	}
}

func containsEngineeringEvidence(body string) bool {
	for _, marker := range engineeringMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return endpointRE.MatchString(body)
}

// AuditSections validates every parsed section before it can be persisted or
// embedded. It returns all section errors in deterministic input order.
func AuditSections(sections []Section) error {
	var problems []error
	for _, section := range sections {
		if _, err := ClassifySection(section); err != nil {
			problems = append(problems, err)
		}
	}

	// Build lookup map for reference validation: (sourceID, anchor) -> count
	type targetKey struct {
		sourceID string
		anchor   string
	}
	counts := make(map[targetKey]int, len(sections))
	for _, s := range sections {
		docType := strings.TrimSpace(s.meta["doc_type"])
		if docType != "distilled" {
			sourceID := strings.TrimSpace(s.meta["id"])
			if sourceID != "" && s.anchor != "" {
				counts[targetKey{sourceID: sourceID, anchor: s.anchor}]++
			}
		}
	}

	for _, s := range sections {
		if strings.TrimSpace(s.meta["doc_type"]) != "distilled" {
			continue
		}
		derivedFrom := strings.TrimSpace(s.meta["derived_from"])
		if derivedFrom == "" {
			problems = append(problems, fmt.Errorf("%w: %s missing derived_from", ErrDistilledReferenceInvalid, s.Citation()))
			continue
		}
		refs := ExtractL0References(s.body)
		if len(refs) == 0 {
			problems = append(problems, fmt.Errorf("%w: %s has no L0 references", ErrDistilledReferenceInvalid, s.Citation()))
			continue
		}
		for _, ref := range refs {
			key := targetKey{sourceID: derivedFrom, anchor: ref.Anchor}
			c := counts[key]
			switch {
			case c == 0:
				problems = append(problems, fmt.Errorf("%w: %s reference to %s#%s not found", ErrDistilledReferenceNotFound, s.Citation(), derivedFrom, ref.Anchor))
			case c > 1:
				problems = append(problems, fmt.Errorf("%w: %s reference to %s#%s matches %d sections", ErrDistilledReferenceInvalid, s.Citation(), derivedFrom, ref.Anchor, c))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return errors.Join(problems...)
}

var l0RefRE = regexp.MustCompile(`\[L0:\s*#([^|\s]+)\s*\|\s*evidence:\s*([^\]\s]+)\]`)

type L0Reference struct {
	Anchor   string
	Evidence string
}

func ExtractL0References(body string) []L0Reference {
	matches := l0RefRE.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]L0Reference, 0, len(matches))
	for _, m := range matches {
		refs = append(refs, L0Reference{Anchor: m[1], Evidence: m[2]})
	}
	return refs
}

func filterRankedSections(indexed []Section, ranked []ScoredSection, allow func(Section) bool) []ScoredSection {
	filtered := make([]ScoredSection, 0, len(ranked))
	for _, scored := range ranked {
		if scored.Index < 0 || scored.Index >= len(indexed) {
			continue
		}
		if allow != nil && !allow(indexed[scored.Index]) {
			continue
		}
		filtered = append(filtered, scored)
	}
	return filtered
}

func topSections(indexed []Section, ranked []ScoredSection, k int) []Section {
	if k > len(ranked) {
		k = len(ranked)
	}
	sections := make([]Section, 0, k)
	for _, scored := range ranked[:k] {
		if scored.Score <= 0 {
			continue
		}
		if scored.Index >= 0 && scored.Index < len(indexed) {
			sections = append(sections, indexed[scored.Index])
		}
	}
	return sections
}

// PoolResult contains the partitioned and fused candidates ready for expansion.
type PoolResult struct {
	TopL1      []Section
	TopL0      []Section
	BML0       []ScoredSection
	VecL0      []ScoredSection
	RankedL0   []ScoredSection
	RankedL1   []ScoredSection
	GateBM25   float64
	GateCosine float64
	Strategy   string
}

// PartitionAndRankPools separates candidates by doc_type ("distilled" vs non-distilled),
// computes deny gate metrics strictly from the L0 pool, fuses each pool independently via RRF,
// and selects topL1 and topL0 with dynamic borrowing when L1 is empty.
// PartitionAndRankPools separates candidates by doc_type ("distilled" vs non-distilled),
// computes deny gate metrics strictly from the L0 pool, fuses each pool independently via RRF,
// and selects topL1 and topL0 with dynamic borrowing when L1 is empty.
func PartitionAndRankPools(
	indexed []Section,
	bm25List []ScoredSection,
	vecList []ScoredSection,
	topK int,
	candidateK int,
	rrfK int,
	minThreshold float64,
) PoolResult {
	// 1. Partition BM25 into L1 and L0 before candidateK truncation.
	var bmL1, bmL0 []ScoredSection
	for _, item := range bm25List {
		if indexed[item.Index].Meta()["doc_type"] == "distilled" {
			bmL1 = append(bmL1, item)
		} else {
			bmL0 = append(bmL0, item)
		}
	}
	gateBM25 := 0.0
	if len(bmL0) > 0 {
		gateBM25 = bmL0[0].Score
	}
	if len(bmL1) > candidateK {
		bmL1 = bmL1[:candidateK]
	}
	if len(bmL0) > candidateK {
		bmL0 = bmL0[:candidateK]
	}

	// 2. Partition Vector into L1 and L0 before candidateK truncation.
	var vecL1, vecL0 []ScoredSection
	for _, item := range vecList {
		if indexed[item.Index].Meta()["doc_type"] == "distilled" {
			vecL1 = append(vecL1, item)
		} else {
			vecL0 = append(vecL0, item)
		}
	}
	gateCosine := 0.0
	if len(vecL0) > 0 {
		gateCosine = vecL0[0].Score
	}
	if len(vecL1) > candidateK {
		vecL1 = vecL1[:candidateK]
	}
	if len(vecL0) > candidateK {
		vecL0 = vecL0[:candidateK]
	}

	// 3. Fuse each pool independently.
	var rankedL1, rankedL0 []ScoredSection
	strategyL1, strategyL0 := "none", "markdown"

	if len(vecL1) > 0 && len(bmL1) > 0 {
		rankedL1, strategyL1 = FuseRRF([][]ScoredSection{bmL1, vecL1}, rrfK), "hybrid"
	} else if len(vecL1) > 0 {
		rankedL1, strategyL1 = vecL1, "vector"
	} else if len(bmL1) > 0 {
		rankedL1, strategyL1 = bmL1, "markdown"
	}

	if len(vecL0) > 0 && len(bmL0) > 0 {
		rankedL0, strategyL0 = FuseRRF([][]ScoredSection{bmL0, vecL0}, rrfK), "hybrid"
	} else if len(vecL0) > 0 {
		rankedL0, strategyL0 = vecL0, "vector"
	} else if len(bmL0) > 0 {
		rankedL0, strategyL0 = bmL0, "markdown"
	}

	strategy := strategyL0
	if strategyL1 == "hybrid" || strategyL0 == "hybrid" {
		strategy = "hybrid"
	} else if strategyL0 == "vector" || strategyL1 == "vector" {
		strategy = "vector"
	}

	// 4. Select top candidates with dynamic borrowing when L1 has no qualified matches.
	// An L1 candidate qualifies for the distilled pool if it has a meaningful lexical match (BM25 >= minThreshold)
	// or its vector score is competitive with L0.
	// Genuine vector hits reach 0.93~1.00 of gateCosine across measured probe queries,
	// while misses fall below 0.62. The 0.85 ratio cleanly filters out semantic drift.
	// Per-module expansion eligibility (Tier 1 / Tier 2 vs Tier 3) and quota return are
	// resolved by ExpandDistilled using canonical L0 references.
	var validRankedL1 []ScoredSection
	for _, item := range rankedL1 {
		idx := item.Index
		bmScore := 0.0
		for _, b := range bmL1 {
			if b.Index == idx {
				bmScore = b.Score
				break
			}
		}
		vecScore := 0.0
		for _, v := range vecL1 {
			if v.Index == idx {
				vecScore = v.Score
				break
			}
		}
		qualified := false
		if bmScore >= minThreshold {
			qualified = true
		}
		if gateCosine > 0 && vecScore >= gateCosine*0.85 {
			qualified = true
		} else if gateCosine <= 0 && vecScore > 0.30 {
			qualified = true
		}
		if qualified {
			validRankedL1 = append(validRankedL1, item)
		}
	}

	topL1 := topSections(indexed, validRankedL1, len(validRankedL1))
	topL0 := topSections(indexed, rankedL0, topK)

	return PoolResult{
		TopL1:      topL1,
		TopL0:      topL0,
		BML0:       bmL0,
		VecL0:      vecL0,
		RankedL0:   rankedL0,
		RankedL1:   validRankedL1,
		GateBM25:   gateBM25,
		GateCosine: gateCosine,
		Strategy:   strategy,
	}
}

const (
	defaultL1K             = 2
	defaultCandidateK      = 20
	defaultExpansionBudget = 12
)

// ExpandDistilled replaces any distilled sections in topL1 with their expanded L0 sections,
// ensuring that L1 sections never reach the LLM context. Candidate steps within each L1 module
// are ranked by their fused RRF rank in rankedL0 (domain: [1, len(rankedL0)] where len <= 2*candidateK).
func ExpandDistilled(
	topL1 []Section,
	topL0 []Section,
	indexed []Section,
	rankedL0 []ScoredSection,
	canSee func(Section) bool,
	l1K int,
	candidateK int,
	budget int,
) ([]Section, error) {
	if l1K <= 0 {
		l1K = defaultL1K
	}
	if candidateK <= 0 {
		candidateK = defaultCandidateK
	}
	if budget <= 0 {
		budget = defaultExpansionBudget
	}

	type targetKey struct {
		sourceID string
		anchor   string
	}
	sectionIndex := make(map[targetKey]int, len(indexed))
	for idx, s := range indexed {
		if s.Meta()["doc_type"] != "distilled" {
			id := strings.TrimSpace(s.Meta()["id"])
			if id != "" && s.Anchor() != "" {
				key := targetKey{sourceID: id, anchor: s.Anchor()}
				if _, dup := sectionIndex[key]; !dup {
					sectionIndex[key] = idx
				}
			}
		}
	}

	l0Rank := make(map[int]int, len(rankedL0))
	for rank, item := range rankedL0 {
		l0Rank[item.Index] = rank + 1
	}

	var guarantees []Section
	var expansionQueues [][]Section
	activeModules := 0

	for _, l1 := range topL1 {
		if l1.Meta()["doc_type"] != "distilled" {
			continue
		}
		derivedFrom := strings.TrimSpace(l1.Meta()["derived_from"])
		if derivedFrom == "" {
			return nil, fmt.Errorf("%w: %s missing derived_from", ErrDistilledReferenceInvalid, l1.Citation())
		}
		refs := ExtractL0References(l1.Body())
		if len(refs) == 0 {
			return nil, fmt.Errorf("%w: %s has no L0 references", ErrDistilledReferenceInvalid, l1.Citation())
		}

		type stepCandidate struct {
			section Section
			rank    int
		}

		var candidates []stepCandidate
		for _, ref := range refs {
			idx, ok := sectionIndex[targetKey{sourceID: derivedFrom, anchor: ref.Anchor}]
			if !ok {
				return nil, fmt.Errorf("%w: %s reference to %s#%s not found", ErrDistilledReferenceNotFound, l1.Citation(), derivedFrom, ref.Anchor)
			}
			sec := indexed[idx]
			if canSee != nil && !canSee(sec) {
				continue
			}
			r, ok := l0Rank[idx]
			if !ok {
				r = math.MaxInt
			}
			candidates = append(candidates, stepCandidate{section: sec, rank: r})
		}

		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].rank == candidates[j].rank {
				return candidates[i].section.Citation() < candidates[j].section.Citation()
			}
			return candidates[i].rank < candidates[j].rank
		})

		if len(candidates) == 0 {
			continue
		}

		// When rankedL0 is empty (e.g. unit tests without full corpus), treat all steps as candidates.
		if len(rankedL0) == 0 {
			guarantees = append(guarantees, candidates[0].section)
			var queue []Section
			for _, c := range candidates[1:] {
				queue = append(queue, c.section)
			}
			if len(queue) > 0 {
				expansionQueues = append(expansionQueues, queue)
			}
			activeModules++
			if activeModules >= l1K {
				break
			}
			continue
		}

		// A module's best step is a valid guarantee if it was retrieved in rankedL0
		// (i.e. was in the top-20 of BM25 or Vector across the corpus, rank <= len(rankedL0)).
		// If candidates[0].rank > len(rankedL0) (math.MaxInt), no step in this module
		// qualified as a corpus-level candidate (Tier 3), so 0 guarantees and 0 expansion are given.
		// Such a module does not count towards active L1 quota (dynamic borrowing per module),
		// allowing subsequently ranked L1 candidates that do have genuine candidate steps to be evaluated.
		if candidates[0].rank <= len(rankedL0) {
			guarantees = append(guarantees, candidates[0].section)
			activeModules++

			// Round-robin expansion is only granted to modules whose best step is
			// in the top-tier of candidates (rank <= candidateK = 20, the upper half of rankedL0).
			// Secondary candidates (candidateK < rank <= len(rankedL0)) receive only 1 guarantee,
			// strictly preventing weak L1 matches from crowding out topL0 candidates.
			if candidates[0].rank <= candidateK {
				var queue []Section
				for _, c := range candidates[1:] {
					if c.rank <= len(rankedL0) {
						queue = append(queue, c.section)
					}
				}
				if len(queue) > 0 {
					expansionQueues = append(expansionQueues, queue)
				}
			}

			if activeModules >= l1K {
				break
			}
		}
	}

	seen := make(map[string]bool)
	result := make([]Section, 0, budget)

	// Layer 1: Guarantees (1 per hit L1 module)
	for _, sec := range guarantees {
		cite := sec.Citation()
		if !seen[cite] && len(result) < budget {
			seen[cite] = true
			result = append(result, sec)
		}
	}

	// Layer 2: Expansion candidates round-robin
	// Reserve room so topL0 is never starved of its quota.
	maxExpansion := budget - len(result) - len(topL0)
	if maxExpansion < 0 {
		maxExpansion = 0
	}
	expansionAdded := 0

	for expansionAdded < maxExpansion {
		addedAny := false
		for i := 0; i < len(expansionQueues); i++ {
			if len(expansionQueues[i]) == 0 {
				continue
			}
			item := expansionQueues[i][0]
			expansionQueues[i] = expansionQueues[i][1:]
			addedAny = true

			cite := item.Citation()
			if !seen[cite] && len(result) < budget && expansionAdded < maxExpansion {
				seen[cite] = true
				result = append(result, item)
				expansionAdded++
			}
			if len(result) >= budget || expansionAdded >= maxExpansion {
				break
			}
		}
		if !addedAny || len(result) >= budget || expansionAdded >= maxExpansion {
			break
		}
	}

	// Layer 3: L0 pool sections
	for _, sec := range topL0 {
		cite := sec.Citation()
		if !seen[cite] && len(result) < budget {
			seen[cite] = true
			result = append(result, sec)
		}
		if len(result) >= budget {
			break
		}
	}

	for _, sec := range result {
		if sec.Meta()["doc_type"] == "distilled" {
			return nil, fmt.Errorf("distilled section %s leaked into expanded context", sec.Citation())
		}
	}

	return result, nil
}

// CanSee applies the independent team and content-tier dimensions. A missing
// or invalid principal is never granted visibility.
func CanSee(principal shared.Principal, section Section) bool {
	if strings.TrimSpace(principal.ID) == "" {
		return false
	}
	if !principal.AllTeams && !containsTeam(principal.Teams, section.Meta()["team"]) {
		return false
	}
	// Read the tier StampTiers assigned, do not re-derive it. ClassifySection
	// regex-scans the body, and this ran ~2790 times per query (once per section
	// in filterRankedSections, again inside RankVector) to reproduce a value
	// AuditSections had already proved at index time — 28.6ms of the 38.5ms of
	// local CPU a query spent, measured in BenchmarkCanSeeAll and
	// BenchmarkRankVector. A section that never reached StampTiers keeps the
	// zero value, SectionTierInvalid, and is therefore invisible rather than
	// public.
	return section.tier == SectionTierPublic ||
		section.tier == SectionTierRestricted && principal.Engineering
}

// StampTiers records each section's visibility tier so queries can read it
// instead of deriving it.
//
// Sections that fail classification are left at SectionTierInvalid rather than
// reported: AuditSections is the fail-loud gate and it runs first on every path
// that indexes. This one runs later, at the point the snapshot is taken, so
// there is no way to publish sections that were never stamped.
func StampTiers(sections []Section) {
	for i := range sections {
		if tier, err := ClassifySection(sections[i]); err == nil {
			sections[i].tier = tier
		}
	}
}

func containsTeam(teams []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, team := range teams {
		if strings.TrimSpace(team) == want {
			return true
		}
	}
	return false
}

// FullAccessPrincipal is used only by explicitly local transports whose
// process/network boundary is already trusted.
func FullAccessPrincipal(id string) shared.Principal {
	return shared.Principal{ID: id, AllTeams: true, Engineering: true}
}

func NewSection(file, heading, body string, meta map[string]string, images []string) (Section, error) {
	if file == "" || heading == "" {
		return Section{}, ErrInvalidSection
	}
	return Section{file: file, heading: heading, anchor: slugify(heading), body: body, meta: meta, images: images}, nil
}

func rehydrateSection(file, heading, anchor, body string, meta map[string]string, images []string) Section {
	return Section{file: file, heading: heading, anchor: anchor, body: body, meta: meta, images: images}
}

func (s Section) File() string            { return s.file }
func (s Section) Heading() string         { return s.heading }
func (s Section) Anchor() string          { return s.anchor }
func (s Section) Body() string            { return s.body }
func (s Section) Meta() map[string]string { return s.meta }
func (s Section) Images() []string        { return s.images }

// Tier is the visibility tier StampTiers assigned; the zero value is
// SectionTierInvalid, so an unstamped section is never visible.
func (s Section) Tier() SectionTier { return s.tier }

func (s Section) Citation() string {
	return s.file + "#" + s.anchor
}

// EvidenceClass reports what the section can prove for an answer.
func (s Section) EvidenceClass() string {
	evidence := s.heading + "\n" + s.body
	switch strings.TrimSpace(s.meta["doc_type"]) {
	case "engineering_reference":
		return "engineering_reference"
	case "ui_inventory":
		return "ui_inventory"
	case "procedure":
		return procedureEvidenceClass(evidence)
	case "distilled":
		return "distilled"
	case "":
	default:
		return "general"
	}

	file := strings.ToLower(s.file)
	if strings.HasSuffix(file, "-ui_inventory.md") {
		return "ui_inventory"
	}

	if strings.HasSuffix(file, "-procedure.md") {
		return procedureEvidenceClass(evidence)
	}

	if strings.Contains(evidence, "**When**") || strings.Contains(evidence, "**Given**") ||
		strings.HasPrefix(s.heading, "步驟") || hasMarkdownHeading(s.body, "步驟") {
		return "procedure"
	}
	if s.heading == "按鈕" || s.heading == "輸入欄位" || s.heading == "截圖" ||
		hasMarkdownHeading(s.body, "按鈕") || hasMarkdownHeading(s.body, "輸入欄位") || hasMarkdownHeading(s.body, "截圖") {
		return "ui_inventory"
	}
	return "general"
}

var evidenceReplacer = strings.NewReplacer(
	"**", "",
	"：", ":",
)

func procedureEvidenceClass(text string) string {
	// Exported procedure sections contain at most one action-evidence marker.
	norm := evidenceReplacer.Replace(text)
	switch {
	case strings.Contains(norm, "動作證據:`recorded`"):
		return "procedure"
	case strings.Contains(norm, "動作證據:`vision_inferred`"):
		return "procedure_visual"
	case strings.Contains(norm, "動作證據:`recorded_unlabeled`"):
		return "procedure_unlabeled"
	case strings.Contains(norm, "動作證據:`inferred`") || strings.Contains(norm, "動作證據:`not_attributable`"):
		return "procedure_inferred"
	default:
		return "general"
	}
}

func hasMarkdownHeading(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line == want || want == "步驟" && strings.HasPrefix(line, want) {
			return true
		}
	}
	return false
}

func slugify(h string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.TrimSpace(h) {
		switch {
		case isASCIIAlnum(r):
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		case isCJK(r):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func tokenize(text string) []string {
	runes := []rune(text)
	out := make([]string, 0, len(runes))
	for i := 0; i < len(runes); {
		switch {
		case isASCIIAlnum(runes[i]):
			j := i
			for j < len(runes) && isASCIIAlnum(runes[j]) {
				j++
			}
			out = append(out, strings.ToLower(string(runes[i:j])))
			i = j
		case isCJK(runes[i]):
			j := i
			for j < len(runes) && isCJK(runes[j]) {
				j++
			}
			out = append(out, bigrams(runes[i:j])...)
			i = j
		default:
			i++
		}
	}
	return out
}

func isASCIIAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func bigrams(run []rune) []string {
	if len(run) == 1 {
		return []string{string(run)}
	}
	out := make([]string, 0, len(run)-1)
	for i := 0; i+1 < len(run); i++ {
		out = append(out, string(run[i:i+2]))
	}
	return out
}

type Citation struct {
	file   string
	anchor string
}

func NewCitation(file, anchor string) Citation {
	return Citation{file: file, anchor: anchor}
}

func (c Citation) String() string { return c.file + "#" + c.anchor }

type Answer struct {
	text     string
	sources  []Citation
	strategy string
	images   []string
	grounded bool
}

func NewAnswer(text string, sources []Citation, strategy string, images []string, grounded bool) Answer {
	return Answer{text: text, sources: sources, strategy: strategy, images: images, grounded: grounded}
}

func (a Answer) Text() string        { return a.text }
func (a Answer) Sources() []Citation { return a.sources }
func (a Answer) Strategy() string    { return a.strategy }

// Grounded reports whether the answer is backed by the knowledge base. It is
// false when retrieval fell below threshold (deny) and when the model, though
// given context, declined to answer — so a caller can branch on it without
// parsing the answer text. See the [ungroundedSentinel] contract.
func (a Answer) Grounded() bool { return a.grounded }

// Images are the screenshot paths of the cited sections, in citation order.
func (a Answer) Images() []string { return a.images }

type Turn struct {
	Query  string
	Answer string
}

type Corpus struct {
	DocTokens [][]string
	DocFreq   map[string]int
	DocLen    []int
	AvgLen    float64
	N         int
}

func BuildCorpus(sections []Section) Corpus {
	c := Corpus{
		DocTokens: make([][]string, 0, len(sections)),
		DocFreq:   map[string]int{},
		DocLen:    make([]int, 0, len(sections)),
		N:         len(sections),
	}

	totalLen := 0
	for _, section := range sections {
		tokens := tokenize(section.Heading() + " " + section.Body())
		c.DocTokens = append(c.DocTokens, tokens)
		c.DocLen = append(c.DocLen, len(tokens))
		totalLen += len(tokens)

		seen := map[string]bool{}
		for _, token := range tokens {
			if !seen[token] {
				c.DocFreq[token]++
				seen[token] = true
			}
		}
	}
	if c.N > 0 {
		c.AvgLen = float64(totalLen) / float64(c.N)
	}
	return c
}

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

func (c Corpus) BM25Score(docIdx int, queryTokens []string) float64 {
	if docIdx < 0 || docIdx >= len(c.DocTokens) || c.AvgLen == 0 {
		return 0
	}

	score := 0.0
	tf := map[string]int{}
	for _, t := range c.DocTokens[docIdx] {
		tf[t]++
	}
	for _, q := range queryTokens {
		n := c.DocFreq[q]
		if n == 0 {
			continue
		}
		idf := math.Log(1 + (float64(c.N)-float64(n)+0.5)/(float64(n)+0.5))
		f := float64(tf[q])
		denom := f + bm25K1*(1-bm25B+bm25B*float64(c.DocLen[docIdx])/c.AvgLen)
		score += idf * (f * (bm25K1 + 1)) / denom
	}
	return score
}

func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

type ScoredSection struct {
	Index int
	Score float64
}

func (c Corpus) RankBM25(queryTokens []string) []ScoredSection {
	ranked := make([]ScoredSection, 0, c.N)
	for i := range c.DocTokens {
		ranked = append(ranked, ScoredSection{Index: i, Score: c.BM25Score(i, queryTokens)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Index < ranked[j].Index
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked
}

func RankVector(indexed []Section, vecMap map[string][]float32, query []float32, limit int, allow func(Section) bool) []ScoredSection {
	ranked := make([]ScoredSection, 0, len(indexed))
	for i, section := range indexed {
		if allow != nil && !allow(section) {
			continue
		}
		score := Cosine(query, vecMap[section.Citation()])
		if score > 0 {
			ranked = append(ranked, ScoredSection{Index: i, Score: score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Index < ranked[j].Index
		}
		return ranked[i].Score > ranked[j].Score
	})
	if limit < len(ranked) {
		ranked = ranked[:limit]
	}
	return ranked
}

// FuseRRF combines ranked lists by reciprocal rank fusion.
//
// Measured 2026-08-21, and worth knowing before touching rrfK or blaming
// chunking for an engineering question that will not retrieve: at rrfK=60 over
// candidateK=20, being in a second list beats any rank inside one list.
//
//	best a one-channel section can score : 1/(60+1)          = 0.0164
//	worst a two-channel section can score : 1/80 + 1/80      = 0.0250
//
// So the fused list is really two tiers — everything both channels returned,
// then everything else — and rank inside a channel decides nothing across that
// line. rrfK would have to drop below 18 for a channel's top hit to outrank a
// section both channels ranked last.
//
// This bites engineering questions specifically. "which API does X call" is
// answered by a 工程對應 section holding endpoint paths and symbol names, which
// shares almost no tokens with the question, so BM25 never returns it and the
// vector channel is the only one that can. Four such questions were measured
// with the vector channel ranking the answer 1, 2, 5 and 8 while fusion placed
// it at 12, 10, 18 and 13.
//
// Two fixes were tried and both cost more than they returned: scoring a missing
// channel as one place past its window fixed the tiering but reordered every
// query and lost 5 stable support answers (315/317 -> 310/317), and widening
// topK to 12 gained one engineering answer while a fifth question, whose
// evidence was already inside the window at rank 8, started refusing because of
// the four extra sections — reproducibly, across two runs. The durable fix is
// upstream: give 工程對應 sections headings and prose BM25 can match, so the
// section reaches both channels and this tiering never applies to it.
func FuseRRF(lists [][]ScoredSection, rrfK int) []ScoredSection {
	acc := map[int]float64{}
	for _, list := range lists {
		for rank, scored := range list {
			acc[scored.Index] += 1 / float64(rrfK+rank+1)
		}
	}
	fused := make([]ScoredSection, 0, len(acc))
	for index, score := range acc {
		fused = append(fused, ScoredSection{Index: index, Score: score})
	}
	sort.Slice(fused, func(i, j int) bool {
		if fused[i].Score == fused[j].Score {
			return fused[i].Index < fused[j].Index
		}
		return fused[i].Score > fused[j].Score
	})
	return fused
}
