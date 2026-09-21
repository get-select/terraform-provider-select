// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// probeModel is the smallest thing v2Resource's write path needs: an id to
// build an endpoint from and an ETag to send.
type probeModel struct {
	Id   types.String
	Etag types.String
}

type probeResponse struct {
	Id   string `json:"id"`
	Etag string `json:"etag"`
}

// probeResource builds a v2Resource wired to a test server, with
// selfInflicted412 fixed at the given answer.
func probeResource(url string, selfInflicted bool) *v2Resource[probeModel, probeResponse] {
	r := &v2Resource[probeModel, probeResponse]{
		client: NewAPIClient("key", "org", url),
		errors: v2ErrorFormat{
			Noun: "Probe", Subject: "the probe", Object: "the probe",
			Plural: "probes", ReadScope: "probes:read", WriteScope: "probes:write",
		},
		itemEndpoint: func(m *probeModel) string { return "/v2/probes/" + m.Id.ValueString() },
		identity: func(m *probeModel) v2Identity {
			return v2Identity{Id: m.Id, Etag: m.Etag}
		},
		applyResponse: func(ctx context.Context, model, source *probeModel, response *probeResponse) diag.Diagnostics {
			model.Id = types.StringValue(response.Id)
			model.Etag = types.StringValue(response.Etag)
			return nil
		},
	}
	if selfInflicted {
		r.selfInflicted412 = func(*APIClient, *probeModel) bool { return true }
	}
	return r
}

// etagServer refuses a write carrying anything but wantEtag, and answers a GET
// with currentEtag, so a test can watch a stale write recover.
type etagServer struct {
	currentEtag string
	writes      []string
	gets        int
}

func (s *etagServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.gets++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"p-1","etag":"` + s.currentEtag + `"}`))
			return
		}

		sent := r.Header.Get("If-Match")
		s.writes = append(s.writes, sent)
		if sent != s.currentEtag {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"detail":"The If-Match header does not match.","code":"precondition_failed"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"p-1","etag":"` + s.currentEtag + `"}`))
	})
}

// Recording a version rotates the set's ETag, so a write later in the same
// apply carries one this provider itself invalidated. That failure is not the
// user's to fix, so the write re-reads and retries once.
func TestWriteRetriesOnceWhenThe412IsSelfInflicted(t *testing.T) {
	backend := &etagServer{currentEtag: "etag-2"}
	server := httptest.NewServer(backend.handler())
	defer server.Close()

	r := probeResource(server.URL, true)
	state := probeModel{Id: types.StringValue("p-1"), Etag: types.StringValue("etag-1")}

	var response probeResponse
	apiErr, diags := r.write(context.Background(), http.MethodPatch, &state, map[string]string{"name": "x"}, &response)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if apiErr != nil {
		t.Fatalf("the retry should have succeeded, got %d: %s", apiErr.StatusCode, apiErr.Detail)
	}

	if len(backend.writes) != 2 {
		t.Fatalf("expected a stale write then a retry, got %d writes: %v", len(backend.writes), backend.writes)
	}
	if backend.writes[0] != "etag-1" || backend.writes[1] != "etag-2" {
		t.Errorf("the retry should carry the freshly read ETag, got %v", backend.writes)
	}
	if backend.gets != 1 {
		t.Errorf("the retry should re-read exactly once, got %d reads", backend.gets)
	}
}

// A 412 on a resource nothing in this apply invalidated is real drift: someone
// changed it outside Terraform. Retrying would overwrite their change, which is
// the whole thing If-Match exists to prevent.
func TestWriteDoesNotRetryAForeign412(t *testing.T) {
	backend := &etagServer{currentEtag: "etag-2"}
	server := httptest.NewServer(backend.handler())
	defer server.Close()

	r := probeResource(server.URL, false)
	state := probeModel{Id: types.StringValue("p-1"), Etag: types.StringValue("etag-1")}

	var response probeResponse
	apiErr, diags := r.write(context.Background(), http.MethodPatch, &state, map[string]string{"name": "x"}, &response)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if apiErr == nil || apiErr.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("a foreign 412 should reach the caller, got %v", apiErr)
	}
	if len(backend.writes) != 1 {
		t.Errorf("a foreign 412 should not be retried, got %d writes", len(backend.writes))
	}
	if backend.gets != 0 {
		t.Errorf("a foreign 412 should not re-read, got %d reads", backend.gets)
	}
}

// The retry re-reads rather than trusting an ETag it already holds, so a change
// made outside Terraform between the two attempts still fails the second one
// rather than being silently overwritten.
func TestWriteRetryStillFailsOnRealDrift(t *testing.T) {
	var writes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The read hands back an ETag that is stale by the time the retry
			// lands, standing in for a concurrent external change.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"p-1","etag":"etag-2"}`))
			return
		}
		writes++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"detail":"The If-Match header does not match.","code":"precondition_failed"}`))
	}))
	defer server.Close()

	r := probeResource(server.URL, true)
	state := probeModel{Id: types.StringValue("p-1"), Etag: types.StringValue("etag-1")}

	var response probeResponse
	apiErr, diags := r.write(context.Background(), http.MethodPatch, &state, map[string]string{"name": "x"}, &response)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if apiErr == nil || apiErr.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("drift that outlives the retry should still fail, got %v", apiErr)
	}
	if writes != 2 {
		t.Errorf("the write should be attempted exactly twice, got %d", writes)
	}
}

// A non-412 failure is the resource's own business and must not be retried.
func TestWriteDoesNotRetryOtherFailures(t *testing.T) {
	var writes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"This caller lacks the probes:write scope.","code":"forbidden"}`))
	}))
	defer server.Close()

	r := probeResource(server.URL, true)
	state := probeModel{Id: types.StringValue("p-1"), Etag: types.StringValue("etag-1")}

	var response probeResponse
	apiErr, _ := r.write(context.Background(), http.MethodPatch, &state, map[string]string{"name": "x"}, &response)
	if apiErr == nil || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("a 403 should reach the caller, got %v", apiErr)
	}
	if writes != 1 {
		t.Errorf("only a self-inflicted 412 is retried, got %d writes", writes)
	}
}

// A usage group is addressed through its set on every route, so an import
// address has to carry both ids. Only the rejected forms are exercised here: a
// well-formed address goes on to write state, which needs a real schema behind
// it and is covered by the resource's own acceptance test.
func TestImportUsageGroupRejectsAddressesMissingAnId(t *testing.T) {
	for _, id := range []string{"just-the-group-id", "/group", "set/", ""} {
		var resp resource.ImportStateResponse
		importUsageGroup(context.Background(), resource.ImportStateRequest{ID: id}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Errorf("%q is not a usable import address and should be rejected", id)
			continue
		}
		if !strings.Contains(resp.Diagnostics[0].Detail(), "usage_group_set_id/usage_group_id") {
			t.Errorf("the error should show the expected form, got: %s", resp.Diagnostics[0].Detail())
		}
	}
}
