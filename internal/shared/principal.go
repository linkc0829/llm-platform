package shared

import "strings"

// Principal is the immutable identity and capability set associated with a
// bearer token. ID is the identity used for ownership checks; Name is only a
// display and administration label.
type Principal struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Teams       []string `json:"teams,omitempty"`
	AllTeams    bool     `json:"all_teams,omitempty"`
	Engineering bool     `json:"engineering,omitempty"`
	Indexer     bool     `json:"indexer,omitempty"`
	Admin       bool     `json:"admin,omitempty"`
	Workload    string   `json:"workload,omitempty"`
	Trusted     bool     `json:"trusted,omitempty"`
}

// Principal kinds separate real people from test and service traffic, so
// usage figures can count people only.
const (
	KindUser    = "user"
	KindTest    = "test"
	KindService = "service"
)

// Kind classifies the principal. Trusted tokens are services (the KB's own
// calls); eval- and gwload- principals are test harnesses.
func (p Principal) Kind() string {
	switch {
	case p.Trusted:
		return KindService
	case strings.HasPrefix(p.Name, "eval-"), strings.HasPrefix(p.Name, "gwload-"):
		return KindTest
	default:
		return KindUser
	}
}
