package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/internal/config"
)

// LoadMetadataSchema reads the metadata validation config from YAML and returns
// a parsed schema. Returns mode "none" with empty fields if config is not
// initialized, mode is empty/unknown, or no fields are defined.
//
// This is the single shared source of truth for metadata-schema config assembly.
// It previously existed as identical unexported copies in the DoltStore wrapper
// (dolt.loadMetadataSchema) and issueops (ValidateMetadataIfConfigured), which
// meant the domain/proxied-server stack — reachable but importing neither —
// silently skipped schema validation (beads-lsbu, beads-83h3). Living in the
// low-level storage package (already home to ValidateMetadataSchema), it is
// callable from every stack with no import cycle.
func LoadMetadataSchema() MetadataSchemaConfig {
	mode := config.MetadataValidationMode()
	if mode == "none" || mode == "" {
		return MetadataSchemaConfig{Mode: "none"}
	}

	rawFields := config.MetadataSchemaFields()
	if rawFields == nil {
		return MetadataSchemaConfig{Mode: "none"}
	}

	fields := make(map[string]MetadataFieldSchema)
	for name, raw := range rawFields {
		fieldMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		fields[name] = parseMetadataFieldSchema(fieldMap)
	}

	if len(fields) == 0 {
		return MetadataSchemaConfig{Mode: "none"}
	}

	return MetadataSchemaConfig{Mode: mode, Fields: fields}
}

// parseMetadataFieldSchema converts a raw config map into a MetadataFieldSchema.
func parseMetadataFieldSchema(m map[string]interface{}) MetadataFieldSchema {
	schema := MetadataFieldSchema{}

	if t, ok := m["type"].(string); ok {
		schema.Type = MetadataFieldType(t)
	}
	if req, ok := m["required"].(bool); ok {
		schema.Required = req
	}

	if vals, ok := m["values"]; ok {
		switch v := vals.(type) {
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					schema.Values = append(schema.Values, s)
				}
			}
		case string:
			for _, s := range strings.Split(v, ",") {
				if s = strings.TrimSpace(s); s != "" {
					schema.Values = append(schema.Values, s)
				}
			}
		}
	}

	if min, ok := metadataToFloat64(m["min"]); ok {
		schema.Min = &min
	}
	if max, ok := metadataToFloat64(m["max"]); ok {
		schema.Max = &max
	}
	return schema
}

func metadataToFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// ValidateMetadataIfConfigured checks metadata against the configured schema.
//   - "none" mode (or config not initialized): no-op, returns nil.
//   - "warn" mode: prints warnings to stderr and returns nil.
//   - "error" mode: returns the first validation error.
//
// Both the direct (DoltStore/embedded) and proxied-server/domain write paths
// must route through this so schema enforcement cannot diverge by stack
// (beads-lsbu / beads-83h3).
func ValidateMetadataIfConfigured(metadata json.RawMessage) error {
	schema := LoadMetadataSchema()
	if schema.Mode == "none" {
		return nil
	}

	errs := ValidateMetadataSchema(metadata, schema)
	if len(errs) == 0 {
		return nil
	}

	if schema.Mode == "warn" {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "warning: %s\n", e.Error())
		}
		return nil
	}
	return fmt.Errorf("metadata schema violation: %s", errs[0].Error())
}
