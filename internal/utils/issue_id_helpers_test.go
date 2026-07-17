package utils

import "testing"

// TestIsNumericEdgeCases covers isNumeric branches not exercised by the
// prefix-extraction tests: the empty-string guard and mixed alnum input.
func TestIsNumericEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},    // empty guard
		{"0", true},    // single digit
		{"123", true},  // all digits
		{"12a", false}, // trailing letter
		{"a12", false}, // leading letter
		{" 12", false}, // leading space is not a digit
		{"1.2", false}, // dot is not a digit
	}
	for _, c := range cases {
		if got := isNumeric(c.in); got != c.want {
			t.Errorf("isNumeric(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestIsLikelyHashNonBase36 covers the branch where a character is outside the
// base36/alnum set, forcing an early false return.
func TestIsLikelyHashNonBase36(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"ab", false},        // too short (<3)
		{"abcdefghi", false}, // too long (>8)
		{"a3f", true},        // 3-char base36 with digit
		{"bat", true},        // 3-char all-letter free pass
		{"a3f8", true},       // 4-char with digit
		{"test", false},      // 4-char all-letter, no digit
		{"a_3", false},       // underscore is not base36
		{"a3!", false},       // punctuation is not base36
		{"a 3", false},       // space is not base36
	}
	for _, c := range cases {
		if got := isLikelyHash(c.in); got != c.want {
			t.Errorf("isLikelyHash(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestExtractIssuePrefixLeadingHyphen covers the first-hyphen fallback where the
// suffix is word-like and the first hyphen sits at index 0, yielding "".
func TestExtractIssuePrefixLeadingHyphen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"-a-test", ""},  // leading hyphen, word-like suffix -> firstIdx==0 -> ""
		{"", ""},         // no hyphen
		{"nohyphen", ""}, // no hyphen
		{"bd-", "bd"},    // trailing hyphen returns prefix
	}
	for _, c := range cases {
		if got := ExtractIssuePrefix(c.in); got != c.want {
			t.Errorf("ExtractIssuePrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNaturalCompareIDsLexicographic covers the non-numeric segment comparison
// branches (both the -1 and +1 return paths) plus the length tie-breaker.
func TestNaturalCompareIDsLexicographic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int // sign: -1, 0, +1
	}{
		{"bd-abc", "bd-abd", -1}, // 'c' < 'd' lexicographically
		{"bd-abd", "bd-abc", 1},  // 'd' > 'c'
		{"bd-abc", "bd-abc", 0},  // equal
		{"bd-a", "bd-a-1", -1},   // prefix is shorter -> length tie-breaker
		{"bd-a-1", "bd-a", 1},    // longer -> positive
		{"bd-1", "bd-abc", -1},   // "1" numeric vs "abc" non-numeric -> lexicographic ("1" < "abc")
	}
	sign := func(n int) int {
		switch {
		case n < 0:
			return -1
		case n > 0:
			return 1
		default:
			return 0
		}
	}
	for _, c := range cases {
		if got := sign(NaturalCompareIDs(c.a, c.b)); got != c.want {
			t.Errorf("NaturalCompareIDs(%q, %q) sign = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
