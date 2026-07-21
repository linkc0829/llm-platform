package kb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
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
		if d.IsDir() {
			if path != r.docsDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
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

	fingerprint, err := r.fingerprint()
	if err != nil {
		return err
	}
	out := indexJSON{
		AnchorVersion:   anchorVersion,
		DocsFingerprint: fingerprint,
		Sections:        make([]sectionJSON, 0, len(sections)),
		Corpus:          toCorpusJSON(BuildCorpus(sections)),
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
	fingerprint, err := r.fingerprint()
	if err != nil || in.DocsFingerprint != fingerprint {
		return nil, ErrIndexStale
	}
	sections := make([]Section, 0, len(in.Sections))
	for _, section := range in.Sections {
		sections = append(sections, fromSectionJSON(section))
	}
	return sections, nil
}

func (r *MarkdownRepo) fingerprint() (string, error) {
	parts := make([]string, 0)
	err := filepath.WalkDir(r.docsDir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name != r.docsDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			return err
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(r.docsDir, name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(body)
		parts = append(parts, filepath.ToSlash(rel)+"\x00"+fmt.Sprintf("%x", hash))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint docs: %w", err)
	}
	sort.Strings(parts)
	hash := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	// ponytail: assets are checked at import time; only Markdown invalidates retrieval.
	return fmt.Sprintf("%x", hash), nil
}

func (r *MarkdownRepo) indexPath() string {
	return filepath.Join(r.indexDir, "index.json")
}

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
var metadataRE = regexp.MustCompile(`^\*\*([^*]+)\*\*:\s*(.+?)\s*$`)
var imageRE = regexp.MustCompile(`!\[[^]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)
var wpfReplayFileRE = regexp.MustCompile(`^.+__\d{2}_.+\.md$`)
var frontmatterRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):\s*(.*)$`)

func parseMarkdownFile(path, relName string) ([]Section, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read markdown: %w", err)
	}

	sections := make([]Section, 0)
	var heading string
	var body strings.Builder
	wpfFlow := wpfReplayFileRE.MatchString(filepath.Base(relName))

	lines := strings.Split(string(b), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	fileMeta, lines := ParseFrontmatter(lines)

	flush := func() error {
		if heading == "" {
			return nil
		}
		text := strings.TrimSpace(body.String())
		body.Reset()
		if text == "" {
			return nil
		}
		meta := mergeMeta(fileMeta, parseMetadata(text))
		images := imagePaths(text, relName)
		section, err := NewSection(relName, heading, text, meta, images)
		if err != nil {
			return err
		}
		sections = append(sections, section)
		return nil
	}

	for _, line := range lines {
		if match := headingRE.FindStringSubmatch(line); match != nil {
			if wpfFlow && heading != "" {
				body.WriteString(line)
				body.WriteByte('\n')
				continue
			}
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
	if err := flush(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseFrontmatter consumes a leading `---` block and returns its key/value pairs
// plus the remaining lines. KB-spec documents carry doc_type, access_level and the
// other required metadata there; an unterminated block is left as ordinary content.
func ParseFrontmatter(lines []string) (map[string]string, []string) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, lines
	}
	meta := map[string]string{}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return meta, lines[i+1:]
		}
		if match := frontmatterRE.FindStringSubmatch(lines[i]); match != nil {
			meta[match[1]] = strings.Trim(strings.TrimSpace(match[2]), `"`)
		}
	}
	return nil, lines
}

// mergeMeta overlays a section's own bold-key metadata on the file-level frontmatter.
func mergeMeta(fileMeta, bodyMeta map[string]string) map[string]string {
	if len(fileMeta) == 0 {
		return bodyMeta
	}
	merged := make(map[string]string, len(fileMeta)+len(bodyMeta))
	for k, v := range fileMeta {
		merged[k] = v
	}
	for k, v := range bodyMeta {
		merged[k] = v
	}
	return merged
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

type ImageRefKind int

const (
	ImageRefLocal ImageRefKind = iota
	ImageRefExternal
	ImageRefRejected
)

// ClassifyImageRef classifies a raw Markdown image reference before it is resolved.
func ClassifyImageRef(ref string) ImageRefKind {
	lower := strings.ToLower(ref)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ImageRefExternal
	}
	if strings.Contains(ref, "://") || filepath.IsAbs(ref) || strings.HasPrefix(ref, "/") ||
		strings.HasPrefix(ref, `\\`) || hasWindowsDrivePrefix(ref) {
		return ImageRefRejected
	}
	return ImageRefLocal
}

func hasWindowsDrivePrefix(ref string) bool {
	return len(ref) >= 2 && ((ref[0] >= 'a' && ref[0] <= 'z') || (ref[0] >= 'A' && ref[0] <= 'Z')) && ref[1] == ':'
}

// ImageRefs returns raw, un-normalized Markdown image references.
func ImageRefs(body string) []string {
	matches := imageRE.FindAllStringSubmatch(body, -1)
	images := make([]string, 0, len(matches))
	for _, match := range matches {
		images = append(images, match[1])
	}
	return images
}

func imagePaths(body, relName string) []string {
	refs := ImageRefs(body)
	images := make([]string, 0, len(refs))
	for _, ref := range refs {
		switch ClassifyImageRef(ref) {
		case ImageRefExternal:
			images = append(images, ref)
		case ImageRefLocal:
			resolved := path.Clean(path.Join(path.Dir(relName), slashRef(ref)))
			if resolved != ".." && !strings.HasPrefix(resolved, "../") {
				images = append(images, resolved)
			}
		}
	}
	return images
}

// slashRef normalizes Markdown paths consistently on every OS.
// ponytail: literal backslashes in Linux filenames are unsupported; bundle paths win.
func slashRef(ref string) string {
	return strings.ReplaceAll(ref, `\`, "/")
}
