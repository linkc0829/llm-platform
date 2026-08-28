package auth

import (
	"errors"
	"regexp"
)

var (
	// ErrInvalidName indicates that a principal name does not use the stored
	// lowercase canonical form.
	ErrInvalidName = errors.New("invalid principal name")
	principalName  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
)

// ValidateName checks the canonical principal name format.
func ValidateName(name string) error {
	if !principalName.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}
