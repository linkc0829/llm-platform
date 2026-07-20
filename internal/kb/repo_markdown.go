package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	sections := make([]Section, 0)
	files := 0
	err := filepath.WalkDir(r.docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(r.docsDir, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		fileSections, err := parseMarkdownFile(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		files++
		sections = append(sections, fileSections...)
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("walk docs: %w", err)
	}
	return sections, files, nil
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
var metadataRE = regexp.MustCompile(`^\*\*([^*]+)\*\*:\s*(.+?)\s*$`)
var imageRE = regexp.MustCompile(`!\[[^]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)

func parseMarkdownFile(path, relName string) ([]Section, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read markdown: %w", err)
	}

	sections := make([]Section, 0)
	var heading string
	var body strings.Builder
	wpfFlow := false

	flush := func() error {
		if heading == "" {
			return nil
		}
		text := strings.TrimSpace(body.String())
		if text == "" {
			return nil
		}
		meta := parseMetadata(text)
		images := imagePaths(text)
		section, err := NewSection(relName, heading, text, meta, images)
		if err != nil {
			return err
		}
		sections = append(sections, section)
		body.Reset()
		return nil
	}

	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if match := headingRE.FindStringSubmatch(line); match != nil {
			if wpfFlow {
				body.WriteString(line)
				body.WriteByte('\n')
				continue
			}
			if err := flush(); err != nil {
				return nil, err
			}
			heading = strings.TrimSpace(match[2])
			wpfFlow = strings.Contains(heading, "/")
			continue
		}
		if heading != "" {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return sections, nil
}

func parseMetadata(body string) map[string]string {
	meta := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		match := metadataRE.FindStringSubmatch(line)
		if match != nil {
			meta[match[1]] = match[2]
		}
	}
	return meta
}

func imagePaths(body string) []string {
	matches := imageRE.FindAllStringSubmatch(body, -1)
	images := make([]string, 0, len(matches))
	for _, match := range matches {
		images = append(images, match[1])
	}
	return images
}
