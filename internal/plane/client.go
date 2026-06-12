package plane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPerPage    = 100
	maxPerPage        = 1000
	defaultMaxRetries = 3
	maxBackoff        = 30 * time.Second
	errBodyLimit      = 300
)

// errNotFound is an internal sentinel for 404 responses; public lookup
// methods translate it to (nil, nil) per the tracker adapter convention.
var errNotFound = errors.New("plane API: resource not found")

// Client is a minimal Plane CE REST API client scoped to one workspace and
// one project, matching the v1.3.0 API contract. All methods are safe for
// concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	workspace  string
	projectID  string
	perPage    int
	maxRetries int
}

// NewClient creates a Plane API client. baseURL is the instance root
// (e.g. "https://plane.example.com"); workspace is the workspace slug;
// projectID is the target project UUID.
func NewClient(baseURL, apiKey, workspace, projectID string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     apiKey,
		workspace:  workspace,
		projectID:  projectID,
		perPage:    defaultPerPage,
		maxRetries: defaultMaxRetries,
	}
}

// WithHTTPClient returns the client configured with a custom *http.Client.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.httpClient = h
	return c
}

// WithPerPage returns the client configured with a page size for list
// requests, clamped to Plane's runtime maximum (1000).
func (c *Client) WithPerPage(n int) *Client {
	if n < 1 {
		n = 1
	}
	if n > maxPerPage {
		n = maxPerPage
	}
	c.perPage = n
	return c
}

// WithMaxRetries returns the client configured with the maximum number of
// retries for rate-limited (429) and transient server-error responses.
func (c *Client) WithMaxRetries(n int) *Client {
	if n < 0 {
		n = 0
	}
	c.maxRetries = n
	return c
}

// BaseURL returns the normalized instance root URL.
func (c *Client) BaseURL() string { return c.baseURL }

// APIKey returns the configured API key.
func (c *Client) APIKey() string { return c.apiKey }

// Workspace returns the configured workspace slug.
func (c *Client) Workspace() string { return c.workspace }

// ProjectID returns the configured project UUID.
func (c *Client) ProjectID() string { return c.projectID }

// projectPath builds an API path under the configured project.
func (c *Client) projectPath(suffix string) string {
	return fmt.Sprintf("/workspaces/%s/projects/%s/%s", c.workspace, c.projectID, suffix)
}

// doJSON performs one API call with retry handling and decodes the JSON
// response into out (when out is non-nil).
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("plane API %s %s: encoding request: %w", method, path, err)
		}
	}

	fullURL := c.baseURL + "/api/v1" + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
		if err != nil {
			return fmt.Errorf("plane API %s %s: building request: %w", method, path, err)
		}
		req.Header.Set("X-Api-Key", c.apiKey)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("plane API %s %s: %w", method, path, err)
		}

		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retryable && attempt < c.maxRetries {
			wait := retryDelay(resp, attempt)
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			select {
			case <-ctx.Done():
				return fmt.Errorf("plane API %s %s: %w", method, path, ctx.Err())
			case <-time.After(wait):
				continue
			}
		}

		return c.handleResponse(method, path, resp, out)
	}
}

// retryDelay computes how long to wait before retrying, honoring the
// Retry-After header when present and falling back to capped exponential
// backoff.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	delay := time.Second << attempt
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}

// handleResponse maps a terminal HTTP response to a decoded value or a
// typed error.
func (c *Client) handleResponse(method, path string, resp *http.Response, out any) error {
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("plane API %s %s: reading response: %w", method, path, readErr)
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("plane API %s %s: decoding response: %w", method, path, err)
		}
		return nil

	case resp.StatusCode == http.StatusNotFound:
		return errNotFound

	case resp.StatusCode == http.StatusConflict:
		var conflict struct {
			Error string `json:"error"`
			ID    string `json:"id"`
		}
		_ = json.Unmarshal(raw, &conflict)
		return &DuplicateError{ExistingID: conflict.ID, Message: conflict.Error}

	case resp.StatusCode == http.StatusUnauthorized:
		return &AuthError{StatusCode: resp.StatusCode, Detail: errorDetail(raw)}

	case resp.StatusCode == http.StatusForbidden:
		// Plane returns 403 (not 401) for invalid/expired tokens; permission
		// denials use the same status. Both halt sync, so treat any 403 as
		// an auth-layer failure with the server's detail preserved.
		return &AuthError{StatusCode: resp.StatusCode, Detail: errorDetail(raw)}

	default:
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       path,
			Body:       truncate(string(raw), errBodyLimit),
		}
	}
}

// errorDetail extracts the human-readable message from Plane's error bodies,
// which use either {"detail": ...} or {"error": ...}.
func errorDetail(raw []byte) string {
	var body struct {
		Detail string `json:"detail"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	if body.Detail != "" {
		return body.Detail
	}
	if body.Error != "" {
		return body.Error
	}
	return truncate(string(raw), errBodyLimit)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// listPages fetches every page of a paginated collection endpoint.
func listPages[T any](ctx context.Context, c *Client, path string, extraQuery url.Values) ([]T, error) {
	var all []T
	cursor := ""
	for {
		query := url.Values{}
		for k, vs := range extraQuery {
			for _, v := range vs {
				query.Add(k, v)
			}
		}
		query.Set("per_page", strconv.Itoa(c.perPage))
		if cursor != "" {
			query.Set("cursor", cursor)
		}

		var page paginatedResponse[T]
		if err := c.doJSON(ctx, http.MethodGet, path, query, nil, &page); err != nil {
			return all, err
		}
		all = append(all, page.Results...)

		if !page.NextPageResults || page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// GetIssue fetches a work item by UUID. Returns (nil, nil) if it does not
// exist.
func (c *Client) GetIssue(ctx context.Context, issueID string) (*Issue, error) {
	var issue Issue
	err := c.doJSON(ctx, http.MethodGet, c.projectPath("work-items/"+issueID+"/"), nil, nil, &issue)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// GetIssueByExternalID looks up a work item by its external_id and
// external_source pair. Plane short-circuits the list endpoint to a single
// (non-paginated) issue object when both params are present. Returns
// (nil, nil) if no match exists.
func (c *Client) GetIssueByExternalID(ctx context.Context, externalID, externalSource string) (*Issue, error) {
	query := url.Values{}
	query.Set("external_id", externalID)
	query.Set("external_source", externalSource)

	var issue Issue
	err := c.doJSON(ctx, http.MethodGet, c.projectPath("work-items/"), query, nil, &issue)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// GetIssueByIdentifier fetches a work item by its human-readable identifier
// (e.g. "PROJ-7") via the workspace-level endpoint. Returns (nil, nil) if it
// does not exist.
func (c *Client) GetIssueByIdentifier(ctx context.Context, identifier string) (*Issue, error) {
	path := fmt.Sprintf("/workspaces/%s/work-items/%s/", c.workspace, identifier)
	var issue Issue
	err := c.doJSON(ctx, http.MethodGet, path, nil, nil, &issue)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// ListIssues fetches all work items in the project, following pagination.
func (c *Client) ListIssues(ctx context.Context, opts ListIssuesOptions) ([]Issue, error) {
	query := url.Values{}
	if opts.OrderBy != "" {
		query.Set("order_by", opts.OrderBy)
	}
	return listPages[Issue](ctx, c, c.projectPath("work-items/"), query)
}

// CreateIssue creates a work item. A 409 duplicate (same
// external_id/external_source pair) surfaces as *DuplicateError carrying the
// existing issue's UUID.
func (c *Client) CreateIssue(ctx context.Context, payload *IssuePayload) (*Issue, error) {
	var issue Issue
	if err := c.doJSON(ctx, http.MethodPost, c.projectPath("work-items/"), nil, payload, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// UpdateIssue applies a partial update (PATCH) to a work item by UUID.
func (c *Client) UpdateIssue(ctx context.Context, issueID string, payload *IssuePayload) (*Issue, error) {
	var issue Issue
	if err := c.doJSON(ctx, http.MethodPatch, c.projectPath("work-items/"+issueID+"/"), nil, payload, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// ListStates fetches all workflow states of the project.
func (c *Client) ListStates(ctx context.Context) ([]State, error) {
	return listPages[State](ctx, c, c.projectPath("states/"), nil)
}

// ListLabels fetches all labels of the project.
func (c *Client) ListLabels(ctx context.Context) ([]Label, error) {
	return listPages[Label](ctx, c, c.projectPath("labels/"), nil)
}

// CreateLabel creates a project label by name. A duplicate name surfaces as
// *DuplicateError carrying the existing label's UUID.
func (c *Client) CreateLabel(ctx context.Context, name string) (*Label, error) {
	var label Label
	body := map[string]string{"name": name}
	if err := c.doJSON(ctx, http.MethodPost, c.projectPath("labels/"), nil, body, &label); err != nil {
		return nil, err
	}
	return &label, nil
}

// ListComments fetches all comments on a work item, following pagination.
func (c *Client) ListComments(ctx context.Context, issueID string) ([]Comment, error) {
	return listPages[Comment](ctx, c, c.projectPath("work-items/"+issueID+"/comments/"), nil)
}

// CreateComment posts an HTML comment on a work item.
func (c *Client) CreateComment(ctx context.Context, issueID, commentHTML string) (*Comment, error) {
	var comment Comment
	body := map[string]string{"comment_html": commentHTML}
	if err := c.doJSON(ctx, http.MethodPost, c.projectPath("work-items/"+issueID+"/comments/"), nil, body, &comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

// ListProjects fetches all projects in the workspace. Plane has no
// identifier/name filter on this endpoint, so discovery is client-side.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	path := fmt.Sprintf("/workspaces/%s/projects/", c.workspace)
	return listPages[Project](ctx, c, path, nil)
}

// GetProject fetches the configured project.
func (c *Client) GetProject(ctx context.Context) (*Project, error) {
	path := fmt.Sprintf("/workspaces/%s/projects/%s/", c.workspace, c.projectID)
	var p Project
	if err := c.doJSON(ctx, http.MethodGet, path, nil, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
