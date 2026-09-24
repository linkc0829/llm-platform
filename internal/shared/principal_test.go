package shared

import "testing"

// Kind decides who counts as a real user on the dashboard; a harness or the
// KB's own calls counted as people would inflate weekly active users.
func TestPrincipalKind(t *testing.T) {
	tests := []struct {
		name string
		p    Principal
		want string
	}{
		{"person", Principal{Name: "alice"}, KindUser},
		{"eval_harness", Principal{Name: "eval-runner"}, KindTest},
		{"load_test", Principal{Name: "gwload-u1"}, KindTest},
		{"trusted_service_wins_over_prefix", Principal{Name: "eval-kb", Trusted: true}, KindService},
		{"prefix_only_at_start", Principal{Name: "my-eval-bot"}, KindUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Kind(); got != tt.want {
				t.Errorf("Kind() = %q, want %q", got, tt.want)
			}
		})
	}
}
