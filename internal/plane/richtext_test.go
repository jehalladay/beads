package plane

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name     string
		md       string
		contains []string
		excludes []string
	}{
		{
			name:     "empty input yields empty output",
			md:       "",
			contains: nil,
		},
		{
			name:     "whitespace-only input yields empty output",
			md:       "   \n\t  ",
			contains: nil,
		},
		{
			name:     "paragraph",
			md:       "Hello, Plane.",
			contains: []string{"<p>Hello, Plane.</p>"},
		},
		{
			name:     "heading and list",
			md:       "# Title\n\n- one\n- two",
			contains: []string{"<h1>Title</h1>", "<ul>", "<li>one</li>", "<li>two</li>"},
		},
		{
			name:     "code block survives",
			md:       "```\nfmt.Println(\"hi\")\n```",
			contains: []string{"<pre><code>", "fmt.Println"},
		},
		{
			name:     "link renders as anchor",
			md:       "[beads](https://example.com/beads)",
			contains: []string{`<a href="https://example.com/beads"`, ">beads</a>"},
		},
		{
			name:     "raw HTML is not passed through",
			md:       "before <script>alert(1)</script> after",
			excludes: []string{"<script>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MarkdownToHTML(tt.md)
			if err != nil {
				t.Fatalf("MarkdownToHTML(%q) error: %v", tt.md, err)
			}
			if len(tt.contains) == 0 && len(tt.excludes) == 0 {
				if got != "" {
					t.Errorf("MarkdownToHTML(%q) = %q, want empty", tt.md, got)
				}
				return
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("MarkdownToHTML(%q) = %q, missing %q", tt.md, got, want)
				}
			}
			for _, banned := range tt.excludes {
				if strings.Contains(got, banned) {
					t.Errorf("MarkdownToHTML(%q) = %q, must not contain %q", tt.md, got, banned)
				}
			}
		})
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains []string
		excludes []string
	}{
		{
			name:     "empty input yields empty output",
			html:     "",
			contains: nil,
		},
		{
			name:     "paragraph",
			html:     "<p>Hello, beads.</p>",
			contains: []string{"Hello, beads."},
		},
		{
			name:     "heading converts to markdown heading",
			html:     "<h2>Design</h2><p>body</p>",
			contains: []string{"## Design", "body"},
		},
		{
			name:     "list converts to markdown list",
			html:     "<ul><li>one</li><li>two</li></ul>",
			contains: []string{"- one", "- two"},
		},
		{
			name:     "script tags are sanitized away",
			html:     `<p>safe</p><script>alert("xss")</script>`,
			contains: []string{"safe"},
			excludes: []string{"alert", "script"},
		},
		{
			name:     "event handlers are sanitized away",
			html:     `<p onclick="evil()">text</p>`,
			contains: []string{"text"},
			excludes: []string{"onclick", "evil"},
		},
		{
			name:     "anchor converts to markdown link",
			html:     `<a href="https://example.com/x">x</a>`,
			contains: []string{"[x](https://example.com/x)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HTMLToMarkdown(tt.html)
			if err != nil {
				t.Fatalf("HTMLToMarkdown(%q) error: %v", tt.html, err)
			}
			if len(tt.contains) == 0 && len(tt.excludes) == 0 {
				if got != "" {
					t.Errorf("HTMLToMarkdown(%q) = %q, want empty", tt.html, got)
				}
				return
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("HTMLToMarkdown(%q) = %q, missing %q", tt.html, got, want)
				}
			}
			for _, banned := range tt.excludes {
				if strings.Contains(got, banned) {
					t.Errorf("HTMLToMarkdown(%q) = %q, must not contain %q", tt.html, got, banned)
				}
			}
		})
	}
}

func TestRichTextRoundTrip(t *testing.T) {
	// Markdown -> HTML -> Markdown must preserve the document structure
	// (not necessarily byte-identical, but structurally equivalent).
	md := "# Title\n\nIntro paragraph.\n\n- item one\n- item two\n\n```\ncode here\n```"
	html, err := MarkdownToHTML(md)
	if err != nil {
		t.Fatalf("MarkdownToHTML error: %v", err)
	}
	back, err := HTMLToMarkdown(html)
	if err != nil {
		t.Fatalf("HTMLToMarkdown error: %v", err)
	}
	for _, want := range []string{"# Title", "Intro paragraph.", "- item one", "- item two", "code here"} {
		if !strings.Contains(back, want) {
			t.Errorf("round trip lost %q; got:\n%s", want, back)
		}
	}
}
