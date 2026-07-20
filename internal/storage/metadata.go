// Package storage defines the interface for issue storage backends.
package storage

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// NormalizeMetadataValue converts metadata values to a validated JSON string.
// Accepts string, []byte, or json.RawMessage and returns a validated JSON string.
// Returns an error if the value is not valid JSON or is an unsupported type.
//
// This supports GH#1417: allow UpdateIssue metadata updates via json.RawMessage/[]byte.
func NormalizeMetadataValue(value interface{}) (string, error) {
	var jsonStr string

	switch v := value.(type) {
	case string:
		jsonStr = v
	case []byte:
		jsonStr = string(v)
	case json.RawMessage:
		jsonStr = string(v)
	default:
		return "", fmt.Errorf("metadata must be string, []byte, or json.RawMessage, got %T", value)
	}

	// Validate that it's valid JSON
	if !json.Valid([]byte(jsonStr)) {
		return "", fmt.Errorf("metadata is not valid JSON")
	}

	return jsonStr, nil
}

// MetadataFieldType defines the type of a metadata field for schema validation.
type MetadataFieldType string

const (
	MetadataFieldString MetadataFieldType = "string"
	MetadataFieldInt    MetadataFieldType = "int"
	MetadataFieldFloat  MetadataFieldType = "float"
	MetadataFieldBool   MetadataFieldType = "bool"
	MetadataFieldEnum   MetadataFieldType = "enum"
)

// MetadataFieldSchema defines validation rules for a single metadata field.
type MetadataFieldSchema struct {
	Type     MetadataFieldType
	Values   []string // allowed values for enum type
	Required bool
	Min      *float64 // min value for int/float
	Max      *float64 // max value for int/float
}

// MetadataSchemaConfig holds the parsed metadata validation configuration.
type MetadataSchemaConfig struct {
	Mode   string                         // "none", "warn", "error"
	Fields map[string]MetadataFieldSchema // field name → schema
}

// MetadataValidationError describes a single schema violation.
type MetadataValidationError struct {
	Field   string
	Message string
}

func (e MetadataValidationError) Error() string {
	return fmt.Sprintf("metadata.%s: %s", e.Field, e.Message)
}

// ValidateMetadataSchema validates a metadata blob against a schema config.
// Returns a list of validation errors. An empty list means validation passed.
// If metadata is nil/empty and no fields are required, returns no errors.
func ValidateMetadataSchema(metadata json.RawMessage, schema MetadataSchemaConfig) []MetadataValidationError {
	if len(schema.Fields) == 0 {
		return nil
	}

	// Parse metadata into a map
	var parsed map[string]interface{}
	if len(metadata) == 0 || string(metadata) == "{}" || string(metadata) == "null" {
		parsed = map[string]interface{}{}
	} else {
		if err := json.Unmarshal(metadata, &parsed); err != nil {
			// Not a JSON object — can't validate fields
			return []MetadataValidationError{{Field: "(root)", Message: "metadata must be a JSON object for schema validation"}}
		}
	}

	var errs []MetadataValidationError

	for fieldName, fieldSchema := range schema.Fields {
		val, exists := parsed[fieldName]

		// Check required
		if fieldSchema.Required && !exists {
			errs = append(errs, MetadataValidationError{
				Field:   fieldName,
				Message: "required field is missing",
			})
			continue
		}

		if !exists {
			continue
		}

		// Type-check the value
		switch fieldSchema.Type {
		case MetadataFieldString:
			if _, ok := val.(string); !ok {
				errs = append(errs, MetadataValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("expected string, got %T", val),
				})
			}

		case MetadataFieldInt:
			num, ok := val.(float64)
			if !ok {
				errs = append(errs, MetadataValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("expected int, got %T", val),
				})
			} else {
				if num != float64(int64(num)) {
					errs = append(errs, MetadataValidationError{
						Field:   fieldName,
						Message: fmt.Sprintf("expected int, got float %v", num),
					})
				} else {
					if fieldSchema.Min != nil && num < *fieldSchema.Min {
						errs = append(errs, MetadataValidationError{
							Field:   fieldName,
							Message: fmt.Sprintf("value %v is below minimum %v", num, *fieldSchema.Min),
						})
					}
					if fieldSchema.Max != nil && num > *fieldSchema.Max {
						errs = append(errs, MetadataValidationError{
							Field:   fieldName,
							Message: fmt.Sprintf("value %v exceeds maximum %v", num, *fieldSchema.Max),
						})
					}
				}
			}

		case MetadataFieldFloat:
			num, ok := val.(float64)
			if !ok {
				errs = append(errs, MetadataValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("expected float, got %T", val),
				})
			} else {
				if fieldSchema.Min != nil && num < *fieldSchema.Min {
					errs = append(errs, MetadataValidationError{
						Field:   fieldName,
						Message: fmt.Sprintf("value %v is below minimum %v", num, *fieldSchema.Min),
					})
				}
				if fieldSchema.Max != nil && num > *fieldSchema.Max {
					errs = append(errs, MetadataValidationError{
						Field:   fieldName,
						Message: fmt.Sprintf("value %v exceeds maximum %v", num, *fieldSchema.Max),
					})
				}
			}

		case MetadataFieldBool:
			if _, ok := val.(bool); !ok {
				errs = append(errs, MetadataValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("expected bool, got %T", val),
				})
			}

		case MetadataFieldEnum:
			str, ok := val.(string)
			if !ok {
				errs = append(errs, MetadataValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("expected string (enum), got %T", val),
				})
			} else {
				found := false
				for _, allowed := range fieldSchema.Values {
					if str == allowed {
						found = true
						break
					}
				}
				if !found {
					errs = append(errs, MetadataValidationError{
						Field:   fieldName,
						Message: fmt.Sprintf("value %q is not one of: %v", str, fieldSchema.Values),
					})
				}
			}
		}
	}

	return errs
}

// ValidateMetadataReadable rejects a metadata blob whose decoded JSON contains
// raw control characters (U+0000–U+001F or U+007F) in any string key or value.
//
// The metadata column is a Dolt JSON type. A control byte reaches it when an
// untrusted source (a `bd import` JSONL line, or `bd create/update --metadata`)
// carries the char as a \uXXXX escape: json.Unmarshal decodes it to a real
// control byte, which Dolt stores but then RE-EMITS raw on readback — and a raw
// control char inside a JSON string is invalid JSON, so beads' own re-parse of
// the column fails with "rows: error processing column N: invalid character".
// A single such row bricks EVERY subsequent bd list/show/export repo-wide
// (data-availability defect / import-DoS, beads-nc639). Reject at write instead
// of silently persisting an unreadable row.
//
// An empty/absent blob is fine. A blob that isn't valid JSON at all is left to
// the caller's other validators (metadataIsJSONObject etc.) — this checks only
// the readback-safety of an otherwise-decodable blob.
func ValidateMetadataReadable(metadata json.RawMessage) error {
	if len(metadata) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(metadata, &v); err != nil {
		// Not decodable JSON — not this validator's concern.
		return nil
	}
	if bad, ok := findControlCharString(v); ok {
		return fmt.Errorf("metadata contains a control character (0x%02x) that would make the stored JSON unreadable; strip control characters before storing", bad)
	}
	return nil
}

// findControlCharString walks a decoded JSON value and returns the first
// control character found in any string key or value.
func findControlCharString(v any) (rune, bool) {
	switch val := v.(type) {
	case string:
		for _, r := range val {
			if r < 0x20 || r == 0x7f {
				return r, true
			}
		}
	case map[string]any:
		// Deterministic order so the reported char is stable across runs.
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, r := range k {
				if r < 0x20 || r == 0x7f {
					return r, true
				}
			}
			if bad, ok := findControlCharString(val[k]); ok {
				return bad, true
			}
		}
	case []any:
		for _, item := range val {
			if bad, ok := findControlCharString(item); ok {
				return bad, true
			}
		}
	}
	return 0, false
}

// validMetadataKeyRe validates metadata key names for use in JSON path expressions.
// Allows alphanumeric, underscore, and dot (for nested paths like "jira.sprint").
var validMetadataKeyRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)

// ValidateMetadataKey checks that a metadata key is safe for use in JSON path
// expressions. Keys must start with a letter or underscore and contain only
// alphanumeric characters, underscores, and dots.
func ValidateMetadataKey(key string) error {
	if !validMetadataKeyRe.MatchString(key) {
		return fmt.Errorf("invalid metadata key %q: must match [a-zA-Z_][a-zA-Z0-9_.]*", key)
	}
	return nil
}

// JSONMetadataPath returns a MySQL/Dolt JSON path expression for the given
// metadata key. Keys containing dots are quoted so that "gc.routed_to"
// produces '$."gc.routed_to"' instead of '$.gc.routed_to' (which dolt
// interprets as a nested path: {gc: {routed_to: ...}}).
func JSONMetadataPath(key string) string {
	if strings.Contains(key, ".") {
		return `$."` + key + `"`
	}
	return "$." + key
}
