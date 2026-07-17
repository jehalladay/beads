package query

import (
	"strings"
	"testing"
)

// TestNodeMarkerMethods calls the empty node() marker methods on every AST node
// type. They exist only to satisfy the Node interface (a closed sum type) and
// are never invoked by production code, so this pins them at 100% and locks the
// marker set against accidental removal.
func TestNodeMarkerMethods(t *testing.T) {
	// Each concrete Node must implement node(); calling it is a no-op but keeps
	// the marker exercised. A compile-time interface assignment additionally
	// proves each type still satisfies Node.
	var _ Node = (*ComparisonNode)(nil)
	var _ Node = (*AndNode)(nil)
	var _ Node = (*OrNode)(nil)
	var _ Node = (*NotNode)(nil)

	(&ComparisonNode{}).node()
	(&AndNode{}).node()
	(&OrNode{}).node()
	(&NotNode{}).node()
}

// TestParserPeekCachesToken verifies Parser.peek: the first call reads a token
// from the lexer and caches it; the second call returns the cached token
// without advancing the lexer (the peeked != nil branch). peek() is not used by
// the production parse path, so this is the only exercise of that branch.
func TestParserPeekCachesToken(t *testing.T) {
	p := NewParser("status=open")

	first, err := p.peek()
	if err != nil {
		t.Fatalf("first peek: %v", err)
	}
	second, err := p.peek()
	if err != nil {
		t.Fatalf("second peek: %v", err)
	}
	if first != second {
		t.Errorf("peek not idempotent: first=%+v second=%+v", first, second)
	}
	if first.Type != TokenIdent || first.Value != "status" {
		t.Errorf("peek() = %+v, want the leading 'status' identifier", first)
	}
	// The cached token must still be consumed by the next advance().
	if err := p.advance(); err != nil {
		t.Fatalf("advance after peek: %v", err)
	}
	if p.current != first {
		t.Errorf("advance after peek current = %+v, want cached %+v", p.current, first)
	}
	if p.peeked != nil {
		t.Error("peeked should be cleared after advance consumes it")
	}
}

// TestParseNot_AdvanceErrorAfterNOT drives the error branch in parseNot where
// advancing past the NOT token fails because the following token is a lexer
// error (a bare '!' is an invalid character). This reaches the "advance after
// NOT errors" arm without a valid operand.
func TestParseNot_AdvanceErrorAfterNOT(t *testing.T) {
	// "NOT !" — NOT parses, then advance tries to lex '!' which is an error.
	_, err := NewParser("NOT !").Parse()
	if err == nil {
		t.Fatal("Parse(\"NOT !\") = nil error, want lexer error advancing past NOT")
	}
}

// TestParseNot_OperandError drives the parseNot branch where the recursive
// operand parse (after a successful advance past NOT) fails.
func TestParseNot_OperandError(t *testing.T) {
	// "NOT =" — advance past NOT succeeds ('=' lexes fine), then parsePrimary/
	// parseComparison rejects a leading '=' (expected field name).
	_, err := NewParser("NOT =").Parse()
	if err == nil {
		t.Fatal("Parse(\"NOT =\") = nil error, want operand parse error")
	}
	if !strings.Contains(err.Error(), "field name") {
		t.Errorf("error = %q, want an 'expected field name' operand error", err)
	}
}

// TestParsePrimary_AdvanceErrorAfterLParen drives the parsePrimary branch where
// advancing past '(' fails because the next token is a lexer error.
func TestParsePrimary_AdvanceErrorAfterLParen(t *testing.T) {
	// "(!" — '(' consumed, advance tries to lex bare '!' → lexer error.
	_, err := NewParser("(!").Parse()
	if err == nil {
		t.Fatal("Parse(\"(!\") = nil error, want lexer error advancing past '('")
	}
}

// TestParsePrimary_InnerExprError drives the parsePrimary branch where the
// parenthesized inner expression fails to parse.
func TestParsePrimary_InnerExprError(t *testing.T) {
	// "( )" — after '(', parseOr sees ')' (not a field) and errors.
	_, err := NewParser("( )").Parse()
	if err == nil {
		t.Fatal("Parse(\"( )\") = nil error, want inner-expression parse error")
	}
}

// TestParsePrimary_AdvanceErrorAfterRParen drives the parsePrimary branch where
// advancing past the closing ')' fails because a lexer error immediately
// follows it.
func TestParsePrimary_AdvanceErrorAfterRParen(t *testing.T) {
	// "(status=open)!" — the group parses and ')' is matched; the advance past
	// ')' then tries to lex bare '!' → lexer error.
	_, err := NewParser("(status=open)!").Parse()
	if err == nil {
		t.Fatal("Parse(\"(status=open)!\") = nil error, want lexer error advancing past ')'")
	}
}
