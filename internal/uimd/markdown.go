// Package uimd provides markdown rendering for beads CLI output.
// Keep this separate from internal/ui so non-markdown ui consumers do not
// inherit the glamour/chroma dependency graph.
// This package may depend on internal/ui for terminal policy checks, but
// internal/ui must not import internal/uimd.
package uimd

import (
	"os"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/steveyegge/beads/internal/ui"
	"golang.org/x/term"
)

// RenderMarkdown renders markdown text using glamour's terminal style.
// Returns the rendered markdown or the original text if rendering fails.
// Word wraps at terminal width (or 80 columns if width can't be detected).
func RenderMarkdown(markdown string) string {
	if ui.IsAgentMode() {
		return markdown
	}

	// Strip OSC sequences from the INPUT before rendering (beads-m5yu): user
	// content (issue Description/Notes/comments) can carry raw OSC escapes —
	// OSC 52 (clipboard write), OSC 0/2 (window-title set) — which are
	// terminal-injection vectors. Glamour FRAGMENTS an embedded escape (it
	// inserts SGR resets between the ESC and its OSC body), so a post-render
	// OSC strip can no longer see a contiguous "\x1b]" to remove; sanitizing the
	// source first prevents the raw ESC from ever reaching the output on the
	// useANSI && !useHyperlinks path (where the xansi.Strip backstop does not
	// run). Glamour's OWN OSC 8 hyperlink emission is still handled post-render.
	markdown = stripOSCSequences(markdown)

	// Also strip C1 control introducers (beads-jbw7b). stripOSCSequences only
	// removes the 7-bit ESC-introduced forms ("\x1b]"), but xterm-family
	// terminals interpret the 8-bit C1 single-byte introducers identically:
	// U+009D (OSC) and U+009B (CSI) start clipboard/title/cursor sequences with
	// no ESC prefix. Neither stripOSCSequences nor the no-color xansi.Strip
	// backstop removes them, so an imported Description/Notes carrying
	// "52;...\a" would leak a clipboard-write escape on both render paths.
	// Mirror internal/ui.SanitizeForTerminal, which already strips the whole
	// C1 range (U+0080-U+009F); glamour never legitimately emits C1.
	markdown = stripC1Controls(markdown)

	// Cap at 100 chars for readability; wider lines are harder to scan.
	const maxReadableWidth = 100
	wrapWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		wrapWidth = w
	}
	if wrapWidth > maxReadableWidth {
		wrapWidth = maxReadableWidth
	}

	// Markdown rendering and terminal escape emission are separate concerns.
	// Even when ANSI color is unavailable, Glamour's notty style still improves
	// structure for tables, lists, wrapping, and links. ANSI SGR and OSC 8 are
	// stripped below unless their specific terminal capability checks pass.
	useANSI := ui.ShouldUseColor()
	useHyperlinks := ui.ShouldUseHyperlinks()
	options := []glamour.TermRendererOption{
		glamour.WithWordWrap(wrapWidth),
		glamour.WithPreservedNewLines(),
		glamour.WithTableWrap(false),
	}
	if useANSI {
		options = append(options,
			glamour.WithEnvironmentConfig(),
			glamour.WithChromaFormatter("terminal256"),
		)
	} else {
		options = append(options, glamour.WithStandardStyle(styles.NoTTYStyle))
	}

	renderer, err := glamour.NewTermRenderer(options...)
	if err != nil {
		return markdown
	}

	rendered, err := renderer.Render(markdown)
	if err != nil {
		return markdown
	}

	if !useHyperlinks {
		// Strip ALL OSC sequences (not just OSC 8 hyperlinks): rendered user
		// content can carry OSC 52 (clipboard) / OSC 0/2 (title) escapes that
		// would otherwise leak to the terminal on the useANSI path, where the
		// xansi.Strip backstop below does not run (beads-m5yu). SGR color is
		// preserved (it uses "\x1b[", not the OSC "\x1b]" introducer).
		rendered = stripOSCSequences(rendered)
	}
	if !useANSI && !useHyperlinks {
		rendered = xansi.Strip(rendered)
	}

	return rendered
}

// stripOSCSequences removes ALL OSC (Operating System Command) sequences —
// any "\x1b]<params><BEL|ST>" — while leaving ANSI SGR color/style ("\x1b[...")
// intact. It runs on the !useHyperlinks path.
//
// OSC support is separate from ANSI SGR color support, so we keep regular
// styling when color is on and only remove OSC when ShouldUseHyperlinks says
// the terminal is unsafe. Glamour legitimately emits only OSC 8 (hyperlinks),
// but rendered user content (issue Description/Notes/comments) can carry OTHER
// OSC sequences straight through — notably OSC 52 (clipboard write) and OSC 0/2
// (window-title set), both terminal-injection vectors. The original helper
// stripped only the "\x1b]8;" prefix, so those non-8 OSC escapes leaked their
// raw ESC to the terminal on the useANSI && !useHyperlinks path (no xansi.Strip
// backstop there). Stripping the whole OSC class closes that gap while
// preserving color — mirrors internal/ui.SanitizeForTerminal's OSC handling
// (beads-m5yu; extends the OSC-8 unterminated fix beads-uq8m).
func stripOSCSequences(s string) string {
	const oscIntro = "\x1b]"
	if !strings.Contains(s, oscIntro) {
		return s
	}

	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], oscIntro) {
			if end := oscSequenceEnd(s, i+len(oscIntro)); end > i {
				i = end
				continue
			}
			// Unterminated OSC open (no BEL/ST): oscSequenceEnd returned -1.
			// Strip the malformed remainder rather than emitting the raw ESC
			// and leaking the dangling sequence — a sanitizer must not leave an
			// unterminated escape intact (beads-uq8m). The open sequence runs to
			// end-of-string, so everything from here on is the dangling escape.
			break
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// stripC1Controls removes 8-bit C1 control characters (U+0080-U+009F) from s
// while preserving all other printable UTF-8. C1 controls are encoded in UTF-8
// as the two bytes 0xC2 0x80..0x9F, so a naive byte scan would corrupt adjacent
// multibyte runes; this walks the string rune-decoded. Notably strips U+009B
// (CSI) and U+009D (OSC) — the ESC-less 8-bit introducers that stripOSCSequences
// (7-bit "\x1b]" only) and the no-color xansi.Strip backstop both miss
// (beads-jbw7b). Mirrors internal/ui.SanitizeForTerminal's C1 handling.
func stripC1Controls(s string) string {
	// Fast path: C1 code points are UTF-8-encoded as 0xC2 0x80..0x9F, so a
	// string with no 0xC2 lead byte holds no C1 control and needs no rewrite.
	if strings.IndexByte(s, 0xC2) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0x80 && r <= 0x9F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// oscSequenceEnd returns the byte index after an OSC control sequence.
// OSC strings can end with BEL or ST (ESC \); this helper keeps the stripping
// logic local to OSC 8 handling instead of using a broad ANSI stripper that would
// also remove color/style escapes we may still want to preserve.
func oscSequenceEnd(s string, start int) int {
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '\a':
			return i + 1
		case '\x1b':
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
	}
	return -1
}
