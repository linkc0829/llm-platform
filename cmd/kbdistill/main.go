package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/kb"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/atomicfile"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(val string) error {
	*s = append(*s, strings.TrimSpace(val))
	return nil
}

type options struct {
	team    string
	bundle  string
	check   bool
	modules stringSliceFlag
}

type manifestDoc struct {
	ID      string `json:"id"`
	DocType string `json:"doc_type"`
	Path    string `json:"path"`
	Team    string `json:"team"`
	Product string `json:"product"`
	Version string `json:"version"`
}

type manifestIndex struct {
	Docs []manifestDoc `json:"docs"`
}

type stepInfo struct {
	num         int
	action      string
	anchor      string
	evidence    string
	when        string
	then        string
	evidenceCls string
}

type generatedFile struct {
	path    string
	content []byte
}

type moduleDistillData struct {
	sourceID     string
	pageCode     string
	moduleCode   string
	flowName     string
	team         string
	product      string
	version      string
	accessLevel  string
	owner        string
	lastReviewed string
	steps        []stepInfo
}

var stepHeadingRE = regexp.MustCompile(`— 步驟\s+(\d+):(.+)$`)

func main() {
	o := options{}
	flag.StringVar(&o.team, "team", "", "team name (e.g. Store.POS)")
	flag.StringVar(&o.bundle, "bundle", "", "bundle kb directory containing procedures/ and kb_index.json")
	flag.BoolVar(&o.check, "check", false, "validate without writing files")
	flag.Var(&o.modules, "module", "optional module filter (page/module, e.g. POS_Ordering/Combo_Ordering), can be repeated")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "kbdistill:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if o.team == "" {
		return errors.New("-team is required")
	}
	if o.bundle == "" {
		return errors.New("-bundle is required")
	}
	bundleDir, err := filepath.Abs(o.bundle)
	if err != nil {
		return err
	}
	if _, err := os.Stat(bundleDir); err != nil {
		return fmt.Errorf("bundle directory: %w", err)
	}

	manifestPath := filepath.Join(bundleDir, "kb_index.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read kb_index.json: %w", err)
	}
	var manifest manifestIndex
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("unmarshal kb_index.json: %w", err)
	}

	repo := kb.NewMarkdownRepo(bundleDir, filepath.Join(bundleDir, ".ignore"))
	sections, _, err := repo.Parse(context.Background())
	if err != nil {
		return fmt.Errorf("parse bundle: %w", err)
	}

	targetModules := make(map[string]bool)
	for _, m := range o.modules {
		if m != "" {
			targetModules[filepath.ToSlash(m)] = true
		}
	}

	distillModules, err := extractDistillData(sections, o.team, targetModules)
	if err != nil {
		return err
	}
	if len(distillModules) == 0 {
		return errors.New("no matching procedure modules found to distill")
	}

	distilledDir := filepath.Join(bundleDir, "distilled")

	// Plan actions: generated files and manifest updates
	var toWrite []generatedFile
	newManifestDocs := make([]manifestDoc, 0, len(distillModules))

	for _, mod := range distillModules {
		fileName := fmt.Sprintf("%s-%s-distilled.md", mod.pageCode, mod.moduleCode)
		filePath := filepath.Join(distilledDir, fileName)
		content := renderDistilledMarkdown(mod)
		toWrite = append(toWrite, generatedFile{path: filePath, content: content})

		newManifestDocs = append(newManifestDocs, manifestDoc{
			ID:      fmt.Sprintf("%s--%s-%s-distilled", mod.team, mod.pageCode, mod.moduleCode),
			DocType: "distilled",
			Path:    filepath.ToSlash(filepath.Join("distilled", fileName)),
			Team:    mod.team,
			Product: mod.product,
			Version: mod.version,
		})
	}

	// Calculate updated manifest
	mergedDocs := updateManifestDocs(manifest.Docs, newManifestDocs, targetModules)
	manifest.Docs = mergedDocs

	updatedManifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal updated manifest: %w", err)
	}
	updatedManifestBytes = append(updatedManifestBytes, '\n')

	if o.check {
		return runCheck(distilledDir, toWrite, manifestPath, updatedManifestBytes, targetModules)
	}

	// Execute writes
	if len(targetModules) == 0 {
		// Full mode: clear distilledDir first
		if err := os.RemoveAll(distilledDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clean distilled directory: %w", err)
		}
	}
	if err := os.MkdirAll(distilledDir, 0o755); err != nil {
		return fmt.Errorf("create distilled directory: %w", err)
	}

	for _, file := range toWrite {
		if err := atomicfile.Replace(file.path, file.content, 0o600); err != nil {
			return fmt.Errorf("write distilled file %s: %w", file.path, err)
		}
	}

	if err := atomicfile.Replace(manifestPath, updatedManifestBytes, 0o600); err != nil {
		return fmt.Errorf("write updated kb_index.json: %w", err)
	}

	fmt.Fprintf(os.Stdout, "kbdistill: successfully generated %d distilled docs in %s\n", len(toWrite), distilledDir)
	return nil
}

func extractDistillData(sections []kb.Section, team string, targetModules map[string]bool) ([]moduleDistillData, error) {
	// Group sections by source procedure ID
	type sourceGroup struct {
		file     string
		meta     map[string]string
		sections []kb.Section
	}
	sources := make(map[string]*sourceGroup)
	for _, sec := range sections {
		meta := sec.Meta()
		if meta["doc_type"] != "procedure" {
			continue
		}
		if meta["team"] != team {
			continue
		}
		sourceID := meta["id"]
		if sourceID == "" {
			continue
		}
		group, ok := sources[sourceID]
		if !ok {
			group = &sourceGroup{
				file: sec.File(),
				meta: meta,
			}
			sources[sourceID] = group
		}
		group.sections = append(group.sections, sec)
	}

	// Sort sourceIDs for deterministic processing
	sourceIDs := make([]string, 0, len(sources))
	for id := range sources {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Strings(sourceIDs)

	var results []moduleDistillData
	seenModules := make(map[string]string) // moduleKey -> sourceID

	for _, sourceID := range sourceIDs {
		group := sources[sourceID]
		meta := group.meta
		pageCode := strings.TrimSpace(meta["page_code"])
		moduleCode := strings.TrimSpace(meta["module_code"])
		if pageCode == "" || moduleCode == "" {
			return nil, fmt.Errorf("%s: missing page_code or module_code", group.file)
		}
		moduleKey := pageCode + "/" + moduleCode
		if len(targetModules) > 0 && !targetModules[moduleKey] {
			continue
		}

		if prevSourceID, dup := seenModules[moduleKey]; dup {
			return nil, fmt.Errorf("duplicate module %q found in %s and %s", moduleKey, prevSourceID, sourceID)
		}
		seenModules[moduleKey] = sourceID

		steps, err := extractSteps(group.file, group.sections)
		if err != nil {
			return nil, err
		}
		if len(steps) == 0 {
			return nil, fmt.Errorf("%s: procedure has 0 valid steps", group.file)
		}

		results = append(results, moduleDistillData{
			sourceID:     sourceID,
			pageCode:     pageCode,
			moduleCode:   moduleCode,
			flowName:     strings.TrimSpace(meta["flow_name"]),
			team:         team,
			product:      strings.TrimSpace(meta["product"]),
			version:      strings.TrimSpace(meta["version"]),
			accessLevel:  strings.TrimSpace(meta["access_level"]),
			owner:        strings.TrimSpace(meta["owner"]),
			lastReviewed: strings.TrimSpace(meta["last_reviewed"]),
			steps:        steps,
		})
	}

	// Verify all target modules were found
	if len(targetModules) > 0 {
		for wantMod := range targetModules {
			if _, ok := seenModules[wantMod]; !ok {
				return nil, fmt.Errorf("specified module %q not found in procedure corpus", wantMod)
			}
		}
	}

	return results, nil
}

func extractSteps(file string, secs []kb.Section) ([]stepInfo, error) {
	var rawSteps []stepInfo
	for _, sec := range secs {
		m := stepHeadingRE.FindStringSubmatch(sec.Heading())
		if m == nil {
			continue
		}
		num, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: invalid step number %q in heading %q", file, m[1], sec.Heading())
		}
		action := strings.TrimSpace(m[2])

		body := sec.Body()
		evidence, when, then := extractStepBody(body)
		if evidence == "" {
			return nil, fmt.Errorf("%s: step %d missing 動作證據 in body", file, num)
		}

		rawSteps = append(rawSteps, stepInfo{
			num:         num,
			action:      action,
			anchor:      sec.Anchor(),
			evidence:    evidence,
			when:        when,
			then:        then,
			evidenceCls: sec.EvidenceClass(),
		})
	}

	// Sort steps by number
	sort.Slice(rawSteps, func(i, j int) bool {
		return rawSteps[i].num < rawSteps[j].num
	})

	// Check step sequence continuity
	seenNums := make(map[int]bool)
	for i, step := range rawSteps {
		if seenNums[step.num] {
			return nil, fmt.Errorf("%s: duplicate step number %d", file, step.num)
		}
		seenNums[step.num] = true
		expected := i + 1
		if step.num != expected {
			return nil, fmt.Errorf("%s: non-consecutive step numbers: got step %d, expected step %d", file, step.num, expected)
		}
	}

	return rawSteps, nil
}

func extractStepBody(body string) (evidence, when, then string) {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimSuffix(trimmed, "\r")
		if after, ok := cutPrefix(trimmed, "- 動作證據："); ok {
			evidence = strings.Trim(after, "` \t")
		} else if after, ok := cutPrefix(trimmed, "- 原始 Gherkin When（敘事註解）："); ok {
			when = strings.TrimSpace(after)
		} else if after, ok := cutPrefix(trimmed, "- 原始 Gherkin Then（敘事註解）："); ok {
			then = strings.TrimSpace(after)
		}
	}
	if when == "" {
		when = "missing"
	}
	if then == "" {
		then = "missing"
	}
	return evidence, when, then
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

func renderDistilledMarkdown(mod moduleDistillData) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %q\n", fmt.Sprintf("%s--%s-%s-distilled", mod.team, mod.pageCode, mod.moduleCode))
	fmt.Fprintf(&b, "team: %q\n", mod.team)
	fmt.Fprintf(&b, "product: %q\n", mod.product)
	b.WriteString("doc_type: \"distilled\"\n")
	fmt.Fprintf(&b, "version: %q\n", mod.version)
	fmt.Fprintf(&b, "access_level: %q\n", mod.accessLevel)
	fmt.Fprintf(&b, "owner: %q\n", mod.owner)
	fmt.Fprintf(&b, "last_reviewed: %q\n", mod.lastReviewed)
	b.WriteString("evidence_basis: \"derived_from_procedure\"\n")
	fmt.Fprintf(&b, "derived_from: %q\n", mod.sourceID)
	fmt.Fprintf(&b, "page_code: %q\n", mod.pageCode)
	fmt.Fprintf(&b, "module_code: %q\n", mod.moduleCode)
	if mod.flowName != "" {
		fmt.Fprintf(&b, "flow_name: %q\n", mod.flowName)
	}
	b.WriteString("---\n\n")

	flowTitle := mod.flowName
	if flowTitle == "" {
		flowTitle = fmt.Sprintf("%s-%s", mod.pageCode, mod.moduleCode)
	}
	fmt.Fprintf(&b, "## %s — 行為鏈\n", flowTitle)

	for _, step := range mod.steps {
		fmt.Fprintf(&b, "%d. %s → %s → %s\n", step.num, step.action, step.when, step.then)
		fmt.Fprintf(&b, "   [L0: #%s | evidence: %s]\n", step.anchor, step.evidenceCls)
	}

	return []byte(b.String())
}

func updateManifestDocs(existing []manifestDoc, newDocs []manifestDoc, targetModules map[string]bool) []manifestDoc {
	newDocMap := make(map[string]manifestDoc, len(newDocs))
	for _, doc := range newDocs {
		newDocMap[doc.ID] = doc
	}

	var retained []manifestDoc
	for _, doc := range existing {
		if doc.DocType == "distilled" {
			if len(targetModules) == 0 {
				// Full mode: omit all old distilled docs
				continue
			}
			// Module mode: omit if this doc matches one of the newly generated docs
			if _, exists := newDocMap[doc.ID]; exists {
				continue
			}
		}
		retained = append(retained, doc)
	}

	retained = append(retained, newDocs...)
	sort.Slice(retained, func(i, j int) bool {
		if retained[i].Path == retained[j].Path {
			return retained[i].ID < retained[j].ID
		}
		return retained[i].Path < retained[j].Path
	})
	return retained
}

func runCheck(distilledDir string, toWrite []generatedFile, manifestPath string, updatedManifestBytes []byte, targetModules map[string]bool) error {
	for _, file := range toWrite {
		current, err := os.ReadFile(file.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("check: missing distilled file %s", file.path)
			}
			return err
		}
		if !bytes.Equal(current, file.content) {
			return fmt.Errorf("check: distilled file content differs for %s", file.path)
		}
	}

	currManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("check: read manifest: %w", err)
	}
	if !bytes.Equal(currManifest, updatedManifestBytes) {
		return errors.New("check: kb_index.json content differs from expected")
	}

	// Check if any unexpected distilled files exist
	if len(targetModules) == 0 {
		expectedPaths := make(map[string]bool)
		for _, file := range toWrite {
			expectedPaths[filepath.Clean(file.path)] = true
		}
		err := filepath.WalkDir(distilledDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.HasSuffix(p, ".md") && !expectedPaths[filepath.Clean(p)] {
				return fmt.Errorf("check: stale distilled file %s found", p)
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	fmt.Println("check: ok, all distilled files and kb_index.json match expected state")
	return nil
}
