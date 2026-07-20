package kb

type sectionJSON struct {
	File    string `json:"file"`
	Heading string `json:"heading"`
	Anchor  string `json:"anchor"`
	Body    string `json:"body"`
}

type indexJSON struct {
	AnchorVersion int           `json:"anchor_version"`
	Sections      []sectionJSON `json:"sections"`
	Corpus        corpusJSON    `json:"corpus"`
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
	return sectionJSON{File: s.File(), Heading: s.Heading(), Anchor: s.Anchor(), Body: s.Body()}
}

func fromSectionJSON(j sectionJSON) Section {
	return rehydrateSection(j.File, j.Heading, j.Anchor, j.Body)
}

func toCorpusJSON(c Corpus) corpusJSON {
	return corpusJSON{DocFreq: c.DocFreq, DocLen: c.DocLen, AvgLen: c.AvgLen, N: c.N}
}
