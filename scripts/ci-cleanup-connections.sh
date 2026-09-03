#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
#
# Delete connections a test run left attached to the organization.
#
# `terraform test` destroys what it created, but a run cancelled mid-apply — or
# one whose destroy the API refused — leaves a connection behind. The next run
# then fails before it starts: SELECT rejects a second connection with a name
# already in use, and the same Snowflake account identifier cannot be added to an
# organization twice. So CI sweeps before and after, and the sweep has to be
# idempotent and safe to run against an organization holding nothing.
#
# Only connections whose name begins with CI_RESOURCE_PREFIX are touched. That
# prefix carries the workflow run id, so a sweep cleaning up after one run cannot
# reach into another's resources — with one exception, the pre-run sweep, which
# is deliberately given the bare prefix to reach older leaks.
#
# Environment:
#   SELECT_API_KEY          key with <resource>:read and :write for all four
#   SELECT_ORGANIZATION_ID  organization the connections belong to
#   SELECT_API_URL          defaults to https://api.select.dev
#   CI_RESOURCE_PREFIX      defaults to terraform-test

set -euo pipefail

: "${SELECT_API_KEY:?SELECT_API_KEY is required}"
: "${SELECT_ORGANIZATION_ID:?SELECT_ORGANIZATION_ID is required}"
API_URL="${SELECT_API_URL:-https://api.select.dev}"
PREFIX="${CI_RESOURCE_PREFIX:-terraform-test}"

COLLECTIONS=(
  snowflake-accounts
  databricks-connections
  bigquery-connections
  aws-accounts
)

deleted=0
failed=0

# The API's own request, with the two headers every call needs. Emits the body
# followed by a final line holding the status, so a caller gets both from one
# invocation without a temporary file.
api() {
  local method="$1" path="$2"
  shift 2
  curl -sS -X "$method" "${API_URL}/v2${path}" \
    -H "Authorization: Bearer ${SELECT_API_KEY}" \
    -H "x-tenant-id: ${SELECT_ORGANIZATION_ID}" \
    -w '\n%{http_code}' \
    "$@"
}

sweep_collection() {
  local collection="$1"
  local page_token='' query body status

  while :; do
    query="?max_results=100"
    if [[ -n "$page_token" ]]; then
      query="${query}&page_token=${page_token}"
    fi

    body="$(api GET "/${collection}${query}")"
    status="${body##*$'\n'}"
    body="${body%$'\n'*}"

    if [[ "$status" != "200" ]]; then
      echo "  ! could not list ${collection} (HTTP ${status}): ${body}" >&2
      failed=$((failed + 1))
      return
    fi

    # id and etag together: the delete needs If-Match, and taking the etag from
    # the list avoids a second GET per connection.
    while IFS=$'\t' read -r id etag name; do
      [[ -n "$id" ]] || continue
      echo "  - deleting ${collection}/${id} (${name})"
      local del del_status
      del="$(api DELETE "/${collection}/${id}" -H "If-Match: ${etag}")"
      del_status="${del##*$'\n'}"
      del="${del%$'\n'*}"

      # 404 means someone else already removed it, which is the state we want.
      if [[ "$del_status" == "204" || "$del_status" == "200" || "$del_status" == "404" ]]; then
        deleted=$((deleted + 1))
      else
        echo "  ! delete failed (HTTP ${del_status}): ${del}" >&2
        failed=$((failed + 1))
      fi
    done < <(jq -r --arg prefix "$PREFIX" \
      '.items[] | select(.name | startswith($prefix)) | [.id, .etag, .name] | @tsv' \
      <<<"$body")

    page_token="$(jq -r '.page_token // empty' <<<"$body")"
    [[ -n "$page_token" ]] || break
  done
}

echo "Sweeping connections named '${PREFIX}*' from organization ${SELECT_ORGANIZATION_ID} at ${API_URL}"
for collection in "${COLLECTIONS[@]}"; do
  echo "${collection}:"
  sweep_collection "$collection"
done

echo "Sweep complete: ${deleted} deleted, ${failed} failed."
# A leak left behind breaks the next run, so it fails the step rather than being
# reported and forgotten.
[[ "$failed" -eq 0 ]]
