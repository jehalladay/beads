package uimd

import (
	"strings"
	"testing"
)

// TestStripOSC8Hyperlinks_NoOSC8 covers the fast-path early return when the
// input contains no OSC 8 open sequence at all.
func TestStripOSC8Hyperlinks_NoOSC8(t *testing.T) {
	in := "plain text with \x1b[1mSGR\x1b[0m but no hyperlinks"
	if got := stripOSC8Hyperlinks(in); got != in {
		t.Fatalf("stripOSC8Hyperlinks should return input unchanged, got %q", got)
	}
}

// TestStripOSC8Hyperlinks_BELTerminated covers the BEL ('\a') terminator branch
// of oscSequenceEnd: a complete OSC 8 open + close pair is removed, surrounding
// text preserved.
func TestStripOSC8Hyperlinks_BELTerminated(t *testing.T) {
	// OSC 8 open (params ; URI BEL) LINKTEXT OSC 8 close (empty params BEL).
	in := "before \x1b]8;;https://example.com\aLINKTEXT\x1b]8;;\aafter"
	got := stripOSC8Hyperlinks(in)
	if strings.Contains(got, "\x1b]8;") {
		t.Fatalf("expected all OSC 8 sequences stripped, got %q", got)
	}
	if !strings.Contains(got, "before ") || !strings.Contains(got, "LINKTEXT") || !strings.Contains(got, "after") {
		t.Fatalf("expected surrounding + link text preserved, got %q", got)
	}
}

// TestStripOSC8Hyperlinks_STTerminated covers the ST ("ESC \\") terminator
// branch of oscSequenceEnd — the alternate legal OSC string terminator.
func TestStripOSC8Hyperlinks_STTerminated(t *testing.T) {
	// Use ESC \ (ST) instead of BEL to close each OSC 8 sequence.
	in := "x\x1b]8;;https://example.com\x1b\\LINK\x1b]8;;\x1b\\y"
	got := stripOSC8Hyperlinks(in)
	if strings.Contains(got, "\x1b]8;") {
		t.Fatalf("expected ST-terminated OSC 8 stripped, got %q", got)
	}
	if !strings.Contains(got, "x") || !strings.Contains(got, "LINK") || !strings.Contains(got, "y") {
		t.Fatalf("expected content preserved around stripped OSC 8, got %q", got)
	}
}

// TestStripOSC8Hyperlinks_Unterminated covers the branch where an OSC 8 open
// sequence never terminates: oscSequenceEnd returns -1, so stripOSC8Hyperlinks
// must fall through and copy the opening bytes verbatim rather than dropping
// the tail of the string.
func TestStripOSC8Hyperlinks_Unterminated(t *testing.T) {
	// No BEL and no ST after the OSC 8 open — dangling sequence.
	in := "keep\x1b]8;;https://example.com/no-terminator"
	got := stripOSC8Hyperlinks(in)
	// The literal bytes are preserved (nothing is silently truncated).
	if !strings.Contains(got, "keep") {
		t.Fatalf("expected leading text preserved, got %q", got)
	}
	if !strings.Contains(got, "no-terminator") {
		t.Fatalf("expected dangling OSC 8 bytes copied through, got %q", got)
	}
}

// TestOSCSequenceEnd_BEL asserts the BEL terminator returns the index past it.
func TestOSCSequenceEnd_BEL(t *testing.T) {
	s := "]8;;u\a"
	if got := oscSequenceEnd(s, 0); got != len(s) {
		t.Fatalf("oscSequenceEnd BEL = %d, want %d", got, len(s))
	}
}

// TestOSCSequenceEnd_ST asserts the ST ("ESC \\") terminator returns the index
// past both bytes.
func TestOSCSequenceEnd_ST(t *testing.T) {
	s := "]8;;u\x1b\\"
	if got := oscSequenceEnd(s, 0); got != len(s) {
		t.Fatalf("oscSequenceEnd ST = %d, want %d", got, len(s))
	}
}

// TestOSCSequenceEnd_LoneESC covers the branch where an ESC is not followed by
// '\\' (so it is not ST) and no terminator ever appears: returns -1.
func TestOSCSequenceEnd_LoneESC(t *testing.T) {
	// A bare ESC at the very end (no following byte) must not be treated as ST.
	s := "]8;;u\x1b"
	if got := oscSequenceEnd(s, 0); got != -1 {
		t.Fatalf("oscSequenceEnd lone-trailing-ESC = %d, want -1", got)
	}
}

// TestOSCSequenceEnd_NoTerminator covers the -1 return when the string ends
// with no BEL and no ST.
func TestOSCSequenceEnd_NoTerminator(t *testing.T) {
	s := "]8;;unterminated"
	if got := oscSequenceEnd(s, 0); got != -1 {
		t.Fatalf("oscSequenceEnd no-terminator = %d, want -1", got)
	}
}

// TestRenderMarkdown_StripsAllEscapesWhenNoColorNoHyperlinks drives the
// RenderMarkdown branch where neither ANSI nor hyperlinks are supported, so the
// broad xansi.Strip runs after OSC 8 stripping — the output must be plain.
func TestRenderMarkdown_StripsAllEscapesWhenNoColorNoHyperlinks(t *testing.T) {
	withMarkdownEnv(t, map[string]string{
		"NO_COLOR":        "1",
		"TERM":            "dumb",
		"CLICOLOR_FORCE":  "",
		"FORCE_HYPERLINK": "",
		"BD_AGENT_MODE":   "",
		"CLAUDE_CODE":     "",
	})

	out := RenderMarkdown("# H\n\n**bold** and `code` and [link](https://example.com)\n")
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("expected all escape sequences stripped, got %q", out)
	}
	if !strings.Contains(out, "bold") || !strings.Contains(out, "example.com") {
		t.Fatalf("expected rendered text content preserved, got %q", out)
	}
}
