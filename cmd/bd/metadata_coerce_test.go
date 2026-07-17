package main

import (
	"encoding/json"
	"testing"
)

// toJSONValue auto-types metadata values (number/bool/null/string). It must
// only coerce a numeric-looking string to a JSON number when that number
// ROUND-TRIPS losslessly — otherwise a big integer ("snowflake"/gh:run ID,
// 18-20 digits) or a whitespace-padded value is silently corrupted to a lossy
// float or has its type/format changed (beads-nj8y).
func TestToJSONValue_LosslessCoercion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // exact JSON encoding expected
	}{
		// Preserved coercions (blessed feature — must keep working).
		{"small int", "5", "5"},
		{"zero", "0", "0"},
		{"negative", "-3", "-3"},
		{"simple float", "1.5", "1.5"},
		{"bool true", "true", "true"},
		{"bool false", "false", "false"},
		{"null", "null", "null"},
		// Corruption cases — must stay STRING.
		{"big integer id", "123456789012345678901234567890", `"123456789012345678901234567890"`},
		{"20-digit snowflake", "12345678901234567890", `"12345678901234567890"`},
		{"whitespace padded", "  3  ", `"  3  "`},
		{"trailing space", "3 ", `"3 "`},
		{"trailing-zero float", "5.0", `"5.0"`},
		// Non-numeric — string (unchanged behavior).
		{"version string", "1.2.3", `"1.2.3"`},
		{"plain word", "platform", `"platform"`},
		{"leading zero id", "007", `"007"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(toJSONValue(tt.in))
			if got != tt.want {
				t.Errorf("toJSONValue(%q) = %s, want %s", tt.in, got, tt.want)
			}
			// Whatever we produce must be valid JSON.
			if !json.Valid([]byte(got)) {
				t.Errorf("toJSONValue(%q) = %s is not valid JSON", tt.in, got)
			}
		})
	}
}

// A coerced number must survive a marshal→unmarshal round-trip without
// precision loss (the concrete corruption symptom).
func TestToJSONValue_BigIntNoPrecisionLoss(t *testing.T) {
	const bigID = "123456789012345678901234567890"
	data := map[string]json.RawMessage{"id": toJSONValue(bigID)}
	out, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, ok := back["id"].(string); !ok || got != bigID {
		t.Errorf("big id round-trip = %v (%T), want string %q — precision must be preserved", back["id"], back["id"], bigID)
	}
}
