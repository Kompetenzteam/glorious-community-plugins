#!/usr/bin/env bash
#
# verify-consistency.sh — self-consistency proof for one release run.
#
# For every platform asset declared in index.json this script downloads the
# REAL bytes from BOTH the Gitea release (web download path) and the GitHub
# release (asset API with Accept: application/octet-stream), computes the
# sha256 of each, reads the sha256 recorded in index.json and compares the
# THREE values per asset:
#
#     GitHub-asset == Gitea-asset == index.json
#
# The Go build is NOT byte-reproducible, so the sizes/hashes legitimately
# change from run to run. This script therefore never checks against a fixed
# expected hash — it only proves that the three sources OF THE SAME RUN agree.
#
# Fail-closed: any mismatch, any missing asset or any missing download exits 1.
# On success it prints exactly one line per asset:
#     OK <name> gitea=<sha> github=<sha> index=<sha>
#
# All inputs are taken from the environment (never interpolated into this
# script by the workflow runner). Required env vars:
#   GITEA_URL       base URL of the Gitea instance (e.g. http://gitea:3000)
#   GH_REPOSITORY   owner/repo (same slug on Gitea and GitHub)
#   GIT_TOKEN       Gitea access token
#   GH_PAT          GitHub personal access token
#   TAG             release tag (e.g. hello-1.0.0)
# Optional:
#   INDEX_JSON      path to index.json (default: index.json)

# Only curl + python3 + sha256sum are used — all present in the standard
# GitHub/Gitea ubuntu-latest runner image. Deliberately no jq dependency.

set -euo pipefail

die() { echo "::error::$*" >&2; exit 1; }

: "${GITEA_URL:?GITEA_URL must be set}"
: "${GH_REPOSITORY:?GH_REPOSITORY must be set}"
: "${GIT_TOKEN:?GIT_TOKEN must be set}"
: "${GH_PAT:?GH_PAT must be set}"
: "${TAG:?TAG must be set}"
INDEX_JSON="${INDEX_JSON:-index.json}"

command -v python3 >/dev/null 2>&1 || die "python3 not found on PATH"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum not found on PATH"
[[ -f "$INDEX_JSON" ]] || die "index.json not found at '$INDEX_JSON'"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

gitea_api="${GITEA_URL%/}/api/v1/repos/${GH_REPOSITORY}"
gitea_web="${GITEA_URL%/}/${GH_REPOSITORY}"
gh_api="https://api.github.com"

# ---- fetch release metadata from both sides -------------------------------
g_code="$(curl -sS -o "$work/gitea.json" -w '%{http_code}' --max-time 60 \
  -H "Authorization: token ${GIT_TOKEN}" \
  "${gitea_api}/releases/tags/${TAG}")" || die "curl to Gitea failed"
case "$g_code" in
  2*) : ;;
  *) die "Gitea release '${TAG}' not readable (HTTP ${g_code}): $(cat "$work/gitea.json")" ;;
esac

h_code="$(curl -sS -o "$work/github.json" -w '%{http_code}' --max-time 60 \
  -H "Authorization: Bearer ${GH_PAT}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "${gh_api}/repos/${GH_REPOSITORY}/releases/tags/${TAG}")" || die "curl to GitHub failed"
case "$h_code" in
  2*) : ;;
  *) die "GitHub release '${TAG}' not readable (HTTP ${h_code}): $(cat "$work/github.json")" ;;
esac

# ---- collect assets from index.json, Gitea and GitHub ---------------------
# Emits three TAB-separated tables consumed by the driver loop below:
#   expected.tsv : <filename>\t<index-sha256>
#   gitea.tsv    : <filename>\t<size-bytes>
#   github.tsv   : <filename>\t<api-download-url>
python3 - "$INDEX_JSON" "$work/gitea.json" "$work/github.json" \
         "$work/expected.tsv" "$work/gitea.tsv" "$work/github.tsv" "$TAG" <<'PY'
import json, sys

idx_path, gitea_path, github_path, exp_out, gt_out, gh_out, tag = sys.argv[1:8]

idx = json.load(open(idx_path, encoding="utf-8"))
gitea = {a["name"]: a for a in json.load(open(gitea_path, encoding="utf-8")).get("assets", [])}
github = {a["name"]: a for a in json.load(open(github_path, encoding="utf-8")).get("assets", [])}

# Scope the check to THE RELEASE UNDER TEST. index.json is a catalog of EVERY
# published plugin (hello, routeexample, ...), but a single release run only
# publishes the assets tagged with this TAG. Iterating the whole catalog here
# would demand that hello's assets exist inside the routeexample release and
# fail with a spurious MISSING (the earlier red verify-consistency). We select
# exactly those entries whose asset URL points at this tag's release path,
# i.e. .../releases/<download|tag>/<tag>/<file>.
tag_segment = f"/releases/download/{tag}/"
expected = {}
for plugin in idx.get("plugins", []):
    for plat, info in plugin.get("platforms", {}).items():
        url = info.get("url", "")
        if tag_segment not in url:
            continue
        fn = url.rsplit("/", 1)[-1]
        sha = info.get("sha256", "")
        if not fn:
            sys.stderr.write(f"::error::plugin {plugin.get('name')}/{plat} has no asset url\n")
            sys.exit(1)
        if not sha:
            sys.stderr.write(f"::error::plugin {plugin.get('name')}/{plat} ({fn}) has no sha256 in index.json\n")
            sys.exit(1)
        expected[fn] = sha

# Fail-closed: the release under test MUST have at least one catalog entry.
# An empty selection means the tag matched no index entry at all (typo'd tag,
# missing update-index push) — never treat that as "nothing to do".
if not expected:
    sys.stderr.write(f"::error::index.json declares no platform assets for tag {tag} — nothing to verify\n")
    sys.exit(1)

# Every selected asset must also be present in the release under test on BOTH
# hosts; a mismatched catalog/release pairing is a hard error, not a skip.
for fn in expected:
    if fn not in gitea:
        sys.stderr.write(f"::error::MISSING {fn}: not present in Gitea release {tag}\n")
        sys.exit(1)
    if fn not in github:
        sys.stderr.write(f"::error::MISSING {fn}: not present in GitHub release {tag}\n")
        sys.exit(1)

with open(exp_out, "w", encoding="utf-8", newline="\n") as fh:
    for fn, sha in expected.items():
        fh.write(f"{fn}\t{sha}\n")
with open(gt_out, "w", encoding="utf-8", newline="\n") as fh:
    for fn, a in gitea.items():
        fh.write(f"{fn}\t{a.get('size', '')}\n")
with open(gh_out, "w", encoding="utf-8", newline="\n") as fh:
    for fn, a in github.items():
        fh.write(f"{fn}\t{a.get('url', '')}\n")
PY

[[ -s "$work/expected.tsv" ]] || die "index.json declares no platform assets — nothing to verify"

# ---- load name sets into associative arrays (portable, no path quoting) ----
declare -A gitea_size=() github_url=()
while IFS=$'\t' read -r n sz; do
  n="${n%$'\r'}"; sz="${sz%$'\r'}"
  [[ -n "$n" ]] && gitea_size["$n"]="$sz"
done < "$work/gitea.tsv"
while IFS=$'\t' read -r n u; do
  n="${n%$'\r'}"; u="${u%$'\r'}"
  [[ -n "$n" ]] && github_url["$n"]="$u"
done < "$work/github.tsv"

# ---- download real bytes and compare --------------------------------------
fail=0
count=0

while IFS=$'\t' read -r fn idx_sha; do
  fn="${fn%$'\r'}"; idx_sha="${idx_sha%$'\r'}"
  [[ -n "$fn" ]] || continue
  count=$((count + 1))

  if [[ -z "${gitea_size[$fn]+x}" ]]; then
    echo "::error::MISSING ${fn}: not present in Gitea release ${TAG}" >&2
    fail=1
    continue
  fi
  if [[ -z "${github_url[$fn]+x}" ]]; then
    echo "::error::MISSING ${fn}: not present in GitHub release ${TAG}" >&2
    fail=1
    continue
  fi

  gh_url="${github_url[$fn]}"

  # GitHub: real bytes via the asset API download endpoint
  if ! curl -fsSL --max-time 600 \
      -H "Authorization: Bearer ${GH_PAT}" \
      -H "Accept: application/octet-stream" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "$gh_url" -o "$work/gh_${fn}"; then
    echo "::error::github download failed for ${fn}" >&2
    fail=1
    continue
  fi

  # Gitea: real bytes via the WEB download path (the API asset endpoint
  # returns metadata JSON and must never be hashed here)
  enc_name="$(python3 -c 'import sys,urllib.parse;print(urllib.parse.quote(sys.argv[1], safe=""))' "$fn")"
  if ! curl -fsSL --max-time 600 \
      -H "Authorization: token ${GIT_TOKEN}" \
      "${gitea_web}/releases/download/${TAG}/${enc_name}" -o "$work/gt_${fn}"; then
    echo "::error::gitea download failed for ${fn}" >&2
    fail=1
    continue
  fi

  # GNU coreutils prints "<sha>  <name>"; MSYS/coreutils-8 in escape mode may
  # prefix a '\'. Normalise to the first run of 64 hex chars so the comparison
  # is exact on both the Linux runner and a Windows/MSYS shell.
  gh_sha="$(sha256sum "$work/gh_${fn}" | tr -d '\\' | cut -c1-64)"
  gt_sha="$(sha256sum "$work/gt_${fn}" | tr -d '\\' | cut -c1-64)"

  if [[ "$gh_sha" != "$idx_sha" || "$gh_sha" != "$gt_sha" ]]; then
    echo "::error::HASH-MISMATCH ${fn}: gitea=${gt_sha} github=${gh_sha} index=${idx_sha}" >&2
    fail=1
    continue
  fi

  echo "OK ${fn} gitea=${gt_sha} github=${gh_sha} index=${idx_sha}"
done < "$work/expected.tsv"

if [[ "$fail" -ne 0 ]]; then
  die "consistency check FAILED — at least one asset diverges between Gitea, GitHub and index.json"
fi

echo "consistency green: ${count} asset(s) byte-identical across Gitea, GitHub and index.json (tag ${TAG})."
