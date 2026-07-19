package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/steveyegge/beads/internal/storage/kvkeys"
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
	return outputJSONTo(os.Stdout, v)
}

// outputJSONTo emits schema_version-wrapped --json output to an explicit writer,
// so callers that hold a cobra command's OutOrStdout() (rather than os.Stdout
// directly) still get the wrapWithSchemaVersion envelope + deprecation notice.
// outputJSON is the os.Stdout convenience wrapper (beads-weyi: the notion
// writeNotionJSON callers pass cmd.OutOrStdout(), which their tests redirect to
// a buffer — writing to os.Stdout produced empty output under test).
func outputJSONTo(w io.Writer, v interface{}) error {
	wrapped := wrapWithSchemaVersion(v)
	encoder := json.NewEncoder(w)
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

// warnReservedUserMapKeys announces on stderr that a user-controlled key in a
// flat map[string]string --json payload collides with a reserved key
// wrapWithSchemaVersion injects (schema_version always; data under
// BD_JSON_ENVELOPE=1) and will be overwritten when the map is emitted flat.
//
// The z0fe write-guard (bd kv set / bd remember --key) stops NEW colliding keys
// from being stored, but a key stored BEFORE the guard landed still sits in the
// DB and is silently clobbered on read (`bd kv list --json` / `bd memories
// --json` emit a flat map). This read-side warning turns that residual silent
// data-loss into a visible note (the value is still readable via the singular
// get: `bd kv get <key>` / `bd recall <key>`), while preserving the tested
// flat-map read contract (no nesting). It mirrors config.go's config-key warn.
// getCmd is the singular retrieval hint shown to the operator (e.g. "kv get").
// Non-fatal: callers still emit the map and exit 0.
func warnReservedUserMapKeys(m map[string]string, getCmd string) {
	for k := range m {
		if kvkeys.IsReservedJSONKey(k) {
			fmt.Fprintf(os.Stderr, "Warning: key %q collides with a bd --json envelope key and is overwritten in the flat --json output; read it with 'bd %s %s'\n", k, getCmd, k)
		}
	}
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
