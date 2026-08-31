package kb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/linkc0829/llm-platform/internal/shared"
)

func TestClassifySectionFourCases(t *testing.T) {
	tests := []struct {
		name     string
		heading  string
		body     string
		docType  string
		wantTier SectionTier
		wantErr  error
	}{
		{
			name:     "engineering_heading_with_endpoint",
			heading:  "帳號權限 — 工程對應:Services/APIs",
			body:     "**Verified Services/APIs**\nGET /api/accounts",
			docType:  "procedure",
			wantTier: SectionTierRestricted,
		},
		{
			name:     "engineering_heading_without_endpoint",
			heading:  "帳號權限 — 工程對應:ViewModels",
			body:     "**Verified ViewModels**: StoreAccountAdjustPage",
			docType:  "procedure",
			wantTier: SectionTierRestricted,
		},
		{
			name:     "engineering_reference_is_always_restricted",
			heading:  "POS Repo Map",
			body:     "source revision: sha256:test\nservice/api is the endpoint authority\nPATCH /v1/example",
			docType:  "engineering_reference",
			wantTier: SectionTierRestricted,
		},
		{
			name:     "operation_heading_without_endpoint",
			heading:  "步驟 1",
			body:     "點擊儲存並確認成功訊息。",
			docType:  "procedure",
			wantTier: SectionTierPublic,
		},
		{
			name:    "operation_heading_with_endpoint",
			heading: "步驟 1",
			body:    "POST /api/accounts",
			docType: "procedure",
			wantErr: ErrSectionAccessDrift,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			section := accessTestSection(t, tt.heading, tt.body, tt.docType, "Store.POS")
			got, err := ClassifySection(section)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ClassifySection(%q) error = %v, want %v", tt.heading, err, tt.wantErr)
			}
			if tt.wantErr == nil && got != tt.wantTier {
				t.Errorf("ClassifySection(%q) tier = %v, want %v", tt.heading, got, tt.wantTier)
			}
		})
	}
}

func TestClassifySectionRejectsUnknownDocType(t *testing.T) {
	section := accessTestSection(t, "說明", "一般內容", "", "Store.POS")
	if _, err := ClassifySection(section); !errors.Is(err, ErrInvalidSectionAccess) {
		t.Errorf("ClassifySection(%q) error = %v, want ErrInvalidSectionAccess", section.Citation(), err)
	}
}

func TestEngineeringReferenceRequiresRestrictedMetadata(t *testing.T) {
	section := accessTestSection(t, "POS Repo Map", "source revision: sha256:test", "engineering_reference", "Store.POS")
	section.meta["access_level"] = "internal"
	if _, err := ClassifySection(section); !errors.Is(err, ErrInvalidSectionAccess) {
		t.Fatalf("ClassifySection(%q) error = %v, want ErrInvalidSectionAccess", section.Citation(), err)
	}
}

func TestClassificationBaselineDistribution(t *testing.T) {
	cases := []struct {
		name  string
		count int
		make  func(int) Section
	}{
		{name: "ui_inventory_public", count: 389, make: func(_ int) Section {
			return accessTestSection(t, "畫面", "控制項", "ui_inventory", "Store.POS")
		}},
		{name: "procedure_public", count: 124, make: func(_ int) Section {
			return accessTestSection(t, "步驟 1", "點擊按鈕", "procedure", "Store.POS")
		}},
		{name: "procedure_engineering_without_endpoint", count: 34, make: func(_ int) Section {
			return accessTestSection(t, "工程對應:ViewModels", "Verified ViewModels: StoreAccountAdjustPage", "procedure", "Store.POS")
		}},
		{name: "procedure_engineering_with_endpoint", count: 109, make: func(_ int) Section {
			return accessTestSection(t, "工程對應:Services/APIs", "**Verified Services/APIs**\nGET /api/accounts", "procedure", "Store.POS")
		}},
		{name: "playlist_public", count: 23, make: func(_ int) Section {
			return accessTestSection(t, "播放清單", "操作流程", "playlist", "Store.POS")
		}},
	}

	sections := make([]Section, 0, 679)
	counts := map[string]int{}
	for _, tc := range cases {
		for i := 0; i < tc.count; i++ {
			section := tc.make(i)
			sections = append(sections, section)
			tier, err := ClassifySection(section)
			if err != nil {
				t.Fatalf("ClassifySection(%q) error = %v, want nil", section.Citation(), err)
			}
			counts[tc.name]++
			if tc.name == "procedure_engineering_without_endpoint" || tc.name == "procedure_engineering_with_endpoint" {
				if tier != SectionTierRestricted {
					t.Fatalf("ClassifySection(%q) tier = %v, want restricted", section.Citation(), tier)
				}
			} else if tier != SectionTierPublic {
				t.Fatalf("ClassifySection(%q) tier = %v, want public", section.Citation(), tier)
			}
		}
	}
	if len(sections) != 679 {
		t.Fatalf("classification baseline sections = %d, want 679", len(sections))
	}
	if err := AuditSections(sections); err != nil {
		t.Fatalf("AuditSections(classification baseline) error = %v, want nil", err)
	}
	for _, tc := range cases {
		if counts[tc.name] != tc.count {
			t.Errorf("classification baseline %s = %d, want %d", tc.name, counts[tc.name], tc.count)
		}
	}
}

func TestCanSeeAppliesTeamAndTier(t *testing.T) {
	public := accessTestSection(t, "步驟 1", "點擊儲存", "procedure", "Store.POS")
	restricted := accessTestSection(t, "工程對應:Commands", "Verified Commands: clickCreate()", "procedure", "Store.POS")
	engineeringReference := accessTestSection(t, "POS Repo Map", "source revision: sha256:test", "engineering_reference", "Store.POS")
	tests := []struct {
		name      string
		principal shared.Principal
		section   Section
		want      bool
	}{
		{name: "regular_sees_public", principal: shared.Principal{ID: "p_regular", AllTeams: true}, section: public, want: true},
		{name: "regular_cannot_see_restricted", principal: shared.Principal{ID: "p_regular", AllTeams: true}, section: restricted, want: false},
		{name: "engineering_sees_restricted_in_team", principal: shared.Principal{ID: "p_engineer", Teams: []string{"Store.POS"}, Engineering: true}, section: restricted, want: true},
		{name: "engineering_wrong_team_is_denied", principal: shared.Principal{ID: "p_engineer", Teams: []string{"Other"}, Engineering: true}, section: restricted, want: false},
		{name: "regular_cannot_see_engineering_reference", principal: shared.Principal{ID: "p_regular", AllTeams: true}, section: engineeringReference, want: false},
		{name: "engineering_sees_engineering_reference", principal: shared.Principal{ID: "p_engineer", Teams: []string{"Store.POS"}, Engineering: true}, section: engineeringReference, want: true},
		{name: "zero_principal_is_denied", principal: shared.Principal{}, section: public, want: false},
		{name: "invalid_section_is_denied_even_for_full_access", principal: FullAccessPrincipal("p_full"), section: accessTestSection(t, "步驟 1", "POST /api/secret", "procedure", "Store.POS"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanSee(tt.principal, tt.section); got != tt.want {
				t.Errorf("CanSee(%#v, %q) = %v, want %v", tt.principal, tt.section.Citation(), got, tt.want)
			}
		})
	}
}

func TestRankVectorFiltersBeforeLimit(t *testing.T) {
	engineering := accessTestSection(t, "工程對應:Services/APIs", "GET /api/secret", "procedure", "Store.POS")
	public := accessTestSection(t, "步驟 1", "設定資料", "procedure", "Store.POS")
	indexed := []Section{engineering, public}
	allow := func(section Section) bool { return CanSee(shared.Principal{ID: "p_regular", AllTeams: true}, section) }
	got := RankVector(indexed, map[string][]float32{
		engineering.Citation(): {1, 0},
		public.Citation():      {0.9, 0.1},
	}, []float32{1, 0}, 1, allow)
	if len(got) != 1 || got[0].Index != 1 {
		t.Errorf("RankVector(filtered, limit=1) = %#v, want public index 1", got)
	}
}

func TestServiceIndexAuditBlocksSaveEmbedAndSwap(t *testing.T) {
	initial := accessTestSection(t, "步驟 1", "舊內容", "procedure", "Store.POS")
	invalid := accessTestSection(t, "步驟 1", "POST /api/secret", "procedure", "Store.POS")
	store := &fakeSectionStore{parseSections: []Section{invalid}, parseFiles: 1}
	embedder := &fakeEmbedder{vectors: map[string][]float32{invalid.Body(): {1, 0}}}
	svc := NewService(store, nil, embedder, &fakeVectorStore{}, nil, "test-model")
	svc.storeIndexSnapshot([]Section{initial}, BuildCorpus([]Section{initial}), nil, true)

	if _, _, err := svc.Index(context.Background()); !errors.Is(err, ErrSectionAccessDrift) {
		t.Fatalf("Service.Index() error = %v, want ErrSectionAccessDrift", err)
	}
	if store.saved != nil {
		t.Errorf("Service.Index() saved = %#v, want nil after audit failure", store.saved)
	}
	if embedder.calls != 0 {
		t.Errorf("Service.Index() embedder calls = %d, want 0 after audit failure", embedder.calls)
	}
	indexed, _, _, ready := svc.indexSnapshot()
	if !ready || len(indexed) != 1 || indexed[0].Citation() != initial.Citation() {
		t.Errorf("Service.Index() snapshot = %#v/%v, want unchanged last-known-good snapshot", indexed, ready)
	}
}

func TestServiceLoadOnStartupRejectsUnauditedSnapshot(t *testing.T) {
	lastKnownGood := accessTestSection(t, "步驟 1", "舊內容", "procedure", "Store.POS")
	invalid := accessTestSection(t, "步驟 1", "POST /api/secret", "procedure", "Store.POS")
	store := &fakeSectionStore{loadSections: []Section{invalid}}
	svc := NewService(store, nil, nil, nil, nil, "test-model")
	svc.storeIndexSnapshot([]Section{lastKnownGood}, BuildCorpus([]Section{lastKnownGood}), nil, true)

	if err := svc.LoadOnStartup(context.Background()); !errors.Is(err, ErrIndexAccessAuditFailed) {
		t.Fatalf("Service.LoadOnStartup() error = %v, want ErrIndexAccessAuditFailed", err)
	}
	indexed, _, _, ready := svc.indexSnapshot()
	if !ready || len(indexed) != 1 || indexed[0].Citation() != lastKnownGood.Citation() {
		t.Errorf("Service.LoadOnStartup() snapshot = %#v/%v, want unchanged last-known-good snapshot", indexed, ready)
	}
}

func TestServiceFilteredMetricsIgnoreRestrictedBM25Evidence(t *testing.T) {
	restricted := accessTestSection(t, "工程對應:Services/APIs", "secret secret secret", "procedure", "Store.POS")
	public := accessTestSection(t, "步驟 1", "ordinary operation", "procedure", "Store.POS")
	llm := &fakeLLM{answer: "should not be called"}
	svc := NewService(&fakeSectionStore{}, llm, nil, nil, NewInProcStore(), "test-model")
	svc.storeIndexSnapshot([]Section{restricted, public}, BuildCorpus([]Section{restricted, public}), nil, true)

	answer, _, metrics, err := svc.ChatWithMetrics(context.Background(), shared.Principal{ID: "p_regular", AllTeams: true}, "secret", "session")
	if err != nil {
		t.Fatalf("Service.ChatWithPrincipal() error = %v, want nil refusal", err)
	}
	if answer.Grounded() || answer.Text() != "I cannot confirm that from the knowledge base." {
		t.Errorf("Service.ChatWithPrincipal() answer = %#v, want ungrounded refusal", answer)
	}
	if metrics.BM25Max != 0 {
		t.Errorf("Service.ChatWithPrincipal() BM25Max = %v, want 0 after filtering", metrics.BM25Max)
	}
	if llm.calls != 0 {
		t.Errorf("Service.ChatWithPrincipal() LLM calls = %d, want 0", llm.calls)
	}
}

func TestMarkdownRepoMergeMetaProtectsFrontMatter(t *testing.T) {
	got := mergeMeta(
		map[string]string{"id": "front-id", "team": "Store.POS", "access_level": "internal", "owner": "front"},
		map[string]string{"id": "body-id", "team": "Other", "access_level": "public", "owner": "body", "功能區": "退款"},
	)
	for key, want := range map[string]string{"id": "front-id", "team": "Store.POS", "access_level": "internal", "owner": "body", "功能區": "退款"} {
		if got[key] != want {
			t.Errorf("mergeMeta()[%q] = %q, want %q", key, got[key], want)
		}
	}
}

func TestServiceIndexAuditLeavesMarkdownSnapshotUntouched(t *testing.T) {
	docsDir := t.TempDir()
	indexDir := t.TempDir()
	docPath := filepath.Join(docsDir, "account-procedure.md")
	writeTestFile(t, docPath, "---\nid: account\nteam: Store.POS\nproduct: POS\ndoc_type: procedure\nversion: v1\naccess_level: internal\nowner: POS\nlast_reviewed: 2026-08-14\n---\n\n# 帳號\n\n## 步驟 1\n設定資料。\n")
	repo := NewMarkdownRepo(docsDir, indexDir)
	sections, _, err := repo.Parse(context.Background())
	if err != nil {
		t.Fatalf("MarkdownRepo.Parse() error = %v, want nil", err)
	}
	if err := repo.Save(context.Background(), sections); err != nil {
		t.Fatalf("MarkdownRepo.Save() error = %v, want nil", err)
	}
	svc := NewService(repo, nil, &fakeEmbedder{}, &fakeVectorStore{}, nil, "test-model")
	if err := svc.LoadOnStartup(context.Background()); err != nil {
		t.Fatalf("Service.LoadOnStartup() error = %v, want nil", err)
	}
	indexPath := filepath.Join(indexDir, "index.json")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v, want nil", indexPath, err)
	}
	beforeInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v, want nil", indexPath, err)
	}
	writeTestFile(t, docPath, "---\nid: account\nteam: Store.POS\nproduct: POS\ndoc_type: procedure\nversion: v1\naccess_level: internal\nowner: POS\nlast_reviewed: 2026-08-14\n---\n\n# 帳號\n\n## 步驟 1\nPOST /api/accounts\n")

	if _, _, err := svc.Index(context.Background()); !errors.Is(err, ErrSectionAccessDrift) {
		t.Fatalf("Service.Index() error = %v, want ErrSectionAccessDrift", err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) after failed index error = %v, want nil", indexPath, err)
	}
	afterInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("Stat(%q) after failed index error = %v, want nil", indexPath, err)
	}
	if string(after) != string(before) || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Errorf("failed Service.Index() changed index snapshot bytes/mtime, want unchanged")
	}
	if got := svc.indexSnapshotSections(); len(got) != 1 || got[0].Citation() != sections[0].Citation() {
		t.Errorf("failed Service.Index() in-memory snapshot = %#v, want last-known-good", got)
	}
}

func (s *Service) indexSnapshotSections() []Section {
	indexed, _, _, _ := s.indexSnapshot()
	return indexed
}

func accessTestSection(t *testing.T, heading, body, docType, team string) Section {
	t.Helper()
	meta := map[string]string{"doc_type": docType, "team": team}
	if docType == "engineering_reference" {
		meta["access_level"] = "internal-engineering"
	}
	section, err := NewSection(
		"access-test.md#"+heading,
		heading,
		body,
		meta,
		nil,
	)
	if err != nil {
		t.Fatalf("NewSection(%q) error = %v, want nil", heading, err)
	}
	// Hand the section back in the state the service publishes it in. CanSee
	// reads the stamped tier, so an unstamped section is invisible regardless of
	// its content — correct for production, but it would make these tests assert
	// the fail-closed default instead of the rule they are about. A section that
	// fails classification stays SectionTierInvalid, which is what the
	// drift cases want.
	stamped := []Section{section}
	StampTiers(stamped)
	return stamped[0]
}
