package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidateMetadataReadable_nc639 guards the write-path control-char reject
// (beads-nc639). A raw control byte in a metadata JSON string value/key is
// accepted by the Dolt JSON column on write but re-emits unreadable JSON on
// readback, bricking every subsequent bd list/show/export repo-wide. The
// validator must reject such a blob at write; benign values must pass.
func TestValidateMetadataReadable_nc639(t *testing.T) {
	esc := "\x1b" // ESC (OSC-52 injection lead byte)
	bel := "\x07" // BEL
	nul := "\x00" // NUL
	del := "\x7f" // DEL
	tab := "\t"   // even JSON-standard whitespace is a raw control byte
	nl := "\n"

	// mustJSON builds a valid JSON object embedding the given (possibly
	// control-bearing) string as the value of key "k".
	mustJSON := func(t *testing.T, val string) string {
		t.Helper()
		b, err := json.Marshal(map[string]any{"k": val})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}

	cases := []struct {
		name    string
		meta    string
		wantErr bool
	}{
		{"empty", ``, false},
		{"emptyObject", `{}`, false},
		{"plainValue", `{"k":"plain value with spaces"}`, false},
		{"unicodeAndEmoji", `{"k":"café 🚀 naïve"}`, false},
		{"quotedAndSlash", `{"k":"a \"quoted\" b/c"}`, false},
		{"nestedObjectClean", `{"a":{"b":["x","y"]}}`, false},
		{"numberBoolNull", `{"n":42,"b":true,"z":null}`, false},
		{"escInValue", mustJSON(t, "x"+esc+"z"), true},
		{"belInValue", mustJSON(t, "a"+bel+"b"), true},
		{"nulInValue", mustJSON(t, "a"+nul+"b"), true},
		{"delInValue", mustJSON(t, "a"+del+"b"), true},
		{"tabInValue", mustJSON(t, "a"+tab+"b"), true},
		{"newlineInValue", mustJSON(t, "a"+nl+"b"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMetadataReadable(json.RawMessage(tc.meta))
			if tc.wantErr && err == nil {
				t.Fatalf("expected rejection for %q, got nil", tc.meta)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected %q to pass, got: %v", tc.meta, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "control character") {
				t.Errorf("rejection message should name the control character; got: %v", err)
			}
		})
	}

	// Control char in a KEY must also reject.
	t.Run("escInKey", func(t *testing.T) {
		b, err := json.Marshal(map[string]any{"a" + esc + "b": "v"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := ValidateMetadataReadable(json.RawMessage(b)); err == nil {
			t.Fatal("expected rejection for control char in metadata key")
		}
	})

	// Control char nested deep inside an array must reject (recursive walk).
	t.Run("escInNestedArray", func(t *testing.T) {
		b, err := json.Marshal(map[string]any{
			"a": map[string]any{"b": []any{"ok", "x" + esc + "y"}},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := ValidateMetadataReadable(json.RawMessage(b)); err == nil {
			t.Fatal("expected rejection for control char nested in array")
		}
	})
}
