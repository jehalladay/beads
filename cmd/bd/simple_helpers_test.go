package main

import (
	"testing"
)

func TestParseLabelArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectIDs    int
		expectLabel  string
	}{
		{
			name:        "single ID single label",
			args:        []string{"bd-1", "bug"},
			expectIDs:   1,
			expectLabel: "bug",
		},
		{
			name:        "multiple IDs single label",
			args:        []string{"bd-1", "bd-2", "critical"},
			expectIDs:   2,
			expectLabel: "critical",
		},
		{
			name:        "three IDs one label",
			args:        []string{"bd-1", "bd-2", "bd-3", "bug"},
			expectIDs:   3,
			expectLabel: "bug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, label := parseLabelArgs(tt.args)

			if len(ids) != tt.expectIDs {
				t.Errorf("Expected %d IDs, got %d", tt.expectIDs, len(ids))
			}

			if label != tt.expectLabel {
				t.Errorf("Expected label %q, got %q", tt.expectLabel, label)
			}
		})
	}
}

func TestReplaceBoundaryAware(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		oldID    string
		newID    string
		expected string
	}{
		{
			name:     "simple replacement",
			text:     "See bd-1 for details",
			oldID:    "bd-1",
			newID:    "bd-100",
			expected: "See bd-100 for details",
		},
		{
			name:     "multiple occurrences",
			text:     "bd-1 relates to bd-1",
			oldID:    "bd-1",
			newID:    "bd-999",
			expected: "bd-999 relates to bd-999",
		},
		{
			name:     "no match",
			text:     "See bd-2 for details",
			oldID:    "bd-1",
			newID:    "bd-100",
			expected: "See bd-2 for details",
		},
		{
			name:     "boundary awareness - don't replace partial match",
			text:     "bd-1000 is different from bd-100",
			oldID:    "bd-100",
			newID:    "bd-999",
			expected: "bd-1000 is different from bd-999",
		},
		{
			name:     "in parentheses",
			text:     "Related issue (bd-42)",
			oldID:    "bd-42",
			newID:    "bd-1",
			expected: "Related issue (bd-1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceBoundaryAware(tt.text, tt.oldID, tt.newID)
			if result != tt.expected {
				t.Errorf("replaceBoundaryAware(%q, %q, %q) = %q, want %q",
					tt.text, tt.oldID, tt.newID, result, tt.expected)
			}
		})
	}
}
