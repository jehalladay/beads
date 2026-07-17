package notion

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// --- refs.go edge branches -------------------------------------------------

func TestCanonicalizeNotionExternalRefEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
		ok   bool
	}{
		{name: "empty", ref: "", ok: false},
		{name: "whitespace only", ref: "   ", ok: false},
		{name: "unparseable url", ref: "https://exa mple.com/\x7f", ok: false},
		{name: "wrong host", ref: "https://evil.com/0123456789abcdef0123456789abcdef", ok: false},
		{name: "notion host but no page id", ref: "https://www.notion.so/no-id-here", ok: false},
		{
			name: "bare notion.so host accepted",
			ref:  "https://notion.so/Page-0123456789abcdef0123456789abcdef",
			want: "https://www.notion.so/0123456789abcdef0123456789abcdef",
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CanonicalizeNotionExternalRef(tt.ref)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractNotionIdentifierEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "hyphenated id passthrough", ref: "01234567-89ab-cdef-0123-456789abcdef", want: "01234567-89ab-cdef-0123-456789abcdef"},
		{name: "unparseable url", ref: "https://exa mple.com/\x7f", want: ""},
		{name: "no id in path", ref: "https://www.notion.so/just-a-title", want: ""},
		{name: "too short to be a page id", ref: "0123456789abcdef", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractNotionIdentifier(tt.ref); got != tt.want {
				t.Fatalf("ExtractNotionIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestNormalizeNotionPageIDRejectsNonHex(t *testing.T) {
	t.Parallel()

	// 32 chars but with non-hex 'z' must be rejected by the hex-digit scan.
	if got := ExtractNotionIdentifier("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); got != "" {
		t.Fatalf("non-hex 32-char id normalized to %q, want empty", got)
	}
	// Uppercase hex should normalize (case-insensitive) to lowercase hyphenated.
	if got := ExtractNotionIdentifier("0123456789ABCDEF0123456789ABCDEF"); got != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("uppercase id = %q", got)
	}
}

// --- mapping.go branches ---------------------------------------------------

func TestParseMappingTimestampBranches(t *testing.T) {
	t.Parallel()

	// Empty -> zero, present=false, no error.
	if ts, present, err := parseMappingTimestamp("  "); err != nil || present || !ts.IsZero() {
		t.Fatalf("empty: ts=%v present=%v err=%v", ts, present, err)
	}

	// RFC3339Nano parses directly.
	if ts, present, err := parseMappingTimestamp("2026-03-19T14:00:00.123456789Z"); err != nil || !present || ts.IsZero() {
		t.Fatalf("nano: ts=%v present=%v err=%v", ts, present, err)
	}

	// RFC3339 (no fractional seconds) falls through to the second parse.
	if ts, present, err := parseMappingTimestamp("2026-03-19T14:00:00Z"); err != nil || !present || ts.IsZero() {
		t.Fatalf("rfc3339: ts=%v present=%v err=%v", ts, present, err)
	}

	// Malformed value returns an error.
	if _, present, err := parseMappingTimestamp("not-a-timestamp"); err == nil || present {
		t.Fatalf("malformed: present=%v err=%v, want error", present, err)
	}
}

func TestTypeToNotionBranches(t *testing.T) {
	t.Parallel()

	config := DefaultMappingConfig()

	// Empty issue type -> default "Task".
	if got := typeToNotion(types.IssueType("  "), config); got != "Task" {
		t.Fatalf("empty type = %q, want Task", got)
	}
	// Mapped type -> configured label.
	if got := typeToNotion(types.TypeBug, config); got != "Bug" {
		t.Fatalf("bug type = %q, want Bug", got)
	}
	// Unmapped (non-empty) type -> default "Task".
	if got := typeToNotion(types.IssueType("mystery"), config); got != "Task" {
		t.Fatalf("unmapped type = %q, want Task", got)
	}
}

func TestPagePropertySelectBranches(t *testing.T) {
	t.Parallel()

	// Nil Select -> empty string.
	if got := pagePropertySelect(PageProperty{}); got != "" {
		t.Fatalf("nil select = %q, want empty", got)
	}
	// Present Select -> its name.
	if got := pagePropertySelect(PageProperty{Select: &SelectOption{Name: "High"}}); got != "High" {
		t.Fatalf("select = %q, want High", got)
	}
}

// --- client.go error branches ---------------------------------------------

func TestDoRequestGuardBranches(t *testing.T) {
	t.Parallel()

	// Nil client.
	var nilClient *Client
	if _, err := nilClient.doRequest(context.Background(), http.MethodGet, "/users/me", nil); err == nil {
		t.Fatal("nil client: expected error")
	}

	// Empty token.
	empty := &Client{BaseURL: "https://api.notion.com/v1"}
	if _, err := empty.GetCurrentUser(context.Background()); err == nil || !strings.Contains(err.Error(), "token not configured") {
		t.Fatalf("empty token err = %v", err)
	}
}

func TestDoRequestMarshalError(t *testing.T) {
	t.Parallel()

	client := NewClient("secret-token")
	// A channel value cannot be JSON-marshaled -> marshal request body error.
	_, err := client.doRequest(context.Background(), http.MethodPost, "/pages", map[string]interface{}{"bad": make(chan int)})
	if err == nil || !strings.Contains(err.Error(), "marshal request body") {
		t.Fatalf("marshal err = %v", err)
	}
}

func TestDoRequestNewRequestError(t *testing.T) {
	t.Parallel()

	client := NewClient("secret-token")
	// A control character in the method makes http.NewRequestWithContext fail.
	_, err := client.doRequest(context.Background(), "BAD\nMETHOD", "/users/me", nil)
	if err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("new request err = %v", err)
	}
}

func TestDoRequestTransportError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close() // close so the connection is refused

	client := NewClient("secret-token").WithBaseURL(url)
	_, err := client.GetCurrentUser(context.Background())
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("transport err = %v", err)
	}
}

func TestClientUnstructuredAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "upstream boom") // not JSON -> unstructured error path
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := client.GetCurrentUser(context.Background())
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "upstream boom") {
		t.Fatalf("unstructured api err = %v", err)
	}
}

func TestClientParseResponseError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "{not json") // 200 but unparseable body
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := client.GetCurrentUser(context.Background())
	if err == nil || !strings.Contains(err.Error(), "parse current user response") {
		t.Fatalf("parse err = %v", err)
	}
}

func TestCreatePageAndUpdatePageAndArchivePage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/pages":
			_, _ = io.WriteString(w, `{"id":"page-new","url":"https://www.notion.so/page-new"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/pages/page-1":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"in_trash"`) {
				_, _ = io.WriteString(w, `{"id":"page-1","in_trash":true}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"page-1"}`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)

	page, err := client.CreatePage(context.Background(), "ds_1", map[string]interface{}{"Name": "x"})
	if err != nil || page.ID != "page-new" {
		t.Fatalf("CreatePage page=%+v err=%v", page, err)
	}

	updated, err := client.UpdatePage(context.Background(), "page-1", map[string]interface{}{"Name": "y"})
	if err != nil || updated.ID != "page-1" {
		t.Fatalf("UpdatePage page=%+v err=%v", updated, err)
	}

	archived, err := client.ArchivePage(context.Background(), "page-1", true)
	if err != nil || archived.ID != "page-1" {
		t.Fatalf("ArchivePage page=%+v err=%v", archived, err)
	}
}

func TestCreateDatabaseValidationBranches(t *testing.T) {
	t.Parallel()

	client := NewClient("secret-token")
	if _, err := client.CreateDatabase(context.Background(), "   ", "Title"); err == nil {
		t.Fatal("blank parent page: expected error")
	}
}

func TestResolveDataSourceReferenceErrorBranches(t *testing.T) {
	t.Parallel()

	// Nil client.
	if _, err := ResolveDataSourceReference(context.Background(), nil, "x"); err == nil {
		t.Fatal("nil client: expected error")
	}

	// Client present but ref has no extractable ID.
	client := NewClient("secret-token").WithBaseURL("http://127.0.0.1:0")
	if _, err := ResolveDataSourceReference(context.Background(), client, "not-a-notion-thing"); err == nil {
		t.Fatal("no id: expected error")
	}
}

func TestResolveDataSourceReferenceBothFail(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"object_not_found","message":"missing"}`)
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := client.doRequest(context.Background(), http.MethodGet, "/data_sources/329e5bf97fae8080bb4ad94e1387655d", nil)
	if err == nil {
		t.Fatal("expected error from 404")
	}

	_, resolveErr := ResolveDataSourceReference(context.Background(), client, "https://www.notion.so/329e5bf97fae8080bb4ad94e1387655d")
	if resolveErr == nil || !strings.Contains(resolveErr.Error(), "as database") {
		t.Fatalf("resolve err = %v", resolveErr)
	}
}

func TestResolveDataSourceReferenceDatabaseNoChildren(t *testing.T) {
	t.Parallel()

	id := "329e5bf9-7fae-8080-bb4a-d94e1387655d"
	compact := "329e5bf97fae8080bb4ad94e1387655d"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data_sources/" + id:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"object_not_found","message":"missing"}`)
		case "/databases/" + id:
			_, _ = io.WriteString(w, `{"id":"`+id+`","data_sources":[]}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient("secret-token").WithBaseURL(server.URL)
	_, err := ResolveDataSourceReference(context.Background(), client, "https://www.notion.so/"+compact)
	if err == nil || !strings.Contains(err.Error(), "no child data sources") {
		t.Fatalf("no children err = %v", err)
	}
}

func TestContextCancellationSurfacesRequestError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient("secret-token").WithBaseURL(server.URL)
	if _, err := client.GetCurrentUser(ctx); err == nil {
		t.Fatal("cancelled context: expected error")
	}
}
