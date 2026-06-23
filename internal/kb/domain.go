package kb

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

type Section struct {
	file    string
	heading string
	anchor  string
	body    string
}

func NewSection(file, heading, body string) (Section, error) {
	if file == "" || heading == "" {
		return Section{}, ErrInvalidSection
	}
	return Section{file: file, heading: heading, anchor: slugify(heading), body: body}, nil
}

func rehydrateSection(file, heading, anchor, body string) Section {
	return Section{file: file, heading: heading, anchor: anchor, body: body}
}

func (s Section) File() string    { return s.file }
func (s Section) Heading() string { return s.heading }
func (s Section) Anchor() string  { return s.anchor }
func (s Section) Body() string    { return s.body }

func (s Section) Citation() string {
	return s.file + "#" + s.anchor
}

var nonSlug = regexp.MustCompile(`[^a-z0-9 -]`)

func slugify(h string) string {
	s := strings.ToLower(strings.TrimSpace(h))
	s = nonSlug.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

var tokenSplit = regexp.MustCompile(`[^a-z0-9]+`)

func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := tokenSplit.Split(text, -1)
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
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
}

func NewAnswer(text string, sources []Citation, strategy string) Answer {
	return Answer{text: text, sources: sources, strategy: strategy}
}

func (a Answer) Text() string        { return a.text }
func (a Answer) Sources() []Citation { return a.sources }
func (a Answer) Strategy() string    { return a.strategy }

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
