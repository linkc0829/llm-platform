package kb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type MarkdownRepo struct {
	docsDir  string
	indexDir string
}

func NewMarkdownRepo(docsDir, indexDir string) *MarkdownRepo {
	return &MarkdownRepo{docsDir: docsDir, indexDir: indexDir}
}

func (r *MarkdownRepo) Parse(ctx context.Context) ([]Section, int, error) {
	paths, err := filepath.Glob(filepath.Join(r.docsDir, "*.md"))
	if err != nil {
		return nil, 0, fmt.Errorf("glob docs: %w", err)
	}

	sections := make([]Section, 0)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		fileSections, err := parseMarkdownFile(path)
		if err != nil {
			return nil, 0, err
		}
		sections = append(sections, fileSections...)
	}
	return sections, len(paths), nil
}

func (r *MarkdownRepo) Save(ctx context.Context, sections []Section) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	out := indexJSON{
		AnchorVersion: anchorVersion,
		Sections:      make([]sectionJSON, 0, len(sections)),
		Corpus:        toCorpusJSON(BuildCorpus(sections)),
	}
	for _, section := range sections {
		out.Sections = append(out.Sections, toSectionJSON(section))
	}

	if err := os.MkdirAll(r.indexDir, 0o755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	if err := os.WriteFile(r.indexPath(), b, 0o600); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	return nil
}

func (r *MarkdownRepo) Load(ctx context.Context) ([]Section, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b, err := os.ReadFile(r.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotIndexed
	}
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}

	var in indexJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("unmarshal index: %w", err)
	}
	if in.AnchorVersion != anchorVersion {
		return nil, ErrIndexStale
	}
	sections := make([]Section, 0, len(in.Sections))
	for _, section := range in.Sections {
		sections = append(sections, fromSectionJSON(section))
	}
	return sections, nil
}

func (r *MarkdownRepo) indexPath() string {
	return filepath.Join(r.indexDir, "index.json")
}

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

func parseMarkdownFile(path string) ([]Section, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open markdown: %w", err)
	}
	defer f.Close()

	fileName := filepath.Base(path)
	sections := make([]Section, 0)
	var heading string
	var body strings.Builder

	flush := func() error {
		if heading == "" {
			return nil
		}
		section, err := NewSection(fileName, heading, strings.TrimSpace(body.String()))
		if err != nil {
			return err
		}
		sections = append(sections, section)
		body.Reset()
		return nil
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if match := headingRE.FindStringSubmatch(line); match != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			heading = strings.TrimSpace(match[2])
			continue
		}
		if heading != "" {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan markdown: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return sections, nil
}
