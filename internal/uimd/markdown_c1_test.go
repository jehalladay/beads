package uimd

import (
	"strings"
	"testing"
)

// The 8-bit C1 control introducers. U+009B is the single-byte CSI (equivalent
// to "\x1b["); U+009D is the single-byte OSC (equivalent to "\x1b]"). xterm and
// most modern terminals interpret these identically to their 7-bit ESC forms,
// so they are the same clipboard/title/cursor injection vectors that
// beads-m5yu closed for the 7-bit OSC form — but stripOSCSequences only removes
// "\x1b]" and the no-color xansi.Strip backstop leaves C1 intact.
const (
	c1CSI = ''
	c1OSC = ''
)

// TestStripC1Controls_RemovesC1 asserts the helper removes both C1 introducers
// (and the whole C1 range) while preserving surrounding text, ASCII controls we
// keep (newline/tab pass through untouched here — C1 strip is orthogonal), and
// legitimate multibyte UTF-8 that is NOT C1 (emoji, accented letters).
func TestStripC1Controls_RemovesC1(t *testing.T) {
	in := "before" + string(c1OSC) + "52;c;evil\amid" + string(c1CSI) + "2Jafter"
	got := stripC1Controls(in)
	if strings.ContainsRune(got, c1OSC) || strings.ContainsRune(got, c1CSI) {
		t.Fatalf("expected C1 introducers stripped, got %q", got)
	}
	for _, want := range []string{"before", "mid", "after"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q preserved, got %q", want, got)
		}
	}
}

// TestStripC1Controls_PreservesUTF8 guards against the naive-byte-strip
// corruption risk: C1 is UTF-8 0xC2 0x80..0x9F, and other 2-byte runes share
// the 0xC2 lead byte (e.g. U+00E9 'é' is 0xC3 0xA9; U+00A9 '©' is 0xC2 0xA9).
// The rune-decoded strip must keep every non-C1 rune intact.
func TestStripC1Controls_PreservesUTF8(t *testing.T) {
	in := "café © 🚀 —" + string(c1OSC) + "x"
	got := stripC1Controls(in)
	if strings.ContainsRune(got, c1OSC) {
		t.Fatalf("expected C1 stripped, got %q", got)
	}
	for _, want := range []string{"café", "©", "🚀", "—", "x"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q preserved (UTF-8 not corrupted), got %q", want, got)
		}
	}
}

// TestStripC1Controls_FastPathNoC2 covers the fast-path early return for a
// string with no 0xC2 lead byte.
func TestStripC1Controls_FastPathNoC2(t *testing.T) {
	in := "plain ascii \x1b[1mSGR\x1b[0m no c1"
	if got := stripC1Controls(in); got != in {
		t.Fatalf("expected fast-path passthrough, got %q", got)
	}
}

// TestRenderMarkdown_StripsC1OnColorPath is the end-to-end teeth for
// beads-jbw7b on the useANSI path (color on, hyperlinks off — where xansi.Strip
// does NOT run): a C1 OSC clipboard-write escape in user content must not
// survive into the output.
func TestRenderMarkdown_StripsC1OnColorPath(t *testing.T) {
	withMarkdownEnv(t, map[string]string{
		"CLICOLOR_FORCE":  "1",
		"TERM":            "dumb",
		"NO_COLOR":        "",
		"FORCE_HYPERLINK": "",
		"BD_AGENT_MODE":   "",
		"CLAUDE_CODE":     "",
	})
	out := RenderMarkdown("safe" + string(c1OSC) + "52;c;ZXZpbA==\atext\n")
	if strings.ContainsRune(out, c1OSC) {
		t.Fatalf("C1 OSC clipboard escape leaked on color path: %q", out)
	}
}

// TestRenderMarkdown_StripsC1OnNoColorPath covers the no-color/no-hyperlink
// branch, proving the C1 strip closes the gap the xansi.Strip backstop leaves
// (xansi.Strip removes 7-bit ESC sequences but not 8-bit C1 introducers).
func TestRenderMarkdown_StripsC1OnNoColorPath(t *testing.T) {
	withMarkdownEnv(t, map[string]string{
		"NO_COLOR":        "1",
		"TERM":            "dumb",
		"CLICOLOR_FORCE":  "",
		"FORCE_HYPERLINK": "",
		"BD_AGENT_MODE":   "",
		"CLAUDE_CODE":     "",
	})
	out := RenderMarkdown("safe" + string(c1CSI) + "2Jtext\n")
	if strings.ContainsRune(out, c1CSI) {
		t.Fatalf("C1 CSI escape leaked on no-color path: %q", out)
	}
	if !strings.Contains(out, "safe") || !strings.Contains(out, "text") {
		t.Fatalf("expected visible text preserved, got %q", out)
	}
}
