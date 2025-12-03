#!/bin/bash
# Auto-increments patch version on each commit
# Called by post-commit hook or manually

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION_FILE="$REPO_ROOT/VERSION"

# Read current version
CURRENT_VERSION=$(head -1 "$VERSION_FILE")

# Parse version components
MAJOR=$(echo "$CURRENT_VERSION" | cut -d. -f1)
MINOR=$(echo "$CURRENT_VERSION" | cut -d. -f2)
PATCH=$(echo "$CURRENT_VERSION" | cut -d. -f3)

# Increment patch
NEW_PATCH=$((PATCH + 1))
NEW_VERSION="${MAJOR}.${MINOR}.${NEW_PATCH}"

# Write to VERSION file
echo "$NEW_VERSION" > "$VERSION_FILE"

echo "Updated VERSION: $CURRENT_VERSION → $NEW_VERSION"

