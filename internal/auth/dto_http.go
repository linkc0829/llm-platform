package auth

import "time"

// CreateTokenRequest is the network API payload. Admin is accepted for
// compatibility but intentionally ignored by the handler.
type CreateTokenRequest struct {
	Name        string   `json:"name"`
	Teams       []string `json:"teams"`
	AllTeams    bool     `json:"all_teams"`
	Engineering bool     `json:"engineering"`
	Indexer     bool     `json:"indexer"`
	Admin       bool     `json:"admin"`
}

type CreateTokenResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// TokenResponse never includes the stored token digest.
type TokenResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Teams       []string  `json:"teams,omitempty"`
	AllTeams    bool      `json:"all_teams"`
	Engineering bool      `json:"engineering"`
	Indexer     bool      `json:"indexer"`
	Admin       bool      `json:"admin"`
	CreatedAt   time.Time `json:"created_at"`
}

func tokenResponse(record Record) TokenResponse {
	return TokenResponse{
		ID:          record.ID,
		Name:        record.Name,
		Teams:       append([]string(nil), record.Teams...),
		AllTeams:    record.AllTeams,
		Engineering: record.Engineering,
		Indexer:     record.Indexer,
		Admin:       record.Admin,
		CreatedAt:   record.CreatedAt,
	}
}
