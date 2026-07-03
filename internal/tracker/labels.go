package tracker

import "strings"

// Shared helpers for label-based field mapping. Several trackers (notably
// GitHub and GitLab) encode priority, status and type as scoped labels of the
// form "prefix::value" (e.g. "priority::high", "type::bug"). These helpers hold
// the extraction logic in one place so each adapter does not re-implement it.

// ParseLabelPrefix splits a scoped label of the form "prefix::value" into its
// prefix and value. A label without a "::" separator yields an empty prefix and
// the whole label as the value.
func ParseLabelPrefix(label string) (prefix, value string) {
	parts := strings.SplitN(label, "::", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", label
}

// PriorityFromLabels extracts a beads priority (0-4) from scoped "priority::*"
// labels, looking each value up (lower-cased) in priorityMap. Returns 2
// (medium) when no priority label matches.
func PriorityFromLabels(labels []string, priorityMap map[string]int) int {
	for _, label := range labels {
		prefix, value := ParseLabelPrefix(label)
		if prefix == "priority" {
			if p, ok := priorityMap[strings.ToLower(value)]; ok {
				return p
			}
		}
	}
	return 2 // Default to medium
}

// TypeFromLabels extracts a beads issue type from labels, looking up both
// scoped "type::*" labels and bare (unprefixed) labels (lower-cased) in
// typeMap. Returns "task" when no type label matches.
func TypeFromLabels(labels []string, typeMap map[string]string) string {
	for _, label := range labels {
		prefix, value := ParseLabelPrefix(label)
		if prefix == "type" {
			if t, ok := typeMap[strings.ToLower(value)]; ok {
				return t
			}
		}
		// Also check bare labels (no prefix)
		if prefix == "" {
			if t, ok := typeMap[strings.ToLower(value)]; ok {
				return t
			}
		}
	}
	return "task" // Default to task
}

// FilterScopedLabels returns only the labels that are not scoped
// priority/status/type labels. Those scoped labels map to dedicated beads
// fields, so they are dropped from the free-form label set.
func FilterScopedLabels(labels []string) []string {
	var filtered []string
	for _, label := range labels {
		prefix, _ := ParseLabelPrefix(label)
		// Skip scoped labels that we handle specially
		if prefix == "priority" || prefix == "status" || prefix == "type" {
			continue
		}
		filtered = append(filtered, label)
	}
	return filtered
}
