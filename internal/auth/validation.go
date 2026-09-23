package auth

import (
	"errors"
	"regexp"
)

var (
	// ErrInvalidName indicates that a principal name does not use the stored
	// lowercase canonical form.
	ErrInvalidName = errors.New("invalid principal name")
	// ErrInvalidWorkload indicates that a workload type is not recognized.
	ErrInvalidWorkload = errors.New("invalid workload type")
	principalName      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
)

// ValidateName checks the canonical principal name format.
func ValidateName(name string) error {
	if !principalName.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// ValidateWorkload checks if workload is empty or one of rag, fim, agent, chat.
func ValidateWorkload(workload string) error {
	switch workload {
	case "", "rag", "fim", "agent", "chat":
		return nil
	default:
		return ErrInvalidWorkload
	}
}
