// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func handleResponseError(operation string, statusCode int, body string) diag.Diagnostics {
	return diag.Diagnostics{
		diag.NewErrorDiagnostic("API Error", fmt.Sprintf("API returned status %d during %s: %s", statusCode, operation, body)),
	}
}

func handleJSONError(operation string, err error) diag.Diagnostics {
	return diag.Diagnostics{
		diag.NewErrorDiagnostic("JSON Error", fmt.Sprintf("Failed to parse JSON during %s: %v", operation, err)),
	}
}

// convertTerraformToAPI converts Terraform framework types to simple Go types for JSON marshaling
// This handles types.String -> string, types.Int64 -> int64, etc.
func convertTerraformToAPI(src interface{}) interface{} {
	if src == nil {
		return nil
	}

	srcValue := reflect.ValueOf(src)
	if srcValue.Kind() == reflect.Ptr {
		if srcValue.IsNil() {
			return nil
		}
		srcValue = srcValue.Elem()
	}

	switch srcValue.Type() {
	// Handle Terraform framework types
	case reflect.TypeOf(types.String{}):
		tfString := srcValue.Interface().(types.String)
		if tfString.IsNull() || tfString.IsUnknown() {
			return nil
		}
		return tfString.ValueString()

	case reflect.TypeOf(types.Int64{}):
		tfInt64 := srcValue.Interface().(types.Int64)
		if tfInt64.IsNull() || tfInt64.IsUnknown() {
			return nil
		}
		return tfInt64.ValueInt64()

	case reflect.TypeOf(types.Bool{}):
		tfBool := srcValue.Interface().(types.Bool)
		if tfBool.IsNull() || tfBool.IsUnknown() {
			return nil
		}
		return tfBool.ValueBool()

	case reflect.TypeOf(types.Float64{}):
		tfFloat64 := srcValue.Interface().(types.Float64)
		if tfFloat64.IsNull() || tfFloat64.IsUnknown() {
			return nil
		}
		return tfFloat64.ValueFloat64()

	case reflect.TypeOf(types.Number{}):
		tfNumber := srcValue.Interface().(types.Number)
		if tfNumber.IsNull() || tfNumber.IsUnknown() {
			return nil
		}
		// Convert types.Number to float64 for JSON serialization
		bigFloat := tfNumber.ValueBigFloat()
		if bigFloat == nil {
			return nil
		}
		float64Value, _ := bigFloat.Float64()
		return float64Value
	}

	// Handle structs by recursively converting fields
	if srcValue.Kind() == reflect.Struct {
		result := make(map[string]interface{})
		srcType := srcValue.Type()

		for i := 0; i < srcValue.NumField(); i++ {
			field := srcType.Field(i)
			fieldValue := srcValue.Field(i)

			if !fieldValue.CanInterface() {
				continue
			}

			jsonTag := field.Tag.Get("json")
			if jsonTag == "" || jsonTag == "-" {
				jsonTag = field.Tag.Get("tfsdk")
			}
			if jsonTag == "" {
				jsonTag = field.Name
			}

			if commaIdx := len(jsonTag); commaIdx > 0 {
				for j, char := range jsonTag {
					if char == ',' {
						commaIdx = j
						break
					}
				}
				jsonTag = jsonTag[:commaIdx]
			}

			convertedValue := convertTerraformToAPI(fieldValue.Interface())

			if convertedValue != nil {
				result[jsonTag] = convertedValue
			}
		}

		return result
	}

	return srcValue.Interface()
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

// updateTerraformFromAPI updates Terraform framework types from simple Go types (JSON response)
func updateTerraformFromAPI(dst interface{}, src map[string]interface{}) {
	dstValue := reflect.ValueOf(dst)
	if dstValue.Kind() != reflect.Ptr || dstValue.IsNil() {
		return
	}

	dstValue = dstValue.Elem()
	if dstValue.Kind() != reflect.Struct {
		return
	}

	dstType := dstValue.Type()

	for i := 0; i < dstValue.NumField(); i++ {
		field := dstType.Field(i)
		fieldValue := dstValue.Field(i)

		if !fieldValue.CanSet() {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			jsonTag = field.Tag.Get("tfsdk")
		}
		if jsonTag == "" {
			jsonTag = field.Name
		}

		if commaIdx := len(jsonTag); commaIdx > 0 {
			for j, char := range jsonTag {
				if char == ',' {
					commaIdx = j
					break
				}
			}
			jsonTag = jsonTag[:commaIdx]
		}

		apiValue, exists := src[jsonTag]
		if !exists {
			continue
		}

		switch fieldValue.Type() {
		case reflect.TypeOf(types.String{}):
			if apiValue == nil {
				fieldValue.Set(reflect.ValueOf(types.StringNull()))
			} else if str, ok := apiValue.(string); ok {
				// Normalize JSON for filter_expression_json field
				if jsonTag == "filter_expression_json" {
					if normalizedStr, err := normalizeJSON(str); err == nil {
						fieldValue.Set(reflect.ValueOf(types.StringValue(normalizedStr)))
					} else {
						fieldValue.Set(reflect.ValueOf(types.StringValue(str)))
					}
				} else {
					fieldValue.Set(reflect.ValueOf(types.StringValue(str)))
				}
			}

		case reflect.TypeOf(types.Int64{}):
			if apiValue == nil {
				fieldValue.Set(reflect.ValueOf(types.Int64Null()))
			} else {
				switch v := apiValue.(type) {
				case int64:
					fieldValue.Set(reflect.ValueOf(types.Int64Value(v)))
				case float64:
					fieldValue.Set(reflect.ValueOf(types.Int64Value(int64(v))))
				}
			}

		case reflect.TypeOf(types.Bool{}):
			if apiValue == nil {
				fieldValue.Set(reflect.ValueOf(types.BoolNull()))
			} else if b, ok := apiValue.(bool); ok {
				fieldValue.Set(reflect.ValueOf(types.BoolValue(b)))
			}

		case reflect.TypeOf(types.Float64{}):
			if apiValue == nil {
				fieldValue.Set(reflect.ValueOf(types.Float64Null()))
			} else if f, ok := apiValue.(float64); ok {
				fieldValue.Set(reflect.ValueOf(types.Float64Value(f)))
			}

		case reflect.TypeOf(types.Number{}):
			if apiValue == nil {
				fieldValue.Set(reflect.ValueOf(types.NumberNull()))
			} else {
				switch v := apiValue.(type) {
				case int64:
					fieldValue.Set(reflect.ValueOf(types.NumberValue(big.NewFloat(float64(v)))))
				case float64:
					fieldValue.Set(reflect.ValueOf(types.NumberValue(big.NewFloat(v))))
				case int:
					fieldValue.Set(reflect.ValueOf(types.NumberValue(big.NewFloat(float64(v)))))
				}
			}
		}
	}
}

// The reflection-based converters above walk a flat model of scalar Terraform
// types. These per-attribute helpers cover what they cannot express: a pointer,
// so an explicit JSON null stays distinguishable from an omitted field, and
// lists.

// stringPointer returns nil for a null or unknown value, so the field is
// serialized as JSON null rather than an empty string.
func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := value.ValueString()
	return &result
}

func numberPointer(value types.Number) *float64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	bigFloat := value.ValueBigFloat()
	if bigFloat == nil {
		return nil
	}
	result, _ := bigFloat.Float64()
	return &result
}

func stringListPointer(ctx context.Context, value types.List) (*[]string, diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	result := []string{}
	diags := value.ElementsAs(ctx, &result, false)
	if diags.HasError() {
		return nil, diags
	}
	return &result, diags
}

func stringValue(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func numberValue(value *float64) types.Number {
	if value == nil {
		return types.NumberNull()
	}
	return types.NumberValue(big.NewFloat(*value))
}

func boolValue(value *bool) types.Bool {
	if value == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*value)
}

func stringListValue(ctx context.Context, value *[]string) (types.List, diag.Diagnostics) {
	if value == nil {
		return types.ListNull(types.StringType), nil
	}
	return types.ListValueFrom(ctx, types.StringType, *value)
}

// preserveEquivalentJSON keeps the configured spelling of a JSON-encoded field
// when the API returns the same document formatted differently, which would
// otherwise read as drift on every plan.
func preserveEquivalentJSON(configured types.String, returned *string) types.String {
	if returned == nil || configured.IsNull() || configured.IsUnknown() {
		return stringValue(returned)
	}

	configuredJSON, configuredErr := normalizeJSON(configured.ValueString())
	returnedJSON, returnedErr := normalizeJSON(*returned)
	if configuredErr == nil && returnedErr == nil && configuredJSON == returnedJSON {
		return configured
	}
	return types.StringValue(*returned)
}

// preserveEquivalentList keeps a null configuration as null when the API
// returns an empty (rather than absent) list for it.
//
// Some list fields have a server-side default of an empty collection that the
// API applies even when the create/update body carries no value, so a field
// the configuration never set comes back as [] rather than null. Terraform's
// consistency check treats those as different values, so without this a
// resource with no such field configured fails every apply.
func preserveEquivalentList(ctx context.Context, configured types.List, returned *[]string) types.List {
	if configured.IsNull() && !configured.IsUnknown() && returned != nil && len(*returned) == 0 {
		return types.ListNull(types.StringType)
	}
	value, _ := stringListValue(ctx, returned)
	return value
}

// preserveEquivalentNumber keeps the configured value when the API echoes the
// same number back.
//
// Terraform parses a configured number at far higher precision than a JSON
// float64 carries, so rebuilding one from the response changes its
// representation without changing its value — and Terraform compares
// representations. A rate of 0.1 would otherwise fail every apply with an
// inconsistent-result error.
func preserveEquivalentNumber(configured types.Number, returned *float64) types.Number {
	if returned == nil || configured.IsNull() || configured.IsUnknown() {
		return numberValue(returned)
	}
	if value := numberPointer(configured); value != nil && *value == *returned {
		return configured
	}
	return numberValue(returned)
}

type VersionResponse struct {
	Id               string `json:"id"`
	CreatedAt        string `json:"created_at"`
	CreatedBy        string `json:"created_by"`
	UsageGroupSetId  string `json:"usage_group_set_id"`
}

type APIClient struct {
	httpClient *HTTPClient
	// Ensures all resources in the same apply use the same version
	versionID string
	versionOnce sync.Once
	versionError error
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
	// Body is the undecoded response body, for callers that need members beyond
	// the shared problem shape.
	Body string
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
		Detail string `json:"detail"`
		Code   string `json:"code"`
		Title  string `json:"title"`
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
	return err
}

// doRequest performs one HTTP+JSON exchange. A non-2xx response comes back as an
// *apiError so callers can branch on the status; diagnostics are reserved for
// failures that are not the API's answer, meaning transport errors and
// undecodable bodies.
func (c *APIClient) doRequest(ctx context.Context, method, endpoint string, requestBody, responseBody interface{}, opts requestOptions) (*apiError, diag.Diagnostics) {
	var body io.Reader

	if requestBody != nil {
		payload := requestBody
		// Terraform framework types are not directly marshalable, so a model
		// passed straight from state is converted first. Callers that build their
		// own request struct already hold plain Go values.
		if !isPlainStruct(requestBody) {
			payload = convertTerraformToAPI(requestBody)
		}

		jsonData, err := json.Marshal(payload)
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

	responseValue := reflect.ValueOf(responseBody)
	if responseValue.Kind() != reflect.Ptr || responseValue.Elem().Kind() != reflect.Struct {
		return nil, nil
	}

	if isPlainStruct(responseBody) {
		if err := json.Unmarshal([]byte(bodyStr), responseBody); err != nil {
			return nil, handleJSONError("unmarshal response", err)
		}
		return nil, nil
	}

	var apiResponse map[string]interface{}
	if err := json.Unmarshal([]byte(bodyStr), &apiResponse); err != nil {
		return nil, handleJSONError("unmarshal response", err)
	}
	updateTerraformFromAPI(responseBody, apiResponse)
	return nil, nil
}

// isPlainStruct reports whether a struct is built from ordinary Go types, as
// opposed to a Terraform framework model. Only the former can go through
// encoding/json directly.
func isPlainStruct(value interface{}) bool {
	structValue := reflect.ValueOf(value)
	if structValue.Kind() == reflect.Ptr {
		if structValue.IsNil() {
			return false
		}
		structValue = structValue.Elem()
	}
	if structValue.Kind() != reflect.Struct {
		return false
	}

	structType := structValue.Type()
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if _, hasJSON := field.Tag.Lookup("json"); !hasJSON {
			continue
		}
		if field.Type.PkgPath() == "" || !strings.Contains(field.Type.String(), "types.") {
			return true
		}
	}
	return false
}

// doJSONRequest handles JSON requests and responses, reporting everything as
// diagnostics. A 404 is a warning rather than an error, which the v1 usage group
// resources rely on; callers that need to act on the status use doRequest.
func (c *APIClient) doJSONRequest(ctx context.Context, method, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	apiErr, diags := c.doRequest(ctx, method, endpoint, requestBody, responseBody, requestOptions{})
	if diags.HasError() {
		return diags
	}
	if apiErr == nil {
		return diags
	}

	if apiErr.StatusCode == http.StatusNotFound {
		return diag.Diagnostics{
			diag.NewWarningDiagnostic("Resource Not Found", fmt.Sprintf("Resource not found at %s", endpoint)),
		}
	}
	return handleResponseError(fmt.Sprintf("%s %s", method, endpoint), apiErr.StatusCode, apiErr.Body)
}

func (c *APIClient) Get(ctx context.Context, endpoint string, responseBody interface{}) diag.Diagnostics {
	return c.doJSONRequest(ctx, "GET", endpoint, nil, responseBody)
}

func (c *APIClient) Post(ctx context.Context, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	return c.doJSONRequest(ctx, "POST", endpoint, requestBody, responseBody)
}

func (c *APIClient) Put(ctx context.Context, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	return c.doJSONRequest(ctx, "PUT", endpoint, requestBody, responseBody)
}

func (c *APIClient) Delete(ctx context.Context, endpoint string) diag.Diagnostics {
	return c.doJSONRequest(ctx, "DELETE", endpoint, nil, nil)
}

func (c *APIClient) GetOrganizationId() string {
	return c.httpClient.organizationId
}

// GetOrCreateVersion creates a new version for the usage group set if one hasn't been created yet
// for the current apply operation. Returns the version ID.
func (c *APIClient) GetOrCreateVersion(ctx context.Context, usageGroupSetId string) (string, diag.Diagnostics) {
	c.versionOnce.Do(func() {
		orgId := c.GetOrganizationId()
		endpoint := fmt.Sprintf("/api/%s/usage-group-sets/%s/versions", orgId, usageGroupSetId)

		versionRequest := map[string]interface{}{}

		var versionResponse VersionResponse
		creationDiags := c.Post(ctx, endpoint, versionRequest, &versionResponse)

		if creationDiags.HasError() {
			c.versionError = fmt.Errorf("failed to create version: %v", creationDiags)
			return
		}
		
		if versionResponse.Id == "" {
			c.versionError = fmt.Errorf("API returned empty version ID")
			return
		}

		c.versionID = versionResponse.Id
	})

	if c.versionError != nil {
		return "", diag.Diagnostics{
			diag.NewErrorDiagnostic(
				"Version Creation Error",
				c.versionError.Error(),
			),
		}
	}

	return c.versionID, diag.Diagnostics{}
}
