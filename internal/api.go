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
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// HTTPClient wraps the standard http.Client with configuration
type HTTPClient struct {
	client         *http.Client
	baseURL        string
	apiKey         string
	organizationId string
}

// NewHTTPClient creates a new HTTP client with reasonable defaults
// Best practice: Centralize client creation with consistent timeouts and configuration
func NewHTTPClient(apiKey, organizationId, baseURL string) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: 30 * time.Second, // 30 second timeout is reasonable for most API calls
		},
		baseURL:        baseURL,
		apiKey:         apiKey,
		organizationId: organizationId,
	}
}

// buildURL constructs the full URL for API calls
func (c *HTTPClient) buildURL(endpoint string) string {
	return fmt.Sprintf("%s%s", c.baseURL, endpoint)
}

// makeRequest is a low-level method that handles the basic HTTP request/response cycle
// Best practice: Centralize HTTP request logic to avoid code duplication
func (c *HTTPClient) makeRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	url := c.buildURL(endpoint)

	// Create request with context for proper cancellation
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	// Set content type for JSON requests
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
	// Make the request
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// readResponseBody reads and returns the response body as a string
// Best practice: Centralize response reading to handle errors consistently
func readResponseBody(resp *http.Response) (string, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return string(body), nil
}

// handleHTTPError creates a diagnostic error for HTTP failures
// Best practice: Standardize error messages across the provider
func handleHTTPError(operation string, err error) diag.Diagnostics {
	return diag.Diagnostics{
		diag.NewErrorDiagnostic("HTTP Request Error", fmt.Sprintf("Failed to %s: %v", operation, err)),
	}
}

// handleResponseError creates a diagnostic error for API response failures
func handleResponseError(operation string, statusCode int, body string) diag.Diagnostics {
	return diag.Diagnostics{
		diag.NewErrorDiagnostic("API Error", fmt.Sprintf("API returned status %d during %s: %s", statusCode, operation, body)),
	}
}

// handleJSONError creates a diagnostic error for JSON parsing failures
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

			// Skip unexported fields
			if !fieldValue.CanInterface() {
				continue
			}

			// Get JSON tag for field name
			jsonTag := field.Tag.Get("json")
			if jsonTag == "" || jsonTag == "-" {
				// Use tfsdk tag if no json tag
				jsonTag = field.Tag.Get("tfsdk")
			}
			if jsonTag == "" {
				// Use field name if no tags
				jsonTag = field.Name
			}

			// Remove omitempty and other options from tag
			if commaIdx := len(jsonTag); commaIdx > 0 {
				for j, char := range jsonTag {
					if char == ',' {
						commaIdx = j
						break
					}
				}
				jsonTag = jsonTag[:commaIdx]
			}

			// Convert the field value
			convertedValue := convertTerraformToAPI(fieldValue.Interface())

			// Only include non-nil values to avoid sending empty fields
			if convertedValue != nil {
				result[jsonTag] = convertedValue
			}
		}

		return result
	}

	// Return the value as-is for basic types
	return srcValue.Interface()
}

// normalizeJSON normalizes a JSON string to ensure consistent key ordering
func normalizeJSON(jsonStr string) (string, error) {
	// Parse the JSON string
	var jsonObj interface{}
	if err := json.Unmarshal([]byte(jsonStr), &jsonObj); err != nil {
		return jsonStr, err // Return original string if parsing fails
	}

	// Re-encode with sorted keys to ensure consistent ordering
	normalized, err := json.Marshal(jsonObj)
	if err != nil {
		return jsonStr, err // Return original string if marshaling fails
	}

	return string(normalized), nil
}

// updateTerraformFromAPI updates Terraform framework types from simple Go types (JSON response)
// This handles string -> types.String, int64 -> types.Int64, etc.
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

		// Skip unexported fields
		if !fieldValue.CanSet() {
			continue
		}

		// Get JSON tag for field name
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			// Use tfsdk tag if no json tag
			jsonTag = field.Tag.Get("tfsdk")
		}
		if jsonTag == "" {
			// Use field name if no tags
			jsonTag = field.Name
		}

		// Remove omitempty and other options from tag
		if commaIdx := len(jsonTag); commaIdx > 0 {
			for j, char := range jsonTag {
				if char == ',' {
					commaIdx = j
					break
				}
			}
			jsonTag = jsonTag[:commaIdx]
		}

		// Get the value from the source map
		apiValue, exists := src[jsonTag]
		if !exists {
			continue
		}

		// Convert based on the destination field type
		switch fieldValue.Type() {
		case reflect.TypeOf(types.String{}):
			if apiValue == nil {
				fieldValue.Set(reflect.ValueOf(types.StringNull()))
			} else if str, ok := apiValue.(string); ok {
				// Special handling for filter_expression_json field - normalize JSON
				if jsonTag == "filter_expression_json" {
					if normalizedStr, err := normalizeJSON(str); err == nil {
						fieldValue.Set(reflect.ValueOf(types.StringValue(normalizedStr)))
					} else {
						// If normalization fails, use the original string
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
				// Handle both int64 and float64 (JSON numbers)
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
				// Handle both int64 and float64 (JSON numbers)
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

// APIClient provides high-level methods for common API operations
type APIClient struct {
	httpClient *HTTPClient
}

// NewAPIClient creates a new API client with the shared HTTP client
func NewAPIClient(apiKey, organizationId, baseURL string) *APIClient {
	return &APIClient{
		httpClient: NewHTTPClient(apiKey, organizationId, baseURL),
	}
}

// doJSONRequest is a high-level method that handles JSON requests and responses
// Now handles Terraform framework types automatically
func (c *APIClient) doJSONRequest(ctx context.Context, method, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	var body io.Reader

	// Marshal request body if provided, converting Terraform types first
	if requestBody != nil {
		// Convert Terraform types to simple types for JSON marshaling
		convertedRequest := convertTerraformToAPI(requestBody)

		jsonData, err := json.Marshal(convertedRequest)
		if err != nil {
			return handleJSONError("marshal request", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	// Make the request
	resp, err := c.httpClient.makeRequest(ctx, method, endpoint, body)
	if err != nil {
		return handleHTTPError(fmt.Sprintf("%s %s", method, endpoint), err)
	}
	defer resp.Body.Close()

	// Read response body
	bodyStr, err := readResponseBody(resp)
	if err != nil {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic("Response Read Error", fmt.Sprintf("Failed to read response body: %v", err)),
		}
	}

	// Handle different status codes
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// Success - parse response if responseBody is provided
		if responseBody != nil && len(bodyStr) > 0 {
			// Unmarshal to a map first to handle the conversion
			var apiResponse map[string]interface{}
			if err := json.Unmarshal([]byte(bodyStr), &apiResponse); err != nil {
				return handleJSONError("unmarshal response", err)
			}

			// Update the Terraform model from the API response
			updateTerraformFromAPI(responseBody, apiResponse)
		}
		return nil

	case http.StatusNotFound:
		// Resource not found - return a warning
		return diag.Diagnostics{
			diag.NewWarningDiagnostic("Resource Not Found", fmt.Sprintf("Resource not found at %s", endpoint)),
		}

	case http.StatusNoContent:
		// Success but no content (common for DELETE operations)
		return nil

	default:
		// Any other status code is an error
		return handleResponseError(fmt.Sprintf("%s %s", method, endpoint), resp.StatusCode, bodyStr)
	}
}

// Get performs a GET request and unmarshals the response
func (c *APIClient) Get(ctx context.Context, endpoint string, responseBody interface{}) diag.Diagnostics {
	return c.doJSONRequest(ctx, "GET", endpoint, nil, responseBody)
}

// Post performs a POST request with a JSON body and unmarshals the response
func (c *APIClient) Post(ctx context.Context, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	return c.doJSONRequest(ctx, "POST", endpoint, requestBody, responseBody)
}

// Put performs a PUT request with a JSON body and unmarshals the response
func (c *APIClient) Put(ctx context.Context, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	return c.doJSONRequest(ctx, "PUT", endpoint, requestBody, responseBody)
}

// Delete performs a DELETE request
func (c *APIClient) Delete(ctx context.Context, endpoint string) diag.Diagnostics {
	return c.doJSONRequest(ctx, "DELETE", endpoint, nil, nil)
}

// GetOrganizationId returns the organization ID configured for this client
func (c *APIClient) GetOrganizationId() string {
	return c.httpClient.organizationId
}
