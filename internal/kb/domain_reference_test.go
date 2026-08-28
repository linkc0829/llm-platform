package kb

import (
	"errors"
	"testing"
)

// TestClassifySectionReference locks the tier contract for PM reference docs.
// `reference` has exactly one legal tier: the corpus owner decided PM content is
// all public, so unlike `procedure` there is no restricted half to fall back on.
// An engineering heading therefore means the document was filed under the wrong
// doc_type, and /index must fail so a human moves it rather than silently
// publishing engineering detail to every support token.
func TestClassifySectionReference(t *testing.T) {
	tests := []struct {
		name     string
		heading  string
		body     string
		wantTier SectionTier
		wantErr  error
	}{
		{
			name:     "reference_plain_heading_is_public",
			heading:  "菜單體系",
			body:     "菜單由品項、供應時段與價目表三個子體系組成。",
			wantTier: SectionTierPublic,
		},
		{
			name:    "reference_engineering_heading_is_rejected",
			heading: "菜單體系 — 工程對應(Engineering Context)",
			body:    "這一段不該存在於 reference 文件。",
			wantErr: ErrInvalidSectionAccess,
		},
		{
			// The drift guard runs before the doc_type switch, so a reference
			// section that smuggles in an endpoint under an ordinary heading
			// must fail closed rather than reach the public branch.
			name:    "reference_plain_heading_with_endpoint_fails_closed",
			heading: "菜單體系",
			body:    "- **Endpoint Index**: GET /v1/business/menus",
			wantErr: ErrSectionAccessDrift,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			section, err := NewSection("pm/menu.md", tt.heading, tt.body,
				map[string]string{"doc_type": "reference"}, nil)
			if err != nil {
				t.Fatalf("NewSection() error = %v, want nil", err)
			}
			tier, err := ClassifySection(section)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ClassifySection() error = %v, want %v", err, tt.wantErr)
				}
				if tier != SectionTierInvalid {
					t.Errorf("ClassifySection() tier = %v, want SectionTierInvalid", tier)
				}
				return
			}
			if err != nil {
				t.Fatalf("ClassifySection() error = %v, want nil", err)
			}
			if tier != tt.wantTier {
				t.Errorf("ClassifySection() tier = %v, want %v", tier, tt.wantTier)
			}
		})
	}
}
