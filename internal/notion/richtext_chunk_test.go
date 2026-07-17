package notion

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// richTextObjects extracts the []rich_text objects from a BuildPageProperties
// property value shaped {"rich_text": [...]} or {"title": [...]}.
func richTextItems(t *testing.T, prop interface{}, key string) []map[string]interface{} {
	t.Helper()
	m, ok := prop.(map[string]interface{})
	if !ok {
		t.Fatalf("property type = %T, want map", prop)
	}
	items, ok := m[key].([]map[string]interface{})
	if !ok {
		t.Fatalf("property[%q] type = %T, want []map[string]interface{}", key, m[key])
	}
	return items
}

func richTextContent(t *testing.T, item map[string]interface{}) string {
	t.Helper()
	text, ok := item["text"].(map[string]interface{})
	if !ok {
		t.Fatalf("rich_text item missing text object: %#v", item)
	}
	content, ok := text["content"].(string)
	if !ok {
		t.Fatalf("rich_text text.content type = %T", text["content"])
	}
	return content
}

// beads-ux45: Notion caps a single rich_text object's text.content at 2000
// chars. A description longer than that must be split across multiple
// <=2000-char rich_text objects (content preserved, not truncated), or the
// whole page create/update fails with a 400 validation error.
func TestBuildPagePropertiesChunksLongDescription(t *testing.T) {
	t.Parallel()

	// 5001 chars => must become 3 chunks (2000 + 2000 + 1001), losslessly.
	longDesc := strings.Repeat("a", 5001)
	pushIssue := &PushIssue{
		ID:          "bd-long",
		Title:       "Long body",
		Description: longDesc,
		Status:      "Open",
		Priority:    "Medium",
		IssueType:   "Task",
	}

	props := BuildPageProperties(pushIssue)
	items := richTextItems(t, props[PropertyDescription], "rich_text")

	var reassembled strings.Builder
	for i, item := range items {
		content := richTextContent(t, item)
		if utf8.RuneCountInString(content) > 2000 {
			t.Fatalf("chunk %d has %d runes, exceeds Notion's 2000 cap", i, utf8.RuneCountInString(content))
		}
		reassembled.WriteString(content)
	}
	if got := reassembled.String(); got != longDesc {
		t.Fatalf("reassembled description length = %d, want %d (content must be preserved, not truncated)", len(got), len(longDesc))
	}
	if len(items) < 3 {
		t.Fatalf("expected >=3 chunks for a 5001-char description, got %d", len(items))
	}
}

// A description at/under the limit stays a single rich_text object (no
// behavior change for the common case).
func TestBuildPagePropertiesShortDescriptionSingleChunk(t *testing.T) {
	t.Parallel()

	pushIssue := &PushIssue{
		ID:          "bd-short",
		Description: "Short summary",
		Status:      "Open",
		Priority:    "Medium",
		IssueType:   "Task",
	}
	props := BuildPageProperties(pushIssue)
	items := richTextItems(t, props[PropertyDescription], "rich_text")
	if len(items) != 1 {
		t.Fatalf("short description chunk count = %d, want 1", len(items))
	}
	if got := richTextContent(t, items[0]); got != "Short summary" {
		t.Fatalf("content = %q, want %q", got, "Short summary")
	}
}

// Long titles must chunk too (title is also a rich_text-family property).
func TestBuildPagePropertiesChunksLongTitle(t *testing.T) {
	t.Parallel()

	longTitle := strings.Repeat("t", 4100)
	pushIssue := &PushIssue{ID: "bd-lt", Title: longTitle, Status: "Open", Priority: "Medium", IssueType: "Task"}
	props := BuildPageProperties(pushIssue)
	items := richTextItems(t, props[PropertyTitle], "title")

	var reassembled strings.Builder
	for i, item := range items {
		content := richTextContent(t, item)
		if utf8.RuneCountInString(content) > 2000 {
			t.Fatalf("title chunk %d has %d runes, exceeds 2000", i, utf8.RuneCountInString(content))
		}
		reassembled.WriteString(content)
	}
	if reassembled.String() != longTitle {
		t.Fatalf("reassembled title != original (len %d vs %d)", reassembled.Len(), len(longTitle))
	}
}

// Chunking must split on rune boundaries, never mid-UTF8 (a mid-rune split
// would corrupt the character or be rejected by Notion).
func TestRichTextRequestChunksOnRuneBoundary(t *testing.T) {
	t.Parallel()

	// Multi-byte runes; 2500 of them (> 2000-rune cap) forces at least one
	// chunk boundary that must land between runes, never mid-UTF8.
	multibyte := strings.Repeat("é", 2500) // 'é' is 2 bytes
	items := richTextRequest(multibyte)
	if len(items) < 2 {
		t.Fatalf("expected >=2 chunks for 2500 multibyte runes, got %d", len(items))
	}

	var reassembled strings.Builder
	for _, item := range items {
		content, _ := item["text"].(map[string]interface{})["content"].(string)
		if !utf8.ValidString(content) {
			t.Fatalf("chunk is not valid UTF-8 (split mid-rune)")
		}
		if utf8.RuneCountInString(content) > 2000 {
			t.Fatalf("chunk has %d runes, exceeds 2000", utf8.RuneCountInString(content))
		}
		reassembled.WriteString(content)
	}
	if reassembled.String() != multibyte {
		t.Fatal("reassembled multibyte content != original")
	}
}

// Guard the round-trip: chunked content pulls back as the concatenated whole
// (DataSourceTitle joins multiple RichText items).
func TestChunkedDescriptionRoundTrips(t *testing.T) {
	t.Parallel()

	longDesc := strings.Repeat("x", 3300)
	items := richTextRequest(longDesc)

	// Simulate what Notion echoes back: PlainText per item.
	pulled := make([]RichText, 0, len(items))
	for _, item := range items {
		content, _ := item["text"].(map[string]interface{})["content"].(string)
		pulled = append(pulled, RichText{PlainText: content})
	}
	if got := DataSourceTitle(pulled); got != longDesc {
		t.Fatalf("round-tripped description length = %d, want %d", len(got), len(longDesc))
	}
}
