// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Conventions shared by every resource on SELECT's v2 API. They come from the
// API's own shared machinery rather than from any one resource: optimistic
// concurrency through If-Match, RFC 9457 problem documents, and a validation
// report attached to a write the API refused to persist. A new v2 resource
// should reach for these rather than restating them.

// ifMatchHeader carries a resource's ETag on a write. The API requires it: a
// write without If-Match is refused with 428, and one carrying a stale value
// with 412, so a change made outside Terraform cannot be silently overwritten.
func ifMatchHeader(etag types.String) map[string]string {
	if etag.IsNull() || etag.IsUnknown() {
		return nil
	}
	return map[string]string{"If-Match": etag.ValueString()}
}

// v2ValidationReport is the report the API attaches to a problem document when a
// configuration fails its checks against the system being connected. Every
// connection resource validates the same way and reports it in the same shape.
type v2ValidationReport struct {
	Success bool                `json:"success"`
	Checks  []v2ValidationCheck `json:"checks"`
}

type v2ValidationCheck struct {
	Id       string  `json:"id"`
	Label    string  `json:"label"`
	Status   string  `json:"status"`
	Message  *string `json:"message"`
	DocsLink *string `json:"docs_link"`
}

// v2ValidationDiagnostic turns an API failure that carried a validation report
// into a diagnostic naming the checks that did not pass, in the order they ran.
// It returns nil when the response carried no report, or when every check
// passed — a report saying nothing went wrong explains nothing about a failure,
// so the caller should fall through to its status-based handling.
func v2ValidationDiagnostic(title, operation string, apiErr *apiError) diag.Diagnostic {
	var problem struct {
		ValidationReport *v2ValidationReport `json:"validation_report"`
	}
	if err := json.Unmarshal([]byte(apiErr.Body), &problem); err != nil {
		return nil
	}
	if problem.ValidationReport == nil {
		return nil
	}

	var lines []string
	for _, check := range problem.ValidationReport.Checks {
		if check.Status == "passed" {
			continue
		}
		line := fmt.Sprintf("  - %s: %s", check.Label, check.Status)
		if check.Message != nil && *check.Message != "" {
			line = fmt.Sprintf("  - %s: %s", check.Label, *check.Message)
		}
		if check.DocsLink != nil && *check.DocsLink != "" {
			line += fmt.Sprintf(" (%s)", *check.DocsLink)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}

	return diag.NewErrorDiagnostic(
		title,
		fmt.Sprintf("SELECT could not %s. These checks did not pass:\n%s",
			operation, strings.Join(lines, "\n")),
	)
}

// v2ErrorFormat names a resource the way its diagnostics talk about it, so the
// failures every v2 resource shares — the ETag contract and the API key's scopes
// — are worded once rather than per resource.
type v2ErrorFormat struct {
	// Noun is the resource in title case, for diagnostic summaries: "Snowflake
	// Account".
	Noun string
	// Subject is how the message body refers to it: "the account".
	Subject string
	// Plural describes what an API key is being asked to manage: "Snowflake
	// accounts".
	Plural string
	// ReadScope and WriteScope are the API key scopes the resource needs.
	ReadScope, WriteScope string
}

// preconditionFailed explains a 412: the resource changed between the read that
// recorded the ETag and this write.
func (f v2ErrorFormat) preconditionFailed(operation string, apiErr *apiError) diag.Diagnostic {
	return diag.NewErrorDiagnostic(
		f.Noun+" Changed Outside Terraform",
		fmt.Sprintf("SELECT could not %s because %s has changed since Terraform last read it. "+
			"Run `terraform apply -refresh-only` to pick up the current state, then apply again.\n\n%s",
			operation, f.Subject, apiErr.Detail),
	)
}

// preconditionRequired explains a 428: Terraform sent no ETag at all, which
// happens when state predates the field or was hand-edited.
func (f v2ErrorFormat) preconditionRequired(operation string, apiErr *apiError) diag.Diagnostic {
	return diag.NewErrorDiagnostic(
		"Missing "+f.Noun+" ETag",
		fmt.Sprintf("SELECT could not %s because Terraform holds no ETag for it. "+
			"Run `terraform apply -refresh-only` to record one, then apply again.\n\n%s",
			operation, apiErr.Detail),
	)
}

// forbidden explains a 403 by naming the scopes the API key is missing.
func (f v2ErrorFormat) forbidden(operation string, apiErr *apiError) diag.Diagnostic {
	return diag.NewErrorDiagnostic(
		"Insufficient API Key Scopes",
		fmt.Sprintf("SELECT could not %s. Managing %s needs an API key with the "+
			"%s and %s scopes.\n\n%s",
			operation, f.Plural, f.ReadScope, f.WriteScope, apiErr.Detail),
	)
}

// unexpected is the fallback for a status with no specific advice, quoting the
// problem document's own explanation.
func (f v2ErrorFormat) unexpected(operation string, apiErr *apiError) diag.Diagnostic {
	return diag.NewErrorDiagnostic(
		f.Noun+" API Error",
		fmt.Sprintf("SELECT could not %s: %s", operation, apiErr.Error()),
	)
}
