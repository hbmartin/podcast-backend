#!/usr/bin/env bash
set -euo pipefail

image="${1:?image with digest is required}"
version="${2:?deployment version is required}"
: "${RAILWAY_TOKEN:?RAILWAY_TOKEN is required}"
: "${RAILWAY_PROJECT_ID:?RAILWAY_PROJECT_ID is required}"
: "${RAILWAY_ENVIRONMENT_ID:?RAILWAY_ENVIRONMENT_ID is required}"
: "${RAILWAY_SERVICE_IDS:?comma-separated RAILWAY_SERVICE_IDS is required}"

endpoint="https://backboard.railway.com/graphql/v2"
graphql() {
  local query="$1"
  local variables="$2"
  local response
  response="$(curl --fail-with-body --silent --show-error "$endpoint" \
    -H "Authorization: Bearer ${RAILWAY_TOKEN}" \
    -H "Content-Type: application/json" \
    --data-binary "$(jq -cn --arg query "$query" --argjson variables "$variables" '{query:$query,variables:$variables}')")"
  if [[ "$(jq '.errors | length // 0' <<<"$response")" != "0" ]]; then
    jq . >&2 <<<"$response"
    return 1
  fi
}

IFS=',' read -r -a service_ids <<<"$RAILWAY_SERVICE_IDS"
for raw_service_id in "${service_ids[@]}"; do
  service_id="$(xargs <<<"$raw_service_id")"
  [[ -n "$service_id" ]] || continue

  # shellcheck disable=SC2016 # GraphQL variable names are intentionally literal.
  graphql \
    'mutation($id:String!,$input:ServiceConnectInput!){serviceConnect(id:$id,input:$input){id}}' \
    "$(jq -cn --arg id "$service_id" --arg image "$image" '{id:$id,input:{image:$image}}')"

  # shellcheck disable=SC2016 # GraphQL variable names are intentionally literal.
  graphql \
    'mutation($input:VariableUpsertInput!){variableUpsert(input:$input)}' \
    "$(jq -cn --arg project "$RAILWAY_PROJECT_ID" --arg environment "$RAILWAY_ENVIRONMENT_ID" --arg service "$service_id" --arg value "$version" '{input:{projectId:$project,environmentId:$environment,serviceId:$service,name:"DEPLOY_VERSION",value:$value,skipDeploys:true}}')"

  # shellcheck disable=SC2016 # GraphQL variable names are intentionally literal.
  graphql \
    'mutation($environmentId:String!,$serviceId:String!){serviceInstanceDeploy(environmentId:$environmentId,serviceId:$serviceId)}' \
    "$(jq -cn --arg environmentId "$RAILWAY_ENVIRONMENT_ID" --arg serviceId "$service_id" '{environmentId:$environmentId,serviceId:$serviceId}')"
done
