package recipes

import (
	"strings"
	"testing"
)

// TestContentForPath exercises every branch of ContentForPath: the two TypeFile
// paths (explicit content vs. default Template fallback), the TypeMultiFile
// success + two error paths, and the unsupported-type default. Pure function,
// no DB/network.
func TestContentForPath(t *testing.T) {
	tests := []struct {
		name    string
		recipe  Recipe
		path    string
		want    string
		wantErr bool
		errHas  string
	}{
		{
			name:   "file with explicit content",
			recipe: Recipe{Name: "custom", Type: TypeFile, Content: "hello"},
			path:   "AGENTS.md",
			want:   "hello",
		},
		{
			name:   "file without content falls back to default Template",
			recipe: Recipe{Name: "default", Type: TypeFile},
			path:   "CLAUDE.md",
			want:   Template,
		},
		{
			name: "multifile hit returns per-path content",
			recipe: Recipe{Name: "aider", Type: TypeMultiFile, Contents: map[string]string{
				"a.md": "content-a",
				"b.md": "content-b",
			}},
			path: "b.md",
			want: "content-b",
		},
		{
			name:    "multifile with empty contents errors",
			recipe:  Recipe{Name: "empty", Type: TypeMultiFile},
			path:    "a.md",
			wantErr: true,
			errHas:  "no file contents",
		},
		{
			name:    "multifile with missing path key errors",
			recipe:  Recipe{Name: "aider", Type: TypeMultiFile, Contents: map[string]string{"a.md": "x"}},
			path:    "missing.md",
			wantErr: true,
			errHas:  "no content for missing.md",
		},
		{
			name:    "unsupported type errors",
			recipe:  Recipe{Name: "weird", Type: RecipeType("bogus")},
			path:    "a.md",
			wantErr: true,
			errHas:  "unsupported type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ContentForPath(tt.recipe, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ContentForPath() = %q, want error", got)
				}
				if tt.errHas != "" && !strings.Contains(err.Error(), tt.errHas) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.errHas)
				}
				return
			}
			if err != nil {
				t.Fatalf("ContentForPath() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ContentForPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
