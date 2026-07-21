package kb

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
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

func (s Section) Citation() string {
	return s.file + "#" + s.anchor
}

// EvidenceClass reports what the section can prove for an answer.
func (s Section) EvidenceClass() string {
	if docType := strings.TrimSpace(s.meta["doc_type"]); docType != "" {
		return docType
	}

	file := strings.ToLower(s.file)
	if strings.HasSuffix(file, "-ui_inventory.md") {
		return "ui_inventory"
	}

	evidence := s.heading + "\n" + s.body
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

func procedureEvidenceClass(text string) string {
	switch {
	case strings.Contains(text, "**動作證據**:`recorded`"):
		return "procedure"
	case strings.Contains(text, "**動作證據**:`recorded_unlabeled`"):
		return "procedure_unlabeled"
	case strings.Contains(text, "**動作證據**:`inferred`") || strings.Contains(text, "**動作證據**:`not_attributable`"):
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
}

func NewAnswer(text string, sources []Citation, strategy string, images []string) Answer {
	return Answer{text: text, sources: sources, strategy: strategy, images: images}
}

func (a Answer) Text() string        { return a.text }
func (a Answer) Sources() []Citation { return a.sources }
func (a Answer) Strategy() string    { return a.strategy }

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

func RankVector(indexed []Section, vecMap map[string][]float32, query []float32, limit int) []ScoredSection {
	ranked := make([]ScoredSection, 0, len(indexed))
	for i, section := range indexed {
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
