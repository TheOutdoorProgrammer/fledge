#!/usr/bin/env bash
# Publish an exported .ipa. Anything not passed as an input comes from the
# repository's fledge.yaml, read by the CLI so config has one implementation.
set -euo pipefail

fail() {
  echo "::error::$*"
  exit 1
}

# Mask before anything can print, so redaction covers every later line including
# whatever the CLI echoes. The bare host is masked too, since URLs contain it.
secure=false
if [ "${FLEDGE_SECURE:-false}" = "true" ]; then
  secure=true
  if [ -n "${FLEDGE_SERVER:-}" ]; then
    echo "::add-mask::${FLEDGE_SERVER%/}"
    echo "::add-mask::${FLEDGE_SERVER#https://}"
  fi
fi

say() {
  [ "$secure" = "true" ] || echo "$@"
}

version="${FLEDGE_VERSION:-latest}"
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported runner: $(uname -s)" ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

tools="$(mktemp -d)"
trap 'rm -rf "$tools"' EXIT

archive_name="fledge_${os}_${arch}.tar.gz"
if [ "$version" = "latest" ]; then
  download="https://github.com/TheOutdoorProgrammer/fledge/releases/latest/download/${archive_name}"
else
  download="https://github.com/TheOutdoorProgrammer/fledge/releases/download/${version}/${archive_name}"
fi

echo "::group::Publishing to Fledge"
curl --silent --show-error --fail --location "$download" \
  | tar -xz -C "$tools" fledge \
  || fail "could not download the fledge CLI from ${download}"
chmod +x "${tools}/fledge"

# Without a token, ask GitHub for a workload identity token instead. Nothing
# long lived is stored anywhere: it is minted per run and expires in minutes.
credential="${FLEDGE_TOKEN:-}"
if [ -z "$credential" ]; then
  if [ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ] || [ -z "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" ]; then
    fail "no token was given and this job cannot mint one. Add 'permissions: id-token: write' to the job, or pass a token."
  fi

  audience="${FLEDGE_AUDIENCE:-${FLEDGE_SERVER:-}}"
  [ -n "$audience" ] || fail "an audience is required when the server is not set as an input"

  claim="$(
    curl --silent --show-error --fail \
      --header "Authorization: bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}" \
      "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=$(jq -rn --arg a "$audience" '$a|@uri')" \
      || fail "could not mint a workload identity token"
  )"

  credential="$(printf '%s' "$claim" | jq -r '.value // empty')"
  [ -n "$credential" ] || fail "GitHub returned no identity token"
  echo "::add-mask::${credential}"
  echo "authenticating as ${GITHUB_REPOSITORY} via workload identity"
fi

notes="${FLEDGE_NOTES:-}"
if [ -z "$notes" ] && [ -n "${COMMIT_MESSAGE:-}" ]; then
  notes="$(printf '%s' "$COMMIT_MESSAGE" | head -n 1)"
fi

published="$(mktemp)"
trap 'rm -rf "$tools" "$published"' EXIT

set +e
FLEDGE_URL="${FLEDGE_SERVER:-}" \
FLEDGE_TOKEN="$credential" \
FLEDGE_SECURE="${FLEDGE_SECURE:-false}" \
  "${tools}/fledge" upload -json ${FLEDGE_IPA:+"$FLEDGE_IPA"} \
  ${notes:+-notes "$notes"} \
  >"$published" 2>"${published}.err"
status=$?
set -e

if [ $status -ne 0 ]; then
  fail "$(tail -c 600 "${published}.err")"
fi

get() { jq -r --arg key "$1" '.[$key] // ""' <"$published"; }

profile="$(get profile)"
{
  echo "install-url=$(get install_url)"
  echo "page-url=$(get page_url)"
  echo "build-id=$(get build_id)"
  echo "version=$(get version)"
  echo "build=$(get build)"
  echo "bundle-id=$(get bundle_id)"
  echo "profile=${profile}"
  echo "expires=$(get expires)"
} >>"$GITHUB_OUTPUT"

echo "published $(get name) $(get version) ($(get build))"
say "install page: $(get page_url)"
echo "::endgroup::"

if [ "$profile" = "development" ]; then
  message="This build is development signed, so every tester must enable Developer Mode and restart. Export with method release-testing to avoid that."
  if [ "${FLEDGE_STRICT:-false}" = "true" ]; then
    fail "$message"
  fi
  echo "::warning::$message"
fi

if [ "${FLEDGE_SUMMARY:-true}" = "true" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### $(get name) $(get version) ($(get build))"
    echo
    # Job summaries are not passed through the log masker, so a secure run has
    # to leave the link out rather than rely on redaction.
    if [ "$secure" = "true" ]; then
      echo "Published. The install link is withheld from this summary."
    else
      echo "[Open on a device]($(get page_url)) — use Safari, other browsers drop the install silently."
    fi
    echo
    echo "| | |"
    echo "| --- | --- |"
    echo "| Bundle | \`$(get bundle_id)\` |"
    echo "| Profile | ${profile} |"
    echo "| Devices | $(get devices) registered |"
    echo "| Profile expires | $(get expires) |"
  } >>"$GITHUB_STEP_SUMMARY"
fi
