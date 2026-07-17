package issueops

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func TestDefaultAdaptiveConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultAdaptiveConfig()
	if cfg.MaxCollisionProbability != 0.25 {
		t.Errorf("MaxCollisionProbability = %v, want 0.25", cfg.MaxCollisionProbability)
	}
	if cfg.MinLength != 3 {
		t.Errorf("MinLength = %d, want 3", cfg.MinLength)
	}
	if cfg.MaxLength != 8 {
		t.Errorf("MaxLength = %d, want 8", cfg.MaxLength)
	}
}

func TestComputeAdaptiveLength(t *testing.T) {
	t.Parallel()

	cfg := DefaultAdaptiveConfig()

	t.Run("zero issues stays at min length", func(t *testing.T) {
		t.Parallel()
		if got := ComputeAdaptiveLength(0, cfg); got != cfg.MinLength {
			t.Fatalf("ComputeAdaptiveLength(0) = %d, want MinLength %d", got, cfg.MinLength)
		}
	})

	t.Run("length grows monotonically with issue count", func(t *testing.T) {
		t.Parallel()
		prev := 0
		for _, n := range []int{0, 100, 1000, 10000, 100000, 1000000} {
			got := ComputeAdaptiveLength(n, cfg)
			if got < prev {
				t.Fatalf("ComputeAdaptiveLength(%d) = %d decreased from %d", n, got, prev)
			}
			if got < cfg.MinLength || got > cfg.MaxLength {
				t.Fatalf("ComputeAdaptiveLength(%d) = %d out of [%d,%d]", n, got, cfg.MinLength, cfg.MaxLength)
			}
			prev = got
		}
	})

	t.Run("huge count saturates at max length", func(t *testing.T) {
		t.Parallel()
		if got := ComputeAdaptiveLength(1_000_000_000, cfg); got != cfg.MaxLength {
			t.Fatalf("ComputeAdaptiveLength(1e9) = %d, want MaxLength %d", got, cfg.MaxLength)
		}
	})

	t.Run("stricter probability threshold needs longer ids", func(t *testing.T) {
		t.Parallel()
		loose := AdaptiveIDConfig{MaxCollisionProbability: 0.5, MinLength: 3, MaxLength: 8}
		strict := AdaptiveIDConfig{MaxCollisionProbability: 0.001, MinLength: 3, MaxLength: 8}
		const n = 5000
		if ComputeAdaptiveLength(n, strict) < ComputeAdaptiveLength(n, loose) {
			t.Fatalf("stricter threshold produced a shorter id than a looser one")
		}
	})
}

func TestNewEventID(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id := NewEventID()
		if id == "" {
			t.Fatal("NewEventID returned empty string")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewEventID returned a duplicate: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestToFloat64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     interface{}
		want   float64
		wantOK bool
	}{
		{name: "nil", in: nil, wantOK: false},
		{name: "float64", in: float64(3.5), want: 3.5, wantOK: true},
		{name: "int", in: 7, want: 7, wantOK: true},
		{name: "int64", in: int64(9), want: 9, wantOK: true},
		{name: "string is unsupported", in: "5", wantOK: false},
		{name: "bool is unsupported", in: true, wantOK: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := toFloat64(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("toFloat64(%v) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("toFloat64(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsDoltNothingToCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "nothing to commit", err: errors.New("nothing to commit, working tree clean"), want: true},
		{name: "uppercase nothing to commit", err: errors.New("Nothing To Commit"), want: true},
		{name: "no changes ... commit", err: errors.New("no changes added to commit"), want: true},
		{name: "no changes without commit word", err: errors.New("no changes here"), want: false},
		{name: "unrelated error", err: errors.New("connection refused"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsDoltNothingToCommit(tt.err); got != tt.want {
				t.Fatalf("IsDoltNothingToCommit(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestParseFieldSchema(t *testing.T) {
	t.Parallel()

	t.Run("empty map yields zero schema", func(t *testing.T) {
		t.Parallel()
		got := ParseFieldSchema(map[string]interface{}{})
		if got.Type != "" || got.Required || len(got.Values) != 0 || got.Min != nil || got.Max != nil {
			t.Fatalf("empty map produced non-zero schema: %+v", got)
		}
	})

	t.Run("full schema with []interface{} values and numeric bounds", func(t *testing.T) {
		t.Parallel()
		got := ParseFieldSchema(map[string]interface{}{
			"type":     "enum",
			"required": true,
			"values":   []interface{}{"a", "b", 3}, // non-string 3 is skipped
			"min":      float64(1),
			"max":      10,
		})
		if got.Type != storage.MetadataFieldEnum {
			t.Errorf("Type = %q, want enum", got.Type)
		}
		if !got.Required {
			t.Error("Required = false, want true")
		}
		if len(got.Values) != 2 || got.Values[0] != "a" || got.Values[1] != "b" {
			t.Errorf("Values = %v, want [a b]", got.Values)
		}
		if got.Min == nil || *got.Min != 1 {
			t.Errorf("Min = %v, want 1", got.Min)
		}
		if got.Max == nil || *got.Max != 10 {
			t.Errorf("Max = %v, want 10", got.Max)
		}
	})

	t.Run("comma-separated string values are split and trimmed", func(t *testing.T) {
		t.Parallel()
		got := ParseFieldSchema(map[string]interface{}{
			"type":   "enum",
			"values": " x , y ,, z ",
		})
		want := []string{"x", "y", "z"}
		if len(got.Values) != len(want) {
			t.Fatalf("Values = %v, want %v", got.Values, want)
		}
		for i := range want {
			if got.Values[i] != want[i] {
				t.Fatalf("Values[%d] = %q, want %q", i, got.Values[i], want[i])
			}
		}
	})

	t.Run("missing bounds leave Min/Max nil", func(t *testing.T) {
		t.Parallel()
		got := ParseFieldSchema(map[string]interface{}{"type": "int"})
		if got.Min != nil || got.Max != nil {
			t.Fatalf("expected nil bounds, got Min=%v Max=%v", got.Min, got.Max)
		}
	})
}

func TestParseTypesValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty is nil", in: "", want: nil},
		{name: "whitespace is nil", in: "   ", want: nil},
		{name: "json array", in: `["gate","convoy"]`, want: []string{"gate", "convoy"}},
		{name: "comma-separated fallback", in: "gate, convoy , mol", want: []string{"gate", "convoy", "mol"}},
		{name: "single value", in: "gate", want: []string{"gate"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseTypesValue(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseTypesValue(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseTypesValue(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
