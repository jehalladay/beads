package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL       = "https://api.notion.com/v1"
	DefaultNotionVersion = "2026-03-11"
	DefaultTimeout       = 30 * time.Second
	maxResponseBytes     = 20 * 1024 * 1024
	maxQueryPages        = 50
	maxPageSize          = 100
	// maxRetries is the number of RETRIES after the initial attempt for a
	// transient failure (rate limit / 5xx). Mirrors jira/gitlab/ado.
	maxRetries = 3
	// retryDelay is the base for exponential backoff between retries.
	retryDelay = time.Second
)

// AmbiguousError indicates a non-idempotent request (POST/create) that failed
// with an ambiguous outcome — a transport error, a lost response, or a 5xx that
// Notion may have emitted AFTER writing the resource. Such requests are NOT
// retried, because a blind retry of a create can mint a duplicate external page
// (beads-merm). The caller must treat the create as "may have succeeded" and
// reconcile rather than assuming it failed. Idempotent methods (GET/PUT/PATCH)
// are unaffected and keep retrying. A 429 (rate limit) is a clean rejection —
// nothing was processed — so it stays retryable even for POST.
type AmbiguousError struct {
	Method string
	URL    string
	// Cause is the underlying transport/status error from the single attempt.
	Cause error
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("notion %s %s failed with an ambiguous outcome (not retried to avoid a duplicate create): %v", e.Method, e.URL, e.Cause)
}

func (e *AmbiguousError) Unwrap() error { return e.Cause }

// isIdempotentMethod reports whether an HTTP method is safe to retry after an
// ambiguous failure without risking a duplicate side effect. POST creates a new
// resource on each call (/pages, /databases), so it is the only non-idempotent
// method used by this client. The data_sources query is also POST but
// read-only; treating it as non-idempotent only costs a caller-visible error on
// an ambiguous 5xx (never a duplicate), which is the safe default.
func isIdempotentMethod(method string) bool {
	return method != http.MethodPost
}

type Client struct {
	Token         string
	BaseURL       string
	NotionVersion string
	HTTPClient    *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		Token:         token,
		BaseURL:       DefaultBaseURL,
		NotionVersion: DefaultNotionVersion,
		HTTPClient:    &http.Client{Timeout: DefaultTimeout},
	}
}

func (c *Client) WithHTTPClient(httpClient *http.Client) *Client {
	clone := *c
	clone.HTTPClient = httpClient
	return &clone
}

func (c *Client) WithBaseURL(baseURL string) *Client {
	clone := *c
	clone.BaseURL = strings.TrimSuffix(baseURL, "/")
	return &clone
}

func (c *Client) GetCurrentUser(ctx context.Context) (*User, error) {
	body, err := c.doRequest(ctx, http.MethodGet, "/users/me", nil)
	if err != nil {
		return nil, err
	}
	var user User
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("parse current user response: %w", err)
	}
	return &user, nil
}

func (c *Client) RetrieveDataSource(ctx context.Context, dataSourceID string) (*DataSource, error) {
	body, err := c.doRequest(ctx, http.MethodGet, "/data_sources/"+url.PathEscape(dataSourceID), nil)
	if err != nil {
		return nil, err
	}
	var ds DataSource
	if err := json.Unmarshal(body, &ds); err != nil {
		return nil, fmt.Errorf("parse data source response: %w", err)
	}
	return &ds, nil
}

func (c *Client) RetrieveDatabase(ctx context.Context, databaseID string) (*Database, error) {
	body, err := c.doRequest(ctx, http.MethodGet, "/databases/"+url.PathEscape(databaseID), nil)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := json.Unmarshal(body, &db); err != nil {
		return nil, fmt.Errorf("parse database response: %w", err)
	}
	return &db, nil
}

func (c *Client) CreateDatabase(ctx context.Context, parentPageID, title string) (*Database, error) {
	parentPageID = strings.TrimSpace(parentPageID)
	if parentPageID == "" {
		return nil, fmt.Errorf("parent page ID is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = DefaultDatabaseTitle
	}
	request := map[string]interface{}{
		"parent": map[string]interface{}{
			"type":    "page_id",
			"page_id": parentPageID,
		},
		"title":     richTextRequest(title),
		"is_inline": false,
		"initial_data_source": map[string]interface{}{
			"title":      richTextRequest(title),
			"properties": BuildInitialDataSourceProperties(),
		},
	}
	body, err := c.doRequest(ctx, http.MethodPost, "/databases", request)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := json.Unmarshal(body, &db); err != nil {
		return nil, fmt.Errorf("parse create database response: %w", err)
	}
	return &db, nil
}

func (c *Client) QueryDataSource(ctx context.Context, dataSourceID string) ([]Page, error) {
	var pages []Page
	var cursor string
	for pageNum := 0; pageNum < maxQueryPages; pageNum++ {
		request := map[string]interface{}{
			"page_size":   maxPageSize,
			"result_type": "page",
			"in_trash":    false,
		}
		if cursor != "" {
			request["start_cursor"] = cursor
		}

		body, err := c.doRequest(ctx, http.MethodPost, "/data_sources/"+url.PathEscape(dataSourceID)+"/query", request)
		if err != nil {
			return nil, err
		}
		var resp QueryDataSourceResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse data source query response: %w", err)
		}
		pages = append(pages, resp.Results...)
		if !resp.HasMore || resp.NextCursor == "" {
			return pages, nil
		}
		cursor = resp.NextCursor
	}
	return nil, fmt.Errorf("query pagination exceeded %d pages", maxQueryPages)
}

func (c *Client) CreatePage(ctx context.Context, dataSourceID string, properties map[string]interface{}) (*Page, error) {
	request := map[string]interface{}{
		"parent": map[string]interface{}{
			"type":           "data_source_id",
			"data_source_id": dataSourceID,
		},
		"properties": properties,
	}
	body, err := c.doRequest(ctx, http.MethodPost, "/pages", request)
	if err != nil {
		return nil, err
	}
	var page Page
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse create page response: %w", err)
	}
	return &page, nil
}

func (c *Client) UpdatePage(ctx context.Context, pageID string, properties map[string]interface{}) (*Page, error) {
	request := map[string]interface{}{"properties": properties}
	body, err := c.doRequest(ctx, http.MethodPatch, "/pages/"+url.PathEscape(pageID), request)
	if err != nil {
		return nil, err
	}
	var page Page
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse update page response: %w", err)
	}
	return &page, nil
}

func (c *Client) ArchivePage(ctx context.Context, pageID string, inTrash bool) (*Page, error) {
	body, err := c.doRequest(ctx, http.MethodPatch, "/pages/"+url.PathEscape(pageID), map[string]interface{}{"in_trash": inTrash})
	if err != nil {
		return nil, err
	}
	var page Page
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse archive page response: %w", err)
	}
	return &page, nil
}

type DataSourceResolver interface {
	RetrieveDataSource(ctx context.Context, dataSourceID string) (*DataSource, error)
	RetrieveDatabase(ctx context.Context, databaseID string) (*Database, error)
}

type ResolvedDataSource struct {
	InputID      string
	DataSourceID string
	DataSource   *DataSource
	Database     *Database
	ViewURL      string
}

func ResolveDataSourceReference(ctx context.Context, client DataSourceResolver, ref string) (*ResolvedDataSource, error) {
	if client == nil {
		return nil, fmt.Errorf("notion client is nil")
	}
	identifier := ExtractNotionIdentifier(ref)
	if identifier == "" {
		return nil, fmt.Errorf("could not extract a Notion ID from %q", ref)
	}
	if ds, err := client.RetrieveDataSource(ctx, identifier); err == nil {
		return &ResolvedDataSource{
			InputID:      identifier,
			DataSourceID: ds.ID,
			DataSource:   ds,
			ViewURL:      strings.TrimSpace(ref),
		}, nil
	} else {
		db, dbErr := client.RetrieveDatabase(ctx, identifier)
		if dbErr != nil {
			return nil, fmt.Errorf("resolve %q as data source: %w; as database: %v", ref, err, dbErr)
		}
		if len(db.DataSources) == 0 || strings.TrimSpace(db.DataSources[0].ID) == "" {
			return nil, fmt.Errorf("database %s has no child data sources", db.ID)
		}
		resolvedID := strings.TrimSpace(db.DataSources[0].ID)
		resolvedDS, err := client.RetrieveDataSource(ctx, resolvedID)
		if err != nil {
			return nil, fmt.Errorf("retrieve child data source %s: %w", resolvedID, err)
		}
		return &ResolvedDataSource{
			InputID:      identifier,
			DataSourceID: resolvedID,
			DataSource:   resolvedDS,
			Database:     db,
			ViewURL:      strings.TrimSpace(ref),
		}, nil
	}
}

func (c *Client) doRequest(ctx context.Context, method, path string, requestBody interface{}) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("notion client is nil")
	}
	if strings.TrimSpace(c.Token) == "" {
		return nil, fmt.Errorf("Notion token not configured")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	var payload []byte
	if requestBody != nil {
		p, err := json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		payload = p
	}

	requestURL := path
	if !strings.HasPrefix(requestURL, "http://") && !strings.HasPrefix(requestURL, "https://") {
		requestURL = strings.TrimSuffix(c.BaseURL, "/") + path
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Rebuild the body reader at the top of the loop so a retry after a
		// network error does not send an empty body (the reader may be at EOF).
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = bytes.NewReader(payload)
		}

		req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Notion-Version", c.NotionVersion)
		req.Header.Set("Accept", "application/json")
		if requestBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := httpClient.Do(req) //nolint:gosec // G704: URL is constructed from configured Notion API base, not user input
		if err != nil {
			lastErr = fmt.Errorf("request failed (attempt %d/%d): %w", attempt+1, maxRetries+1, err)
			// A transport error on a non-idempotent request (POST/create) is
			// ambiguous: the server may have processed it before the response
			// was lost. Do not retry — a blind retry can create a duplicate.
			if !isIdempotentMethod(method) {
				return nil, &AmbiguousError{Method: method, URL: requestURL, Cause: lastErr}
			}
			if !sleepBeforeRetry(ctx, retryDelay, attempt, "") {
				return nil, ctx.Err()
			}
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response (attempt %d/%d): %w", attempt+1, maxRetries+1, err)
			// The server returned a status but we could not read the body. For a
			// non-idempotent request the create may have committed — treat as
			// ambiguous rather than retrying into a duplicate.
			if !isIdempotentMethod(method) {
				return nil, &AmbiguousError{Method: method, URL: requestURL, Cause: lastErr}
			}
			if !sleepBeforeRetry(ctx, retryDelay, attempt, "") {
				return nil, ctx.Err()
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}

		// A 5xx on a non-idempotent request is ambiguous: the create may have
		// committed before Notion errored. Do not retry — surface an
		// AmbiguousError so the caller reconciles instead of duplicating. A 429
		// (rate limit) is a clean rejection (not processed), so it stays
		// retryable even for POST.
		isServerError := resp.StatusCode == http.StatusInternalServerError ||
			resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusGatewayTimeout
		if isServerError && !isIdempotentMethod(method) {
			return nil, &AmbiguousError{
				Method: method,
				URL:    requestURL,
				Cause:  notionAPIError(resp.StatusCode, body),
			}
		}

		// Retry on rate-limiting and server errors with exponential backoff.
		// Notion's API is aggressively rate-limited (~3 req/s), so 429 is the
		// normal backpressure signal — respect Retry-After when present.
		retriable := resp.StatusCode == http.StatusTooManyRequests || isServerError
		if retriable && attempt < maxRetries {
			lastErr = fmt.Errorf("transient error %d (attempt %d/%d)", resp.StatusCode, attempt+1, maxRetries+1)
			if !sleepBeforeRetry(ctx, retryDelay, attempt, resp.Header.Get("Retry-After")) {
				return nil, ctx.Err()
			}
			continue
		}

		return nil, notionAPIError(resp.StatusCode, body)
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", maxRetries+1, lastErr)
}

// notionAPIError builds a descriptive error from a non-2xx Notion response,
// preferring the structured {code, message} body when present.
func notionAPIError(statusCode int, body []byte) error {
	var apiErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Message != "" {
		return fmt.Errorf("Notion API error %s (%d): %s", apiErr.Code, statusCode, apiErr.Message)
	}
	return fmt.Errorf("Notion API error (%d): %s", statusCode, strings.TrimSpace(string(body)))
}

// sleepBeforeRetry waits before the next retry attempt. When serverDelay (the
// Retry-After header) parses to a positive number of seconds it is respected
// verbatim (no jitter — honor the server-mandated delay); otherwise it uses
// exponential backoff (retryDelay * 2^attempt) with jitter. Returns false if
// the context is cancelled while waiting.
func sleepBeforeRetry(ctx context.Context, base time.Duration, attempt int, serverDelay string) bool {
	delay := base * time.Duration(1<<uint(attempt))
	useServerDelay := false
	if serverDelay != "" {
		if seconds, err := strconv.Atoi(strings.TrimSpace(serverDelay)); err == nil && seconds >= 0 {
			delay = time.Duration(seconds) * time.Second
			useServerDelay = true
		}
	}
	if !useServerDelay {
		if half := int64(delay / 2); half > 0 {
			delay += time.Duration(rand.Int64N(half)) //nolint:gosec // G404: jitter for retry backoff does not need crypto rand
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}
