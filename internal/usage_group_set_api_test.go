// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"terraform-provider-select/internal/provider/resource_usage_group"
	"terraform-provider-select/internal/provider/resource_usage_group_set"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func usageGroupSet() resource_usage_group_set.UsageGroupSetModel {
	return resource_usage_group_set.UsageGroupSetModel{
		Id:                  types.StringValue("ugs-1"),
		Etag:                types.StringValue("etag-1"),
		Name:                types.StringValue("Engineering"),
		Order:               types.Int64Value(0),
		TeamId:              types.StringValue("team-1"),
		Public:              types.BoolValue(true),
		Version:             types.Int64Value(3),
		InsightsSyncPending: types.BoolValue(false),
	}
}

// organization_id was a v1 attribute: the organization was a path segment, so
// the resource carried it. v2 scopes by the x-tenant-id header instead, which
// the client sets from the provider block, so the attribute is gone. Its
// removal is the breaking part of this resource's migration.
func TestUsageGroupSchemasDropOrganizationId(t *testing.T) {
	ctx := context.Background()

	if _, present := resource_usage_group_set.UsageGroupSetResourceSchema(ctx).Attributes["organization_id"]; present {
		t.Error("usage_group_set should not carry organization_id: v2 scopes by the x-tenant-id header")
	}
	if _, present := resource_usage_group.UsageGroupResourceSchema(ctx).Attributes["organization_id"]; present {
		t.Error("usage_group should not carry organization_id: v2 scopes by the x-tenant-id header")
	}
}

func TestUsageGroupSetCreatePayloadOmitsAbsentOptionals(t *testing.T) {
	plan := usageGroupSet()
	plan.TeamId = types.StringNull()
	plan.Public = types.BoolNull()

	body := marshal(t, buildUsageGroupSetCreate(&plan))

	if body["name"] != "Engineering" {
		t.Errorf("name should be sent, got %v", body["name"])
	}
	for _, field := range []string{"team_id", "public"} {
		if _, present := body[field]; present {
			t.Errorf("%s was not configured and should be omitted, got %v", field, body[field])
		}
	}
}

func TestUsageGroupSetUpdatePayloadOmitsUnchangedFields(t *testing.T) {
	state := usageGroupSet()
	plan := usageGroupSet()
	plan.Order = types.Int64Value(5)

	body := marshal(t, buildUsageGroupSetUpdate(&plan, &state))

	if body["order"] != float64(5) {
		t.Errorf("a changed order should be sent, got %v", body["order"])
	}
	for _, field := range []string{"name", "public", "team_id"} {
		if _, present := body[field]; present {
			t.Errorf("%s did not change and should be omitted from the merge patch, got %v", field, body[field])
		}
	}
}

// version is deprecated on UsageGroupSetUpdateV2: it was v2's original way to
// snapshot, by incrementing the count. This provider records versions through
// POST .../versions instead, so the key must never appear in a patch.
func TestUsageGroupSetUpdatePayloadNeverSendsVersion(t *testing.T) {
	state := usageGroupSet()
	plan := usageGroupSet()
	plan.Name = types.StringValue("Platform")
	plan.Version = types.Int64Value(99)

	body := marshal(t, buildUsageGroupSetUpdate(&plan, &state))

	if _, present := body["version"]; present {
		t.Errorf("version is deprecated on the patch body and must never be sent, got %v", body["version"])
	}
}

// team_id is the one field on a set the API lets a caller clear, so removing it
// from a configuration has to reach the API as an explicit null rather than
// being omitted, which would leave the set on its old team.
func TestUsageGroupSetUpdatePayloadClearsTeamWithExplicitNull(t *testing.T) {
	state := usageGroupSet()
	plan := usageGroupSet()
	plan.TeamId = types.StringNull()

	body := marshal(t, buildUsageGroupSetUpdate(&plan, &state))

	value, present := body["team_id"]
	if !present {
		t.Fatal("unowning a set must send an explicit null, not omit the key")
	}
	if value != nil {
		t.Errorf("team_id should be null, got %v", value)
	}
}

func TestUsageGroupSetApplyResponseTakesEverythingFromTheResponse(t *testing.T) {
	source := usageGroupSet()
	model := source
	team := "team-2"
	response := usageGroupSetResponse{
		Id:                  "ugs-1",
		Etag:                "etag-2",
		Name:                "Platform",
		Order:               5,
		TeamId:              &team,
		Public:              false,
		Version:             4,
		InsightsSyncPending: true,
	}

	if diags := applyUsageGroupSetResponse(context.Background(), &model, &source, &response); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if model.Name.ValueString() != "Platform" || model.Order.ValueInt64() != 5 {
		t.Errorf("name and order should come from the response, got %v", model)
	}
	if model.TeamId.ValueString() != "team-2" {
		t.Errorf("team_id should come from the response, got %v", model.TeamId)
	}
	if model.Version.ValueInt64() != 4 {
		t.Errorf("version should come from the response, got %v", model.Version)
	}
	if !model.InsightsSyncPending.ValueBool() {
		t.Error("insights_sync_pending should come from the response")
	}
}

// versionServer counts POSTs per set so a test can tell how many versions an
// apply would record.
type versionServer struct {
	mu     sync.Mutex
	posts  map[string]int
	server *httptest.Server
}

func newVersionServer(t *testing.T) *versionServer {
	t.Helper()
	v := &versionServer{posts: map[string]int{}}
	v.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/versions") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		setId := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/usage-group-sets/"), "/versions")

		v.mu.Lock()
		v.posts[setId]++
		v.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ver-1","create_time":"2026-09-21T00:00:00Z","created_by":"key-1","usage_group_set_id":"` + setId + `","usage_groups":[]}`))
	}))
	t.Cleanup(v.server.Close)
	return v
}

func (v *versionServer) client() *APIClient {
	return NewAPIClient("key", "org", v.server.URL)
}

func (v *versionServer) count(setId string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.posts[setId]
}

// One version per set per apply, however many groups in that set the apply
// touches. This is what makes an apply's changes land on one checkpoint rather
// than a version per group.
func TestEnsureVersionRecordsOneVersionPerSet(t *testing.T) {
	server := newVersionServer(t)
	client := server.client()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if diags := client.EnsureVersion(ctx, "ugs-1"); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
	}

	if got := server.count("ugs-1"); got != 1 {
		t.Errorf("five group writes in one set should record one version, recorded %d", got)
	}
}

// The v1 implementation held a single sync.Once on the client, so in an apply
// touching two sets only the first ever got a version and the second's previous
// state was overwritten with no checkpoint. Versions are tracked per set now.
func TestEnsureVersionRecordsAVersionForEverySet(t *testing.T) {
	server := newVersionServer(t)
	client := server.client()
	ctx := context.Background()

	for _, setId := range []string{"ugs-1", "ugs-2", "ugs-3"} {
		for i := 0; i < 3; i++ {
			if diags := client.EnsureVersion(ctx, setId); diags.HasError() {
				t.Fatalf("%s: unexpected diagnostics: %v", setId, diags)
			}
		}
	}

	for _, setId := range []string{"ugs-1", "ugs-2", "ugs-3"} {
		if got := server.count(setId); got != 1 {
			t.Errorf("%s should have exactly one version recorded, got %d", setId, got)
		}
	}
}

// Terraform applies at a default parallelism of 10, so several groups in the
// same set reach EnsureVersion at once. Run with -race.
func TestEnsureVersionIsSafeUnderConcurrency(t *testing.T) {
	server := newVersionServer(t)
	client := server.client()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if diags := client.EnsureVersion(ctx, "ugs-1"); diags.HasError() {
				t.Errorf("unexpected diagnostics: %v", diags)
			}
		}()
	}
	wg.Wait()

	if got := server.count("ugs-1"); got != 1 {
		t.Errorf("ten concurrent writes should record one version, recorded %d", got)
	}
	if !client.VersionRecorded("ugs-1") {
		t.Error("VersionRecorded should report true once a version has been recorded")
	}
}

// The set resource asks VersionRecorded while a group write in the same apply
// may still be recording that version. Run with -race.
func TestVersionRecordedIsSafeDuringEnsureVersion(t *testing.T) {
	server := newVersionServer(t)
	client := server.client()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = client.EnsureVersion(context.Background(), "ugs-1")
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = client.VersionRecorded("ugs-1")
		}
	}()
	wg.Wait()

	if !client.VersionRecorded("ugs-1") {
		t.Error("VersionRecorded should report true once EnsureVersion has returned")
	}
}

// A write only treats a 412 as self-inflicted for a set this apply actually
// recorded a version of; anywhere else a precondition failure is real drift and
// has to reach the user.
func TestVersionRecordedOnlyForSetsThisApplyTouched(t *testing.T) {
	server := newVersionServer(t)
	client := server.client()

	if client.VersionRecorded("ugs-1") {
		t.Error("no version has been recorded yet")
	}

	if diags := client.EnsureVersion(context.Background(), "ugs-1"); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !client.VersionRecorded("ugs-1") {
		t.Error("ugs-1 should be reported as recorded")
	}
	if client.VersionRecorded("ugs-2") {
		t.Error("ugs-2 was never written to and should not be reported as recorded")
	}
}

// A failure to record a version has to stop the write. Carrying on would mean
// the apply silently overwrites the state it was supposed to check-point first.
func TestEnsureVersionReportsAFailureAndDoesNotRetry(t *testing.T) {
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"This caller lacks the usage_groups:write scope.","code":"forbidden"}`))
	}))
	defer server.Close()

	client := NewAPIClient("key", "org", server.URL)
	ctx := context.Background()

	diags := client.EnsureVersion(ctx, "ugs-1")
	if !diags.HasError() {
		t.Fatal("a refused version should be reported as an error")
	}
	if !strings.Contains(diags[0].Detail(), "usage_groups:write") {
		t.Errorf("the diagnostic should name the scopes needed, got: %s", diags[0].Detail())
	}

	// A second write in the same apply gets the same answer without asking again.
	if again := client.EnsureVersion(ctx, "ugs-1"); !again.HasError() {
		t.Error("the failure should stick for the rest of the apply")
	}
	if posts != 1 {
		t.Errorf("a failed version should not be retried within an apply, posted %d times", posts)
	}
	if client.VersionRecorded("ugs-1") {
		t.Error("a version that failed to record should not count as recorded")
	}
}

// A set that was removed out of band has nothing left to check-point. The
// write EnsureVersion is preparing gets its own 404 from the set's item
// endpoint, so a delete of a group whose set is also gone must succeed rather
// than fail here first.
func TestEnsureVersionToleratesA404AndDoesNotRetry(t *testing.T) {
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Usage group set 'ugs-1' was not found.","code":"not_found"}`))
	}))
	defer server.Close()

	client := NewAPIClient("key", "org", server.URL)
	ctx := context.Background()

	if diags := client.EnsureVersion(ctx, "ugs-1"); diags.HasError() {
		t.Fatalf("a 404 has nothing to check-point and must not fail the write: %v", diags)
	}

	// A second write in the same apply gets the same tolerant answer without
	// asking again — the sync.Once already ran.
	if diags := client.EnsureVersion(ctx, "ugs-1"); diags.HasError() {
		t.Errorf("a 404 result should not become sticky: %v", diags)
	}
	if posts != 1 {
		t.Errorf("a 404 should not be retried within an apply, posted %d times", posts)
	}
}

// VersionRecorded gates the self-inflicted-412 retry. A 404 means nothing was
// actually recorded, so a later 412 on this set must still reach the user as
// real drift rather than being retried as this provider's own doing.
func TestEnsureVersionA404DoesNotCountAsRecorded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Usage group set 'ugs-1' was not found.","code":"not_found"}`))
	}))
	defer server.Close()

	client := NewAPIClient("key", "org", server.URL)

	if diags := client.EnsureVersion(context.Background(), "ugs-1"); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if client.VersionRecorded("ugs-1") {
		t.Error("a set that answered 404 has no recorded version and must not be reported as recorded")
	}
}

// A 404 from the version route alone does not prove the set is gone: a server
// without that route answers the same way. If the set itself still answers, the
// write must stop rather than go ahead with no checkpoint.
func TestEnsureVersionFailsWhenOnlyTheVersionRouteIs404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == usageGroupSetEndpoint("ugs-1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ugs-1"}`))
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not Found","code":"not_found"}`))
	}))
	defer server.Close()

	client := NewAPIClient("key", "org", server.URL)

	if diags := client.EnsureVersion(context.Background(), "ugs-1"); !diags.HasError() {
		t.Fatal("a set that still exists must not have its missing checkpoint tolerated")
	}
	if client.VersionRecorded("ugs-1") {
		t.Error("a version that failed to record should not count as recorded")
	}
}

// A failure that is not a 404 — a 403 on scopes, a 500 — must still fail the
// apply the same way for every caller, 404-tolerance notwithstanding.
func TestEnsureVersionStillFailsOnNonNotFoundErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"Something went wrong.","code":"internal_error"}`))
	}))
	defer server.Close()

	client := NewAPIClient("key", "org", server.URL)

	diags := client.EnsureVersion(context.Background(), "ugs-1")
	if !diags.HasError() {
		t.Fatal("a 500 is not a case for 404-tolerance and should still fail the write")
	}
	if client.VersionRecorded("ugs-1") {
		t.Error("a version that failed to record should not count as recorded")
	}
}

func TestUsageGroupSetEndpoints(t *testing.T) {
	if got := usageGroupSetEndpoint("ugs-1"); got != "/v2/usage-group-sets/ugs-1" {
		t.Errorf("item endpoint = %s", got)
	}
	if got := usageGroupSetVersionsEndpoint("ugs-1"); got != "/v2/usage-group-sets/ugs-1/versions" {
		t.Errorf("versions endpoint = %s", got)
	}
}

func TestUsageGroupSetPreconditionAndScopeDiagnostics(t *testing.T) {
	stale := usageGroupSetErrors.diagnostic("update the usage group set",
		newAPIError(412, `{"detail":"The If-Match header does not match the resource's current ETag.","code":"precondition_failed"}`), nil)
	if !strings.Contains(stale.Detail(), "-refresh-only") {
		t.Errorf("a stale ETag should tell the user how to recover, got: %s", stale.Detail())
	}

	forbidden := usageGroupSetErrors.diagnostic("add the usage group set",
		newAPIError(403, `{"detail":"This caller lacks the usage_groups:write scope.","code":"forbidden"}`), nil)
	if !strings.Contains(forbidden.Detail(), "usage_groups:write") {
		t.Errorf("a scope failure should name the scopes needed, got: %s", forbidden.Detail())
	}
}

// The API answers every 403 with the same "forbidden" code, whether the cause
// is a missing scope or something unrelated to scopes at all — such as a
// team_id naming a team outside the caller's organization. The diagnostic must
// not assert a scope problem it cannot tell apart from any other 403; it should
// lead with the API's own detail and offer scopes only as a possibility.
func TestUsageGroupSetForbiddenDoesNotAssertAScopeCause(t *testing.T) {
	teamAccess := usageGroupSetErrors.diagnostic("add the usage group set",
		newAPIError(403, `{"detail":"This organization does not have access to team 2f0899e2-2746-4300-887c-524e64b5a138.","code":"forbidden"}`), nil)

	if !strings.Contains(teamAccess.Detail(), "does not have access to team") {
		t.Errorf("the diagnostic should quote the API's actual explanation, got: %s", teamAccess.Detail())
	}
	if teamAccess.Summary() == "Insufficient API Key Scopes" {
		t.Error("the summary should not name scopes as the cause when the API's detail says otherwise")
	}
}

// A 409 on its own says something clashed, not what. The problem document's
// details array names the offending field, and the diagnostic should surface
// it rather than dropping it the way the plain unexpected fallback did.
func TestUsageGroupSetConflictNamesTheOffendingField(t *testing.T) {
	dup := usageGroupSetErrors.diagnostic("add the usage group set",
		newAPIError(409, `{"detail":"A usage group set named 'Engineering' already exists.","code":"conflict","details":[{"field":"body.name"}]}`), nil)

	// Assert on wording only the conflict renderer produces. The API's own
	// detail already contains "name" and "already exists", so matching those
	// would pass against the generic fallback too.
	if dup.Summary() != "Usage Group Set Conflict" {
		t.Errorf("a 409 carrying a field should use the conflict wording, got summary: %s", dup.Summary())
	}
	if !strings.Contains(dup.Detail(), `conflict is on the "name" field`) {
		t.Errorf("the diagnostic should name the conflicting field, got: %s", dup.Detail())
	}
	if !strings.Contains(dup.Detail(), "already exists") {
		t.Errorf("the diagnostic should still quote the API's detail, got: %s", dup.Detail())
	}
}

// A 409 with no details array has nothing to name, so the diagnostic should
// fall back to the plain unexpected wording rather than claiming a field it was
// never told about.
func TestUsageGroupSetConflictWithNoDetailsFallsBackToUnexpected(t *testing.T) {
	dup := usageGroupSetErrors.diagnostic("add the usage group set",
		newAPIError(409, `{"detail":"A usage group set named 'Engineering' already exists.","code":"conflict"}`), nil)

	if dup.Summary() != "Usage Group Set API Error" {
		t.Errorf("a conflict with no details should fall back to the generic fallback, got summary: %s", dup.Summary())
	}
	if strings.Contains(dup.Detail(), "conflict is on the") {
		t.Errorf("the diagnostic should not claim a field it was never told about, got: %s", dup.Detail())
	}
}
