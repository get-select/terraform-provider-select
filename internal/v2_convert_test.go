// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// preserveEquivalentJSON's ordinary job: when a configured spelling exists,
// keep it if the API's response is only reformatted, and take the response
// otherwise. See TestUsageGroupApplyResponsePreservesFilterKeyOrder and
// TestUsageGroupApplyResponseTakesGenuinelyChangedFilter for the same
// behavior exercised through applyUsageGroupResponse.

func TestPreserveEquivalentJSONKeepsConfiguredSpellingWhenEquivalent(t *testing.T) {
	configured := `{"filters":[],"operator":"and"}`
	returned := `{"operator":"and","filters":[]}`

	got := preserveEquivalentJSON(types.StringValue(configured), &returned)

	if got.ValueString() != configured {
		t.Errorf("an equivalent document should keep the configured spelling, got %s", got.ValueString())
	}
}

func TestPreserveEquivalentJSONTakesGenuinelyChangedValue(t *testing.T) {
	configured := `{"filters":[],"operator":"and"}`
	returned := `{"filters":[],"operator":"or"}`

	got := preserveEquivalentJSON(types.StringValue(configured), &returned)

	if got.ValueString() != returned {
		t.Errorf("a document that really differs should be taken from the response, got %s", got.ValueString())
	}
}

func TestPreserveEquivalentJSONReturnsNullWhenAPIReturnsNothing(t *testing.T) {
	got := preserveEquivalentJSON(types.StringValue(`{"a":1}`), nil)

	if !got.IsNull() {
		t.Errorf("an absent response value should stay null, got %v", got)
	}
}

// There is no configured spelling to preserve on the first Read after a
// `terraform import`: import seeds only the identifying attributes, so
// `configured` is null. Falling back to the API's raw spelling used to
// reproduce a permanent diff against a `jsonencode()`-built config, because
// `jsonencode` always sorts object keys and the API's own re-encoding does
// not. Falling back to normalizeJSON's sorted-key rendering instead lines the
// two up, so the first post-import plan is already empty.
func TestPreserveEquivalentJSONCanonicalizesWhenNoConfiguredSpellingExists(t *testing.T) {
	returned := `{"operator":"and","filters":[{"field":"warehouse_name","values":["SELECT_BACKEND"],"operator":"in"}]}`
	// What Terraform's jsonencode() renders for the semantically identical
	// filter, regardless of the order its arguments were written in: sorted
	// keys, no extra whitespace.
	wantJsonencode := `{"filters":[{"field":"warehouse_name","operator":"in","values":["SELECT_BACKEND"]}],"operator":"and"}`

	for name, configured := range map[string]types.String{
		"null configured (import)": types.StringNull(),
		"unknown configured":       types.StringUnknown(),
	} {
		t.Run(name, func(t *testing.T) {
			got := preserveEquivalentJSON(configured, &returned)
			if got.ValueString() != wantJsonencode {
				t.Errorf("canonicalized value should match jsonencode's sorted-key rendering,\ngot  %s\nwant %s", got.ValueString(), wantJsonencode)
			}
		})
	}
}

// A malformed value from the API (which should never happen, but normalizeJSON
// can fail) must not be dropped silently.
func TestPreserveEquivalentJSONFallsBackToRawOnInvalidJSONWhenUnconfigured(t *testing.T) {
	returned := `not valid json`

	got := preserveEquivalentJSON(types.StringNull(), &returned)

	if got.ValueString() != returned {
		t.Errorf("invalid JSON should be passed through verbatim rather than dropped, got %s", got.ValueString())
	}
}
