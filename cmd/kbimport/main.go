package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
)

type options struct {
	team, from, docs, eval string
	check                  bool
}
type bundleIndex struct {
	Docs []struct {
		ID   string `json:"id"`
		Team string `json:"team"`
	} `json:"docs"`
}

var expectID = regexp.MustCompile(`^\s*expect_source_id:\s*"?([^"\r\n]+)"?\s*$`)

func main() {
	o := options{}
	flag.StringVar(&o.team, "team", "", "team name")
	flag.StringVar(&o.from, "from", "", "bundle kb directory")
	flag.StringVar(&o.docs, "docs", "docs", "docs directory")
	flag.StringVar(&o.eval, "eval", "eval", "eval directory")
	flag.BoolVar(&o.check, "check", false, "validate without writing")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "kbimport:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if !validTeam(o.team) {
		return fmt.Errorf("invalid -team %q", o.team)
	}
	if o.from == "" {
		return fmt.Errorf("-from is required")
	}
	from, err := filepath.Abs(o.from)
	if err != nil {
		return err
	}
	if _, err := os.Stat(from); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}
	docsParent, err := filepath.Abs(o.docs)
	if err != nil {
		return err
	}
	evalParent, err := filepath.Abs(o.eval)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(docsParent, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(evalParent, 0o755); err != nil {
		return err
	}
	stageDocs, err := os.MkdirTemp(docsParent, ".kbimport-docs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDocs)
	stageEval, err := os.MkdirTemp(evalParent, ".kbimport-eval-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageEval)
	if err := stageBundle(from, stageDocs, stageEval); err != nil {
		return err
	}
	if err := validateStage(stageDocs, stageEval, docsParent, o.team); err != nil {
		return err
	}
	if o.check {
		return nil
	}
	return replaceTeam(stageDocs, stageEval, filepath.Join(docsParent, o.team), filepath.Join(evalParent, o.team))
}

func validTeam(team string) bool {
	return team != "" && team != "." && team != ".." && !strings.ContainsAny(team, `/\`) && filepath.Base(team) == team
}

func stageBundle(from, docs, eval string) error {
	bundleRoot := filepath.Dir(from)
	return filepath.WalkDir(from, func(src string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(from, src)
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Ext(src), ".md") {
			return stageMarkdown(src, filepath.Join(docs, rel), docs, bundleRoot)
		}
		if strings.EqualFold(filepath.Ext(src), ".yaml") || filepath.Base(src) == "kb_index.json" {
			return copyFile(src, filepath.Join(eval, rel))
		}
		return nil
	})
}

func stageMarkdown(src, dst, docs, bundleRoot string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	meta := readMeta(string(b))
	if meta["team"] == "" {
		return fmt.Errorf("%s: missing team", src)
	}
	if !strings.HasPrefix(meta["id"], meta["team"]+"--") {
		return fmt.Errorf("%s: id must start with %s--", src, meta["team"])
	}
	for _, ref := range kb.ImageRefs(string(b)) {
		switch kb.ClassifyImageRef(ref) {
		case kb.ImageRefRejected:
			return fmt.Errorf("%s: rejected image reference %q", src, ref)
		case kb.ImageRefLocal:
			local := filepath.FromSlash(strings.ReplaceAll(ref, `\`, "/"))
			asset := filepath.Clean(filepath.Join(filepath.Dir(src), local))
			inside, err := within(bundleRoot, asset)
			if err != nil || !inside {
				return fmt.Errorf("%s: image escapes bundle %q", src, ref)
			}
			if _, err := os.Stat(asset); err != nil {
				return fmt.Errorf("%s: image %q: %w", src, ref, err)
			}
			assetRel, _ := filepath.Rel(bundleRoot, asset)
			staged := filepath.Join(docs, "_assets", assetRel)
			if err := copyFile(asset, staged); err != nil {
				return err
			}
			newRef, err := filepath.Rel(filepath.Dir(dst), staged)
			if err != nil {
				return err
			}
			b = []byte(strings.ReplaceAll(string(b), "("+ref+")", "("+filepath.ToSlash(newRef)+")"))
		}
	}
	return writeFile(dst, b)
}

func validateStage(docs, eval, existingDocs, team string) error {
	repo := kb.NewMarkdownRepo(docs, filepath.Join(docs, ".ignore"))
	secs, _, err := repo.Parse(context.Background())
	if err != nil {
		return err
	}
	mdFiles, err := markdownFiles(docs)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, s := range secs {
		seen[s.File()] = true
		if s.Meta()["team"] != team {
			return fmt.Errorf("%s: team %q does not match -team %q; rerun exporter with TEAM=%s", s.File(), s.Meta()["team"], team, team)
		}
		ids[s.Meta()["id"]] = true
	}
	for _, file := range mdFiles {
		if !seen[file] {
			return fmt.Errorf("%s: no sections", file)
		}
	}
	other, _, err := kb.NewMarkdownRepo(existingDocs, filepath.Join(docs, ".ignore")).Parse(context.Background())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	otherSections := make([]kb.Section, 0, len(other))
	seenOtherFiles := map[string]bool{}
	for _, section := range other {
		if !strings.HasPrefix(section.File(), team+"/") && !seenOtherFiles[section.File()] {
			seenOtherFiles[section.File()] = true
			otherSections = append(otherSections, section)
		}
	}
	if problems := kb.ValidateCorpus(append(otherSections, secs...)); len(problems) > 0 {
		return errors.Join(problems...)
	}
	return validateEvalIndex(eval, ids, team)
}

func validateEvalIndex(eval string, ids map[string]bool, team string) error {
	b, err := os.ReadFile(filepath.Join(eval, "kb_index.json"))
	if err != nil {
		return err
	}
	var index bundleIndex
	if err := json.Unmarshal(b, &index); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, row := range index.Docs {
		if row.Team != team || !ids[row.ID] {
			return fmt.Errorf("kb_index.json: invalid id/team %q/%q", row.ID, row.Team)
		}
		counts[row.ID]++
	}
	for id := range ids {
		if counts[id] != 1 {
			return fmt.Errorf("kb_index.json: id %q count = %d, want 1", id, counts[id])
		}
	}
	return filepath.WalkDir(eval, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "-eval.yaml") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if m := expectID.FindStringSubmatch(line); m != nil && !ids[m[1]] {
				return fmt.Errorf("%s: unknown expect_source_id %q", path, m[1])
			}
		}
		return nil
	})
}

// renameBackoff is the retry schedule for a directory rename. Windows briefly
// holds a handle on a just-written directory (Defender / Search Indexer scanning
// the freshly staged screenshots), so the swap-into-place rename fails with
// "Access is denied" on the first try and succeeds moments later — observed
// 5/5 first-try failures against docs/Store.POS. A short backoff clears it.
var renameBackoff = []time.Duration{
	20 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 250 * time.Millisecond,
}

// renameRetry is os.Rename with the backoff above. A rename that keeps failing
// past the schedule still returns its error, so a genuine permission problem is
// not masked, only a transient lock is ridden out.
func renameRetry(oldpath, newpath string) error {
	err := os.Rename(oldpath, newpath)
	for _, d := range renameBackoff {
		if err == nil {
			return nil
		}
		time.Sleep(d)
		err = os.Rename(oldpath, newpath)
	}
	return err
}

func replaceTeam(stageDocs, stageEval, docsTarget, evalTarget string) error {
	return replaceOne(stageDocs, docsTarget, func() error { return replaceOne(stageEval, evalTarget, func() error { return nil }) })
}
func replaceOne(stage, target string, next func() error) error {
	bak, err := os.MkdirTemp(filepath.Dir(target), ".kbimport-bak-")
	if err != nil {
		return err
	}
	if err := os.Remove(bak); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		if err := renameRetry(target, bak); err != nil {
			return err
		}
	}
	if err := renameRetry(stage, target); err != nil {
		_ = renameRetry(bak, target)
		return err
	}
	if err := next(); err != nil {
		_ = os.RemoveAll(target)
		_ = renameRetry(bak, target)
		return err
	}
	return os.RemoveAll(bak)
}
func readMeta(text string) map[string]string {
	meta, _ := kb.ParseFrontmatter(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"))
	return meta
}
func markdownFiles(root string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.EqualFold(filepath.Ext(p), ".md") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, err
}
func within(root, target string) (bool, error) {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), err
}
func copyFile(src, dst string) error {
	b, err := os.Open(src)
	if err != nil {
		return err
	}
	defer b.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, b)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
