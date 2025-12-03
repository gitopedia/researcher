#!/bin/bash
# Updates VERSION file with git commit info for development builds
# Called by post-commit hook or manually

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION_FILE="$REPO_ROOT/VERSION"

# Read base version (first line, strip any existing suffix)
BASE_VERSION=$(head -1 "$VERSION_FILE" | sed 's/-.*//')

# Get short commit hash
COMMIT_HASH=$(git rev-parse --short HEAD)

# Check if working directory is dirty
if [[ -n $(git status --porcelain) ]]; then
    DIRTY="-dirty"
else
    DIRTY=""
fi

# Build full version string
FULL_VERSION="${BASE_VERSION}-${COMMIT_HASH}${DIRTY}"

# Write to VERSION file
echo "$FULL_VERSION" > "$VERSION_FILE"

echo "Updated VERSION to: $FULL_VERSION"

