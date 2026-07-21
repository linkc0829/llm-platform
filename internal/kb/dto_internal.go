package kb

type sectionJSON struct {
	File    string            `json:"file"`
	Heading string            `json:"heading"`
	Anchor  string            `json:"anchor"`
	Body    string            `json:"body"`
	Meta    map[string]string `json:"meta,omitempty"`
	Images  []string          `json:"images,omitempty"`
}

type indexJSON struct {
	AnchorVersion   int           `json:"anchor_version"`
	DocsFingerprint string        `json:"docs_fingerprint"`
	Sections        []sectionJSON `json:"sections"`
	Corpus          corpusJSON    `json:"corpus"`
}

type corpusJSON struct {
	DocFreq map[string]int `json:"doc_freq"`
	DocLen  []int          `json:"doc_len"`
	AvgLen  float64        `json:"avg_len"`
	N       int            `json:"n"`
}

type vectorMetaJSON struct {
	Model   string               `json:"model"`
	Vectors map[string][]float32 `json:"vectors"`
}

func toSectionJSON(s Section) sectionJSON {
	return sectionJSON{File: s.File(), Heading: s.Heading(), Anchor: s.Anchor(), Body: s.Body(), Meta: s.Meta(), Images: s.Images()}
}

func fromSectionJSON(j sectionJSON) Section {
	return rehydrateSection(j.File, j.Heading, j.Anchor, j.Body, j.Meta, j.Images)
}

func toCorpusJSON(c Corpus) corpusJSON {
	return corpusJSON{DocFreq: c.DocFreq, DocLen: c.DocLen, AvgLen: c.AvgLen, N: c.N}
}
