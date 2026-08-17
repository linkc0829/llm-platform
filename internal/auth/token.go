package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	// TokenPrefix makes bearer tokens recognizable to secret scanners and
	// operators without exposing any identity or capability information.
	TokenPrefix      = "kb_"
	tokenBytes       = 32
	principalIDBytes = 16
)

// GenerateToken returns a new bearer token with 256 bits of entropy.
func GenerateToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// GeneratePrincipalID returns an opaque immutable identity that is never
// derived from the display name. A deleted ID must never be reused.
func GeneratePrincipalID() (string, error) {
	raw := make([]byte, principalIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate principal id: %w", err)
	}
	return "p_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashToken returns the hexadecimal SHA-256 digest of the exact token string.
func HashToken(token string) string {
	digest := tokenDigest(token)
	return hex.EncodeToString(digest[:])
}

// VerifyToken compares a presented token with a stored hexadecimal SHA-256
// digest without making the comparison dependent on the first differing byte.
func VerifyToken(token, storedHash string) bool {
	return verifyTokenDigest(tokenDigest(token), storedHash)
}

func tokenDigest(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

func verifyTokenDigest(digest [sha256.Size]byte, storedHash string) bool {
	expected, err := hex.DecodeString(storedHash)
	if err != nil || len(expected) != sha256.Size {
		return false
	}

	return subtle.ConstantTimeCompare(digest[:], expected) == 1
}
