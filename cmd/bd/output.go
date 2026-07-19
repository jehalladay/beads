package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/steveyegge/beads/internal/ui"
)

const JSONSchemaVersion = 1

// NOTE: the reserved-JSON-key set (schema_version / data — what
// wrapWithSchemaVersion injects below) lives in internal/storage/kvkeys
// (kvkeys.ReservedJSONKeys / IsReservedJSONKey), the same package that owns the
// kv/memory key prefixes, so the write-time guards in `bd kv set` and
// `bd remember --key` share one source of truth with these injected keys
// (beads-z0fe).

func jsonEnvelopeEnabled() bool {
	return os.Getenv("BD_JSON_ENVELOPE") == "1"
}

func outputJSON(v interface{}) error {
	wrapped := wrapWithSchemaVersion(v)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(wrapped); err != nil {
		return fmt.Errorf("encoding JSON: %v", err)
	}

	if !jsonEnvelopeEnabled() {
		emitEnvelopeDeprecation()
	}
	return nil
}

func outputJSONRaw(v interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %v", err)
	}
	return nil
}

func wrapWithSchemaVersion(v interface{}) interface{} {
	if jsonEnvelopeEnabled() {
		return map[string]interface{}{
			"schema_version": JSONSchemaVersion,
			"data":           v,
		}
	}

	if v == nil {
		return map[string]interface{}{"schema_version": JSONSchemaVersion}
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return v
	}

	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return v
	}
	m["schema_version"] = JSONSchemaVersion
	return m
}

var envelopeDeprecationEmitted bool

func emitEnvelopeDeprecation() {
	if envelopeDeprecationEmitted || !ui.IsStderrTerminal() {
		return
	}
	envelopeDeprecationEmitted = true
	fmt.Fprintf(os.Stderr,
		"NOTE: bd --json output format will change in v2.0. "+
			"Set BD_JSON_ENVELOPE=1 to opt in early. "+
			"See docs/JSON_SCHEMA.md for migration details.\n")
}

func outputJSONError(err error, code string) error {
	var errObj interface{}
	base := map[string]interface{}{
		"error": err.Error(),
	}
	if code != "" {
		base["code"] = code
	}
	if jsonEnvelopeEnabled() {
		errObj = map[string]interface{}{
			"schema_version": JSONSchemaVersion,
			"data":           base,
		}
	} else {
		base["schema_version"] = JSONSchemaVersion
		errObj = base
	}
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(errObj)
	return &exitError{Code: 1}
}
