package auth

import (
	"errors"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "lowercase name", value: "alice", valid: true},
		{name: "dot and hyphen", value: "a-team.v2", valid: true},
		{name: "uppercase name", value: "Alice", valid: false},
		{name: "starts with punctuation", value: "-alice", valid: false},
		{name: "one character", value: "a", valid: false},
		{name: "space", value: "a team", valid: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.value)
			if got := err == nil; got != tc.valid {
				t.Errorf("ValidateName(%q) error = %v, valid = %t, want valid = %t", tc.value, err, got, tc.valid)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidName) {
				t.Errorf("ValidateName(%q) error = %v, want ErrInvalidName", tc.value, err)
			}
		})
	}
}

func TestValidateWorkload(t *testing.T) {
	tests := []struct {
		name     string
		workload string
		valid    bool
	}{
		{name: "empty (default)", workload: "", valid: true},
		{name: "rag", workload: "rag", valid: true},
		{name: "fim", workload: "fim", valid: true},
		{name: "agent", workload: "agent", valid: true},
		{name: "chat", workload: "chat", valid: true},
		{name: "uppercase", workload: "RAG", valid: false},
		{name: "invalid word", workload: "custom", valid: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWorkload(tc.workload)
			if got := err == nil; got != tc.valid {
				t.Errorf("ValidateWorkload(%q) valid = %t, want %t (err=%v)", tc.workload, got, tc.valid, err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidWorkload) {
				t.Errorf("ValidateWorkload(%q) error = %v, want ErrInvalidWorkload", tc.workload, err)
			}
		})
	}
}

