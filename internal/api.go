// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

type HTTPClient struct {
	client         *http.Client
	baseURL        string
	apiKey         string
	organizationId string
}

func NewHTTPClient(apiKey, organizationId, baseURL string) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Transport: &http.Transport{
				MaxConnsPerHost:     12,  // Allow 12 concurrent connections per host (slightly above Terraform's default parallelism of 10)
				MaxIdleConns:        100, // Maximum idle connections across all hosts
				MaxIdleConnsPerHost: 12,  // Maximum idle connections per host
				IdleConnTimeout:     90 * time.Second,
			},
			Timeout: 90 * time.Second,
		},
		baseURL:        baseURL,
		apiKey:         apiKey,
		organizationId: organizationId,
	}
}

func (c *HTTPClient) buildURL(endpoint string) string {
	return fmt.Sprintf("%s%s", c.baseURL, endpoint)
}

func (c *HTTPClient) makeRequest(ctx context.Context, method, endpoint string, body io.Reader, headers map[string]string) (*http.Response, error) {
	url := c.buildURL(endpoint)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	// The v2 API scopes every request by this header rather than by an
	// organization ID in the path. Sending it unconditionally keeps one code path
	// for both API versions; v1 ignores headers it does not read.
	req.Header.Set("x-tenant-id", c.organizationId)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func readResponseBody(resp *http.Response) (string, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return string(body), nil
}

func handleHTTPError(operation string, err error) diag.Diagnostics {
	return diag.Diagnostics{
		diag.NewErrorDiagnostic("HTTP Request Error", fmt.Sprintf("Failed to %s: %v", operation, err)),
	}
}

func handleJSONError(operation string, err error) diag.Diagnostics {
	return diag.Diagnostics{
		diag.NewErrorDiagnostic("JSON Error", fmt.Sprintf("Failed to parse JSON during %s: %v", operation, err)),
	}
}

// normalizeJSON normalizes a JSON string to ensure consistent key ordering
func normalizeJSON(jsonStr string) (string, error) {
	var jsonObj interface{}
	if err := json.Unmarshal([]byte(jsonStr), &jsonObj); err != nil {
		return jsonStr, err
	}

	normalized, err := json.Marshal(jsonObj)
	if err != nil {
		return jsonStr, err
	}

	return string(normalized), nil
}

// setVersion tracks one usage group set's version for the current apply. See
// APIClient.EnsureVersion.
type setVersion struct {
	once     sync.Once
	recorded bool
	diags    diag.Diagnostics
}

type APIClient struct {
	httpClient *HTTPClient
	// versions holds one entry per usage group set this apply has written to,
	// so every set gets exactly one version rather than only the first set to
	// be touched. Guarded by versionsMu; the setVersion it points at does its
	// own synchronization.
	versions   map[string]*setVersion
	versionsMu sync.Mutex
}

func NewAPIClient(apiKey, organizationId, baseURL string) *APIClient {
	return &APIClient{
		httpClient: NewHTTPClient(apiKey, organizationId, baseURL),
	}
}

// requestOptions carries per-request details that only some callers need.
type requestOptions struct {
	// headers are set in addition to the standard ones. The v2 API's optimistic
	// concurrency runs through If-Match, which varies per request rather than
	// per client.
	headers map[string]string
}

// apiError is a non-2xx response from the API, in the terms a caller needs in
// order to branch on it.
type apiError struct {
	StatusCode int
	// Detail explains the failure: the `detail` member of an
	// application/problem+json body, or the raw body when the response is not a
	// problem document.
	Detail string
	// Code is the v2 problem catalogue code, such as "precondition_failed".
	// Empty for responses that are not problem documents.
	Code string
	// Details are the problem document's `details` members, which narrow a code
	// that covers several situations to the one that fired. A 409 conflict, for
	// instance, means something different for every issue it carries.
	Details []apiErrorDetail
	// Body is the undecoded response body, for callers that need members beyond
	// the shared problem shape.
	Body string
}

// apiErrorDetail is one member of a problem document's `details` array.
type apiErrorDetail struct {
	Field    string `json:"field"`
	Issue    string `json:"issue"`
	Message  string `json:"message"`
	DocsLink string `json:"docs_link"`
}

// issue returns the first detail's machine-readable issue, which is what a
// caller branches on. Empty when the API sent no details.
func (e *apiError) issue() string {
	if len(e.Details) == 0 {
		return ""
	}
	return e.Details[0].Issue
}

// field returns the first detail's field name, which is what a caller branches
// on when a code covers several situations that share one issue. Empty when the
// API sent no details.
func (e *apiError) field() string {
	if len(e.Details) == 0 {
		return ""
	}
	return e.Details[0].Field
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (HTTP %d, %s)", e.Detail, e.StatusCode, e.Code)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Detail, e.StatusCode)
}

// newAPIError builds an apiError from a response body, reading the RFC 9457
// members the v2 API returns when they are present.
func newAPIError(statusCode int, body string) *apiError {
	err := &apiError{StatusCode: statusCode, Detail: body, Body: body}

	var problem struct {
		Detail  string           `json:"detail"`
		Code    string           `json:"code"`
		Title   string           `json:"title"`
		Details []apiErrorDetail `json:"details"`
	}
	if jsonErr := json.Unmarshal([]byte(body), &problem); jsonErr != nil {
		return err
	}
	if problem.Detail != "" {
		err.Detail = problem.Detail
	} else if problem.Title != "" {
		err.Detail = problem.Title
	}
	err.Code = problem.Code
	err.Details = problem.Details
	return err
}

// doRequest performs one HTTP+JSON exchange. A non-2xx response comes back as an
// *apiError so callers can branch on the status; diagnostics are reserved for
// failures that are not the API's answer, meaning transport errors and
// undecodable bodies.
func (c *APIClient) doRequest(ctx context.Context, method, endpoint string, requestBody, responseBody interface{}, opts requestOptions) (*apiError, diag.Diagnostics) {
	var body io.Reader

	if requestBody != nil {
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			return nil, handleJSONError("marshal request", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	resp, err := c.httpClient.makeRequest(ctx, method, endpoint, body, opts.headers)
	if err != nil {
		return nil, handleHTTPError(fmt.Sprintf("%s %s", method, endpoint), err)
	}
	defer resp.Body.Close()

	bodyStr, err := readResponseBody(resp)
	if err != nil {
		return nil, diag.Diagnostics{
			diag.NewErrorDiagnostic("Response Read Error", fmt.Sprintf("Failed to read response body: %v", err)),
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newAPIError(resp.StatusCode, bodyStr), nil
	}

	if responseBody == nil || len(bodyStr) == 0 {
		return nil, nil
	}

	if err := json.Unmarshal([]byte(bodyStr), responseBody); err != nil {
		return nil, handleJSONError("unmarshal response", err)
	}
	return nil, nil
}

// EnsureVersion records a version of a usage group set's groups, once per set
// per apply, before this apply changes any of them.
//
// A version is a frozen copy of the set's groups. Usage group writes change the
// newest version in place and never add one on their own, so without this every
// apply would overwrite the state the previous apply left, and nothing would be
// restorable. Recording one first means an apply's changes land on a fresh copy
// and the state the apply started from survives as a checkpoint — the behavior
// the v1 API gave through POST /versions, kept unchanged.
//
// Terraform offers a provider no apply-level hook, so there is nowhere to do
// this except inside the first write that needs it. The APIClient lives for one
// apply, so per-set state on it is per-apply state.
//
// Concurrent callers for the same set block until the first finishes; callers
// for different sets do not contend. Terraform applies at a default parallelism
// of 10, so several groups in the same set reach this at once.
func (c *APIClient) EnsureVersion(ctx context.Context, usageGroupSetId string) diag.Diagnostics {
	c.versionsMu.Lock()
	if c.versions == nil {
		c.versions = map[string]*setVersion{}
	}
	version, started := c.versions[usageGroupSetId]
	if !started {
		version = &setVersion{}
		c.versions[usageGroupSetId] = version
	}
	c.versionsMu.Unlock()

	version.once.Do(func() {
		var response usageGroupSetVersionResponse
		// No If-Match: the checkpoint should capture the set as it stands when
		// this runs. Sending an ETag read before the apply began would fail on a
		// set another caller has touched since, which is not this call's
		// business — it only records what is there now.
		apiErr, diags := c.doRequest(ctx, http.MethodPost,
			usageGroupSetVersionsEndpoint(usageGroupSetId), nil, &response, requestOptions{})
		if diags.HasError() {
			version.diags = diags
			return
		}
		if apiErr != nil {
			version.diags = diag.Diagnostics{
				usageGroupSetErrors.diagnostic("record a version of the usage group set", apiErr, nil),
			}
			return
		}
		version.recorded = true
	})

	return version.diags
}

// VersionRecorded reports whether this apply has already recorded a version of
// the given set.
//
// It is how a write knows its ETag may be stale through no fault of the user:
// recording a version changes the set, so an ETag read before that no longer
// matches. A caller that gets true re-reads what it is about to write rather
// than sending an ETag this provider itself invalidated. See
// usageGroupSetResource and usageGroupResource for where that happens.
func (c *APIClient) VersionRecorded(usageGroupSetId string) bool {
	c.versionsMu.Lock()
	defer c.versionsMu.Unlock()

	version, started := c.versions[usageGroupSetId]
	return started && version.recorded
}
