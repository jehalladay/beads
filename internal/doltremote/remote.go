package doltremote

import "strings"

// NativeSchemes are URL schemes that Dolt understands natively and should not
// be converted through FromGitURL.
var NativeSchemes = []string{
	"dolthub://",
	"file://",
	"aws://",
	"gs://",
	"git+https://",
	"git+ssh://",
	"git+http://",
	"git+file://",
}

// Normalize converts a remote URL to a Dolt-compatible format.
// Dolt-native URLs (dolthub://, file://, aws://, gs://, git+...) are returned
// as-is. Git URLs (https://, ssh://, git@...) are converted via FromGitURL.
// Unknown schemes are returned as-is and let dolt clone decide.
func Normalize(url string) string {
	for _, scheme := range NativeSchemes {
		if strings.HasPrefix(url, scheme) {
			return url
		}
	}
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") ||
		strings.HasPrefix(url, "ssh://") {
		return FromGitURL(url)
	}
	if isWindowsDrivePath(url) {
		return FromGitURL(url)
	}
	if isSCPStyleGitURL(url) {
		return FromGitURL(url)
	}
	return url
}

// FromGitURL converts a git remote URL to Dolt's remote format.
// HTTPS URLs get "git+" prefix: https://... -> git+https://...
// SCP-style SSH URLs are converted: git@host:path -> git+ssh://git@host/path
// SSH URLs get "git+" prefix: ssh://... -> git+ssh://...
// URLs that already have "git+" prefix are returned as-is.
func FromGitURL(url string) string {
	if strings.HasPrefix(url, "git+") {
		return url
	}
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		return "git+" + url
	}
	if strings.HasPrefix(url, "ssh://") {
		return "git+" + url
	}
	if isWindowsDrivePath(url) {
		return "git+" + url
	}
	// Only convert genuine SCP-style git URLs (user@host:path). Gate on the same
	// isSCPStyleGitURL predicate Normalize uses (which requires '@'), so the two
	// helpers agree: without this, FromGitURL mangled host:port ("localhost:8080"
	// → "git+ssh://localhost/8080") and Windows drive-relative ("C:relative")
	// strings that Normalize correctly leaves alone (beads-0l14).
	if isSCPStyleGitURL(url) {
		idx := strings.Index(url, ":")
		return "git+ssh://" + url[:idx] + "/" + url[idx+1:]
	}
	return "git+" + url
}

func isSCPStyleGitURL(url string) bool {
	if idx := strings.Index(url, ":"); idx > 0 && !strings.Contains(url[:idx], "/") && strings.Contains(url, "@") {
		return true
	}
	return false
}

func isWindowsDrivePath(path string) bool {
	if len(path) < 3 || path[1] != ':' {
		return false
	}
	drive := path[0]
	return ((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) &&
		(path[2] == '/' || path[2] == '\\')
}
