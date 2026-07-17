package doltremote

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// Dolt-native schemes pass through untouched.
		{"dolthub native", "dolthub://org/db", "dolthub://org/db"},
		{"file native", "file:///tmp/repo", "file:///tmp/repo"},
		{"aws native", "aws://table/db", "aws://table/db"},
		{"gs native", "gs://bucket/db", "gs://bucket/db"},
		{"git+https already native", "git+https://github.com/o/r", "git+https://github.com/o/r"},
		{"git+ssh already native", "git+ssh://git@host/o/r", "git+ssh://git@host/o/r"},

		// Git URLs get converted via FromGitURL.
		{"https", "https://github.com/o/r.git", "git+https://github.com/o/r.git"},
		{"http", "http://example.com/o/r", "git+http://example.com/o/r"},
		{"ssh scheme", "ssh://git@host/o/r", "git+ssh://git@host/o/r"},
		{"scp style", "git@github.com:o/r.git", "git+ssh://git@github.com/o/r.git"},
		{"windows drive fwd slash", "C:/repos/db", "git+C:/repos/db"},
		{"windows drive backslash", `D:\repos\db`, `git+D:\repos\db`},

		// Unknown / relative paths are returned as-is for dolt to decide.
		{"relative path", "relative/path/db", "relative/path/db"},
		{"bare word", "mydb", "mydb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFromGitURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already git+ prefixed", "git+https://github.com/o/r", "git+https://github.com/o/r"},
		{"https", "https://github.com/o/r.git", "git+https://github.com/o/r.git"},
		{"http", "http://example.com/o/r", "git+http://example.com/o/r"},
		{"ssh scheme", "ssh://git@host/o/r", "git+ssh://git@host/o/r"},
		{"windows drive", "C:/repos/db", "git+C:/repos/db"},
		{"scp style converts to git+ssh", "git@github.com:o/r.git", "git+ssh://git@github.com/o/r.git"},
		// A colon-less path with no recognized scheme falls through to a bare
		// git+ prefix.
		{"fallback bare prefix", "some/local/path", "git+some/local/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromGitURL(tt.in); got != tt.want {
				t.Errorf("FromGitURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsSCPStyleGitURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"scp with user", "git@github.com:o/r.git", true},
		{"host colon path but no @", "github.com:o/r", false},
		{"has slash before colon", "https://github.com:443/o/r", false},
		{"no colon", "git@github.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSCPStyleGitURL(tt.in); got != tt.want {
				t.Errorf("isSCPStyleGitURL(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsWindowsDrivePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"upper drive fwd slash", "C:/repo", true},
		{"lower drive backslash", `d:\repo`, true},
		{"too short", "C:", false},
		{"no colon at index 1", "CD/repo", false},
		{"non-letter drive", "1:/repo", false},
		{"colon but no separator", "C:repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsDrivePath(tt.in); got != tt.want {
				t.Errorf("isWindowsDrivePath(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
