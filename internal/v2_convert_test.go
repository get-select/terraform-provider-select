// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
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

func TestPreserveConfiguredJSON(t *testing.T) {
	configured := `{"title":null,"sections":[{"section_type":"chart","query":{"query_class":"OrgDailySpendQuery","period":{"period":30,"grain":"day"}}}]}`

	cases := []struct {
		name       string
		configured types.String
		returned   *string
		want       types.String
	}{
		{
			name:       "the API adds keys",
			configured: types.StringValue(configured),
			returned:   strPtr(`{"sections":[{"enabled":true,"title":"Daily spend","section_type":"chart","query":{"period":{"grain":"day","period":30,"date_column":"day","fill_missing_dates":false},"query_class":"OrgDailySpendQuery"}}],"templated_values":{}}`),
			want:       types.StringValue(configured),
		},
		{
			name:       "the API changes a configured value",
			configured: types.StringValue(configured),
			returned:   strPtr(`{"sections":[{"section_type":"chart","query":{"query_class":"OrgDailySpendQuery","period":{"period":7,"grain":"day"}}}]}`),
			want:       types.StringValue(`{"sections":[{"section_type":"chart","query":{"query_class":"OrgDailySpendQuery","period":{"period":7,"grain":"day"}}}]}`),
		},
		{
			name:       "the API returns a different number of sections",
			configured: types.StringValue(`{"sections":[{"section_type":"title"}]}`),
			returned:   strPtr(`{"sections":[{"section_type":"title"},{"section_type":"title"}]}`),
			want:       types.StringValue(`{"sections":[{"section_type":"title"},{"section_type":"title"}]}`),
		},
		{
			name:       "a configured null matches the generated value",
			configured: types.StringValue(`{"title":null}`),
			returned:   strPtr(`{"title":"Generated"}`),
			want:       types.StringValue(`{"title":null}`),
		},
		{
			name:       "no configuration, as after an import, gives sorted keys",
			configured: types.StringNull(),
			returned:   strPtr(`{"b":1,"a":2}`),
			want:       types.StringValue(`{"a":2,"b":1}`),
		},
		{
			name:       "no response",
			configured: types.StringValue(configured),
			returned:   nil,
			want:       types.StringNull(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preserveConfiguredJSON(tc.configured, tc.returned); !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestJSONSubset(t *testing.T) {
	cases := []struct {
		want, got string
		subset    bool
	}{
		{`{"a":1}`, `{"a":1,"b":2}`, true},
		{`{"a":1}`, `{"a":1.0}`, true},
		{`{"a":null}`, `{}`, true},
		{`{"a":null}`, `{"a":null}`, true},
		{`{"a":null}`, `{"a":"generated"}`, true},
		{`{"a":1}`, `{}`, false},
		{`{"a":[1,2]}`, `{"a":[2,1]}`, false},
		{`{"a":{"b":"x"}}`, `{"a":"x"}`, false},
		{`[{"a":1}]`, `[{"a":1,"b":2}]`, true},
	}
	for _, tc := range cases {
		var want, got any
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc.got), &got); err != nil {
			t.Fatal(err)
		}
		if jsonSubset(want, got) != tc.subset {
			t.Errorf("jsonSubset(%s, %s) should be %v", tc.want, tc.got, tc.subset)
		}
	}
}
