// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Field-conversion and drift-avoidance helpers used by the v2 resources' hand-
// written payload builders and response mappers. The reflection-based
// converters in api.go walk a flat model of scalar Terraform types; these
// per-attribute helpers cover what those cannot express: a pointer, so an
// explicit JSON null stays distinguishable from an omitted field, lists, and
// the normalization needed to keep a resource's plan stable when the API
// echoes a value back in a different but equivalent form.

// stringPointer returns nil for a null or unknown value, so the field is
// serialized as JSON null rather than an empty string.
func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := value.ValueString()
	return &result
}

// nullableString is a merge-patch field that has to say three things a *string
// cannot: leave this unchanged, clear it, or set it. A nil *nullableString with
// `omitempty` is omitted, a non-nil one holding a nil value marshals as an
// explicit null, and one holding a value marshals as that value.
//
// Only fields the API actually lets a caller clear need this. Everywhere else a
// *string is enough, because "omitted" and "null" mean the same thing on a
// create and the API rejects the null on an update.
type nullableString struct {
	value *string
}

func (n nullableString) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.value)
}

// clearedString builds the patch value for a clearable field: nil when the
// configured value has not changed, an explicit null when it has been removed
// from the configuration, and the new value otherwise.
//
// An unknown plan value is omitted rather than sent. Terraform resolves a
// configured attribute before it calls Update, so this should not arise, but
// stringPointer maps unknown to nil and sending that would clear a field the
// configuration never asked to clear.
func clearedString(plan, state types.String) *nullableString {
	if plan.IsUnknown() || plan.Equal(state) {
		return nil
	}
	return &nullableString{value: stringPointer(plan)}
}

// changedString returns the plan's value when it differs from state, nil to
// omit it from a merge-patch update — the v2 API leaves an omitted field
// unchanged, so this is how a builder says "nothing to send here." Sibling of
// clearedString for fields the API does not support clearing.
func changedString(plan, state types.String) *string {
	if plan.Equal(state) {
		return nil
	}
	return stringPointer(plan)
}

// changedBool is changedString's counterpart for types.Bool.
func changedBool(plan, state types.Bool) *bool {
	if plan.Equal(state) {
		return nil
	}
	return boolPointer(plan)
}

func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := value.ValueBool()
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

// int64Pointer returns nil for a null or unknown value, so the field is
// serialized as JSON null rather than 0.
func int64Pointer(value types.Int64) *int64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := value.ValueInt64()
	return &result
}

// changedNumber is changedString's counterpart for types.Number.
func changedNumber(plan, state types.Number) *float64 {
	if plan.Equal(state) {
		return nil
	}
	return numberPointer(plan)
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

func int64Value(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
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

// preserveEquivalentFold keeps the configured spelling of a field the API folds
// to a canonical case, when the two are equal case-insensitively. Without this a
// configured value that differs from the API's casing only would fail every
// apply with an inconsistent-result error, the same class of bug
// preserveEquivalentJSON and preserveEquivalentNumber exist to prevent for their
// own fields.
func preserveEquivalentFold(configured types.String, returned *string) types.String {
	if returned == nil || configured.IsNull() || configured.IsUnknown() {
		return stringValue(returned)
	}
	if strings.EqualFold(configured.ValueString(), *returned) {
		return configured
	}
	return types.StringValue(*returned)
}

// preserveEquivalentTime keeps the configured spelling of a timestamp when the
// API echoes back an equivalent instant in a different format — a budget's
// started_at configured as the bare date "2026-01-01" comes back as
// "2026-01-01T00:00:00Z".
func preserveEquivalentTime(configured types.String, returned *string) types.String {
	if returned == nil || configured.IsNull() || configured.IsUnknown() {
		return stringValue(returned)
	}
	configuredTime, configuredErr := parseTimestamp(configured.ValueString())
	returnedTime, returnedErr := parseTimestamp(*returned)
	if configuredErr == nil && returnedErr == nil && configuredTime.Equal(returnedTime) {
		return configured
	}
	return types.StringValue(*returned)
}

// parseTimestamp accepts every spelling a timestamp field can arrive in: RFC
// 3339 with a zone, the zone-less form some clients send, and a bare date.
func parseTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", time.DateOnly} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", value)
}
