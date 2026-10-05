#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
#
# Delete connections, budgets, usage group sets, teams and role grants a test
# run left attached to the organization.
#
# `terraform test` destroys what it created, but a run cancelled mid-apply — or
# one whose destroy the API refused — leaves a resource behind. The next run
# then fails before it starts: SELECT rejects a second connection with a name
# already in use, and the same Snowflake account identifier cannot be added to an
# organization twice. So CI sweeps before and after, and the sweep has to be
# idempotent and safe to run against an organization holding nothing.
#
# Only resources whose name begins with CI_RESOURCE_PREFIX are touched. That
# prefix carries the workflow run id, so a sweep cleaning up after one run cannot
# reach into another's resources — with one exception, the pre-run sweep, which
# is deliberately given the bare prefix to reach older leaks.
#
# A role grant has no name. Two kinds of grant are swept anyway, and only when
# a test made them:
#
#   - A default role grant on a usage group in a set whose name has the
#     prefix. The role suite scopes its default grant to its own usage group.
#   - A direct grant to an email in CI_SWEEP_USER_EMAILS whose address starts
#     with the prefix. That email belongs to no real user, so every direct
#     grant it holds came from a test.
#
# A team role grant needs no sweep: deleting the team deletes its grants.
#
# Environment:
#   SELECT_API_KEY          key with <resource>:read and :write for every
#                           collection below, and for users and default_roles
#   SELECT_ORGANIZATION_ID  organization the resources belong to
#   SELECT_API_URL          defaults to https://api.select.dev
#   CI_RESOURCE_PREFIX      defaults to terraform-test
#   CI_SWEEP_USER_EMAILS    space-separated test emails whose direct role
#                           grants are deleted; optional

set -euo pipefail

: "${SELECT_API_KEY:?SELECT_API_KEY is required}"
: "${SELECT_ORGANIZATION_ID:?SELECT_ORGANIZATION_ID is required}"
API_URL="${SELECT_API_URL:-https://api.select.dev}"
PREFIX="${CI_RESOURCE_PREFIX:-terraform-test}"
USER_EMAILS="${CI_SWEEP_USER_EMAILS:-}"

COLLECTIONS=(
  snowflake-accounts
  databricks-connections
  bigquery-connections
  aws-accounts
  budgets
  # Deleting a set also deletes the groups nested under it, so no separate
  # sweep of usage-group-sets/{id}/usage-groups is needed.
  usage-group-sets
  # Deleting a team also deletes its memberships and role grants, so no
  # separate sweep of teams/{id}/members is needed. Only teams whose name has
  # the prefix are touched, so the built-in all-users team is never deleted.
  teams
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

# Print every item of a list route, one JSON object per line, from all of its
# pages. Returns 1, with a message, when a page cannot be listed. With a second
# argument of "allow-404", a 404 prints nothing and returns 0: the parent is
# gone, so it holds nothing to delete.
list_items() {
  local path="$1" allow_404="${2:-}"
  local page_token='' query body status

  while :; do
    query="?max_results=100"
    if [[ -n "$page_token" ]]; then
      query="${query}&page_token=${page_token}"
    fi

    body="$(api GET "${path}${query}")"
    status="${body##*$'\n'}"
    body="${body%$'\n'*}"

    if [[ "$status" == "404" && "$allow_404" == "allow-404" ]]; then
      return 0
    fi
    if [[ "$status" != "200" ]]; then
      echo "  ! could not list ${path} (HTTP ${status}): ${body}" >&2
      return 1
    fi

    jq -c '.items[]' <<<"$body"

    page_token="$(jq -r '.page_token // empty' <<<"$body")"
    [[ -n "$page_token" ]] || break
  done
}

# Delete one item with If-Match, and count the result.
delete_item() {
  local path="$1" etag="$2" label="$3"
  local del del_status

  echo "  - deleting ${path} (${label})"
  del="$(api DELETE "$path" -H "If-Match: ${etag}")"
  del_status="${del##*$'\n'}"
  del="${del%$'\n'*}"

  # 404 means someone else already removed it, which is the state we want.
  if [[ "$del_status" == "204" || "$del_status" == "200" || "$del_status" == "404" ]]; then
    deleted=$((deleted + 1))
  else
    echo "  ! delete failed (HTTP ${del_status}): ${del}" >&2
    failed=$((failed + 1))
  fi
}

# Delete the default role grants scoped to a usage group in a set whose name
# has the prefix. This has to run before the usage-group-sets sweep: after the
# set is gone, nothing tells which grant a test made.
sweep_default_roles() {
  local sets set_id groups grants
  local -a group_ids=()

  if ! sets="$(list_items /usage-group-sets)"; then
    failed=$((failed + 1))
    return
  fi
  while read -r set_id; do
    [[ -n "$set_id" ]] || continue
    if ! groups="$(list_items "/usage-group-sets/${set_id}/usage-groups" allow-404)"; then
      failed=$((failed + 1))
      continue
    fi
    while read -r group_id; do
      [[ -n "$group_id" ]] && group_ids+=("$group_id")
    done < <(jq -r '.id' <<<"$groups")
  done < <(jq -r --arg prefix "$PREFIX" 'select(.name | startswith($prefix)) | .id' <<<"$sets")

  [[ ${#group_ids[@]} -gt 0 ]] || return 0

  if ! grants="$(list_items /default-roles)"; then
    failed=$((failed + 1))
    return
  fi
  while IFS=$'\t' read -r id etag role group; do
    [[ -n "$id" ]] || continue
    delete_item "/default-roles/${id}" "$etag" "${role} on usage group ${group}"
  done < <(jq -r --args '
      ($ARGS.positional) as $groups
      | (.usage_group_id // (if .entity.type == "usage_group" then .entity.id else null end)) as $group
      | select($group != null and any($groups[]; . == $group))
      | [.id, .etag, .role, $group] | @tsv' \
    "${group_ids[@]}" <<<"$grants")
}

# Delete every direct role grant held by each email in CI_SWEEP_USER_EMAILS
# that starts with the prefix. A default or team-inherited grant is not
# revoked here: it is revoked at its source.
sweep_user_roles() {
  local email escaped grants

  for email in $USER_EMAILS; do
    [[ "$email" == "$PREFIX"* ]] || continue
    escaped="$(jq -rn --arg email "$email" '$email | @uri')"
    # The API can answer 404 for an email that holds no grants.
    if ! grants="$(list_items "/users/${escaped}/roles" allow-404)"; then
      failed=$((failed + 1))
      continue
    fi
    while IFS=$'\t' read -r id etag role; do
      [[ -n "$id" ]] || continue
      delete_item "/users/${escaped}/roles/${id}" "$etag" "${role} for ${email}"
    done < <(jq -r 'select(.is_default == false and .granted_from_team_name == null) | [.id, .etag, .role] | @tsv' <<<"$grants")
  done
}

echo "Sweeping resources named '${PREFIX}*' from organization ${SELECT_ORGANIZATION_ID} at ${API_URL}"
echo "default-roles:"
sweep_default_roles
echo "user roles:"
sweep_user_roles
for collection in "${COLLECTIONS[@]}"; do
  echo "${collection}:"
  sweep_collection "$collection"
done

echo "Sweep complete: ${deleted} deleted, ${failed} failed."
# A leak left behind breaks the next run, so it fails the step rather than being
# reported and forgotten.
[[ "$failed" -eq 0 ]]
