package uimd

import (
	"strings"
	"testing"
)

// TestStripOSC8Hyperlinks_NoOSC8 covers the fast-path early return when the
// input contains no OSC 8 open sequence at all.
func TestStripOSC8Hyperlinks_NoOSC8(t *testing.T) {
	in := "plain text with \x1b[1mSGR\x1b[0m but no hyperlinks"
	if got := stripOSCSequences(in); got != in {
		t.Fatalf("stripOSCSequences should return input unchanged, got %q", got)
	}
}

// TestStripOSC8Hyperlinks_BELTerminated covers the BEL ('\a') terminator branch
// of oscSequenceEnd: a complete OSC 8 open + close pair is removed, surrounding
// text preserved.
func TestStripOSC8Hyperlinks_BELTerminated(t *testing.T) {
	// OSC 8 open (params ; URI BEL) LINKTEXT OSC 8 close (empty params BEL).
	in := "before \x1b]8;;https://example.com\aLINKTEXT\x1b]8;;\aafter"
	got := stripOSCSequences(in)
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
	got := stripOSCSequences(in)
	if strings.Contains(got, "\x1b]8;") {
		t.Fatalf("expected ST-terminated OSC 8 stripped, got %q", got)
	}
	if !strings.Contains(got, "x") || !strings.Contains(got, "LINK") || !strings.Contains(got, "y") {
		t.Fatalf("expected content preserved around stripped OSC 8, got %q", got)
	}
}

// TestStripOSC8Hyperlinks_Unterminated covers the branch where an OSC 8 open
// sequence never terminates: oscSequenceEnd returns -1. A sanitizer must NOT
// leave the dangling sequence — including its raw ESC — intact (beads-uq8m).
// So the malformed remainder is stripped to end-of-string while text BEFORE
// the open sequence is preserved. (This corrects the earlier beads-2zqt
// coverage test, which asserted the buggy copy-through behavior.)
func TestStripOSC8Hyperlinks_Unterminated(t *testing.T) {
	// No BEL and no ST after the OSC 8 open — dangling sequence.
	in := "keep\x1b]8;;https://example.com/no-terminator"
	got := stripOSCSequences(in)
	// Text before the dangling open sequence is preserved.
	if !strings.Contains(got, "keep") {
		t.Fatalf("expected leading text preserved, got %q", got)
	}
	// The raw ESC must be gone — a leaked unterminated escape is the defect.
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("expected dangling OSC 8 (incl. raw ESC) stripped, got %q", got)
	}
	// The malformed OSC 8 open marker must not survive.
	if strings.Contains(got, "]8;") {
		t.Fatalf("expected malformed OSC 8 open stripped, got %q", got)
	}
}

// TestStripOSCSequences_NonOSC8 covers the beads-m5yu gap: OSC sequences other
// than OSC 8 — OSC 52 (clipboard write) and OSC 0/2 (window-title set), both
// terminal-injection vectors — must ALSO be stripped, not just hyperlinks. The
// original OSC-8-only helper let these leak their raw ESC on the useANSI &&
// !useHyperlinks render path (no xansi.Strip backstop there).
func TestStripOSCSequences_NonOSC8(t *testing.T) {
	cases := map[string]string{
		"osc52_clipboard_BEL": "keep\x1b]52;c;ZXZpbA==\atail",
		"osc0_title_BEL":      "keep\x1b]0;pwned\atail",
		"osc2_title_ST":       "keep\x1b]2;pwned\x1b\\tail",
		"osc52_unterminated":  "keep\x1b]52;c;ZXZpbA==",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := stripOSCSequences(in)
			if strings.ContainsRune(got, '\x1b') {
				t.Fatalf("expected raw ESC stripped, got %q", got)
			}
			if strings.Contains(got, "]52;") || strings.Contains(got, "]0;") || strings.Contains(got, "]2;") {
				t.Fatalf("expected OSC marker stripped, got %q", got)
			}
			if !strings.Contains(got, "keep") {
				t.Fatalf("expected leading text preserved, got %q", got)
			}
		})
	}
}

// TestStripOSCSequences_PreservesSGR asserts that stripping OSC does NOT remove
// ANSI SGR color/style — SGR uses the CSI introducer "\x1b[" not the OSC "\x1b]",
// so color survives while OSC injection vectors are removed (the whole point of
// the narrow OSC-only strip on the color-capable path).
func TestStripOSCSequences_PreservesSGR(t *testing.T) {
	in := "\x1b[38;5;252mcolored\x1b[m and \x1b]0;title\a plain"
	got := stripOSCSequences(in)
	if !strings.Contains(got, "\x1b[38;5;252m") || !strings.Contains(got, "\x1b[m") {
		t.Fatalf("expected SGR color preserved, got %q", got)
	}
	if strings.Contains(got, "]0;") {
		t.Fatalf("expected OSC title stripped, got %q", got)
	}
}

// TestRenderMarkdown_StripsOSC52OnColorPath is the end-to-end teeth for
// beads-m5yu: on the useANSI && !useHyperlinks path (color on, hyperlinks off —
// the common terminal case, where xansi.Strip does NOT run), a raw OSC 52
// clipboard-write escape in user content must not survive into the output.
func TestRenderMarkdown_StripsOSC52OnColorPath(t *testing.T) {
	withMarkdownEnv(t, map[string]string{
		"CLICOLOR_FORCE":  "1",   // ShouldUseColor -> true (useANSI)
		"TERM":            "dumb", // ShouldUseHyperlinks -> false
		"NO_COLOR":        "",
		"FORCE_HYPERLINK": "",
		"BD_AGENT_MODE":   "",
		"CLAUDE_CODE":     "",
	})

	out := RenderMarkdown("safe\x1b]52;c;ZXZpbA==\atext\n")
	if strings.Contains(out, "]52;") {
		t.Fatalf("OSC 52 clipboard escape leaked into output: %q", out)
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
