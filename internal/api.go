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

func (c *HTTPClient) makeRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	url := c.buildURL(endpoint)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))
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

// doJSONRequest handles JSON requests and responses
func (c *APIClient) doJSONRequest(ctx context.Context, method, endpoint string, requestBody interface{}, responseBody interface{}) diag.Diagnostics {
	var body io.Reader

	if requestBody != nil {
		convertedRequest := convertTerraformToAPI(requestBody)

		jsonData, err := json.Marshal(convertedRequest)
		if err != nil {
			return handleJSONError("marshal request", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	resp, err := c.httpClient.makeRequest(ctx, method, endpoint, body)
	if err != nil {
		return handleHTTPError(fmt.Sprintf("%s %s", method, endpoint), err)
	}
	defer resp.Body.Close()

	bodyStr, err := readResponseBody(resp)
	if err != nil {
		return diag.Diagnostics{
			diag.NewErrorDiagnostic("Response Read Error", fmt.Sprintf("Failed to read response body: %v", err)),
		}
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if responseBody != nil && len(bodyStr) > 0 {
			responseValue := reflect.ValueOf(responseBody)
			if responseValue.Kind() == reflect.Ptr && responseValue.Elem().Kind() == reflect.Struct {
				responseType := responseValue.Elem().Type()
				isRegularStruct := false
				for i := 0; i < responseType.NumField(); i++ {
					field := responseType.Field(i)
					if _, hasJSON := field.Tag.Lookup("json"); hasJSON {
						if field.Type.PkgPath() == "" || !strings.Contains(field.Type.String(), "types.") {
							isRegularStruct = true
							break
						}
					}
				}
				
				if isRegularStruct {
					if err := json.Unmarshal([]byte(bodyStr), responseBody); err != nil {
						return handleJSONError("unmarshal response", err)
					}
				} else {
					var apiResponse map[string]interface{}
					if err := json.Unmarshal([]byte(bodyStr), &apiResponse); err != nil {
						return handleJSONError("unmarshal response", err)
					}
					updateTerraformFromAPI(responseBody, apiResponse)
				}
			}
		}
		return nil

	case http.StatusNotFound:
		return diag.Diagnostics{
			diag.NewWarningDiagnostic("Resource Not Found", fmt.Sprintf("Resource not found at %s", endpoint)),
		}

	case http.StatusNoContent:
		return nil

	default:
		return handleResponseError(fmt.Sprintf("%s %s", method, endpoint), resp.StatusCode, bodyStr)
	}
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
