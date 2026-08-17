package shared

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
}
