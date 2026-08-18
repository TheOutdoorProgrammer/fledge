#!/usr/bin/env bash
# Upload an exported .ipa to a Fledge server and expose the result as outputs.
set -euo pipefail

fail() {
  echo "::error::$*"
  exit 1
}

[ -n "${FLEDGE_SERVER:-}" ] || fail "server is required"
[ -n "${FLEDGE_TOKEN:-}" ] || fail "token is required"
[ -n "${FLEDGE_IPA:-}" ] || fail "ipa is required"

case "$FLEDGE_SERVER" in
  https://*) ;;
  # iOS refuses to install over plain HTTP and says nothing when it does, so a
  # non-HTTPS server is a failure worth catching in CI rather than on a phone.
  *) fail "server must be https, got ${FLEDGE_SERVER}" ;;
esac
server="${FLEDGE_SERVER%/}"

# A glob is convenient because exporters name the archive after the scheme, but
# it has to resolve to exactly one file or the wrong build gets published.
shopt -s nullglob
# shellcheck disable=SC2206 # unquoted so the glob expands
matches=($FLEDGE_IPA)
shopt -u nullglob

case ${#matches[@]} in
  0) fail "no archive matched ${FLEDGE_IPA}" ;;
  1) archive="${matches[0]}" ;;
  *) fail "${FLEDGE_IPA} matched ${#matches[@]} files: ${matches[*]}" ;;
esac

[ -r "$archive" ] || fail "cannot read ${archive}"

notes="${FLEDGE_NOTES:-}"
if [ -z "$notes" ] && [ -n "${COMMIT_MESSAGE:-}" ]; then
  notes="$(printf '%s' "$COMMIT_MESSAGE" | head -n 1)"
fi

echo "::group::Publishing $(basename "$archive") to ${server}"

body="$(mktemp)"
trap 'rm -f "$body"' EXIT

status=$(
  curl --silent --show-error --location --fail-with-body \
    --write-out '%{http_code}' \
    --output "$body" \
    --request POST "${server}/api/builds" \
    --header "Authorization: Bearer ${FLEDGE_TOKEN}" \
    --header "Content-Type: application/octet-stream" \
    --header "X-Fledge-Notes: ${notes}" \
    --data-binary "@${archive}" \
    || true
)

if [ "$status" != "201" ]; then
  detail="$(jq -r '.error // empty' <"$body" 2>/dev/null || true)"
  [ -n "$detail" ] || detail="$(head -c 400 "$body")"
  fail "fledge returned ${status:-no response}: ${detail}"
fi

get() { jq -r --arg key "$1" '.[$key] // ""' <"$body"; }

install_url="$(get install_url)"
page_url="$(get page_url)"
profile="$(get profile)"

{
  echo "install-url=${install_url}"
  echo "page-url=${page_url}"
  echo "build-id=$(get build_id)"
  echo "version=$(get version)"
  echo "build=$(get build)"
  echo "bundle-id=$(get bundle_id)"
  echo "profile=${profile}"
  echo "expires=$(get expires)"
} >>"$GITHUB_OUTPUT"

echo "published $(get name) $(get version) ($(get build))"
echo "install page: ${page_url}"
echo "::endgroup::"

if [ "$profile" = "development" ]; then
  message="This build is development signed, so every tester must enable Developer Mode and restart. Export with -exportOptionsPlist method release-testing to avoid that."
  if [ "${FLEDGE_STRICT:-false}" = "true" ]; then
    fail "$message"
  fi
  echo "::warning::$message"
fi

if [ "${FLEDGE_SUMMARY:-true}" = "true" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### $(get name) $(get version) ($(get build))"
    echo
    echo "[Open on a device]($page_url) — use Safari, other browsers drop the install silently."
    echo
    echo "| | |"
    echo "| --- | --- |"
    echo "| Bundle | \`$(get bundle_id)\` |"
    echo "| Profile | ${profile} |"
    echo "| Devices | $(get devices) registered |"
    echo "| Profile expires | $(get expires) |"
  } >>"$GITHUB_STEP_SUMMARY"
fi
