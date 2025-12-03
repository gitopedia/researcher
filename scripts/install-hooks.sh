#!/bin/bash
# Installs git hooks for the researcher repository

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HOOKS_DIR="$REPO_ROOT/.git/hooks"

echo "Installing git hooks..."

# Create post-commit hook
cat > "$HOOKS_DIR/post-commit" << 'EOF'
#!/bin/bash
# Post-commit hook: update VERSION with commit hash

REPO_ROOT="$(git rev-parse --show-toplevel)"
UPDATE_SCRIPT="$REPO_ROOT/scripts/update-version.sh"

if [[ -x "$UPDATE_SCRIPT" ]]; then
    "$UPDATE_SCRIPT"
fi
EOF

chmod +x "$HOOKS_DIR/post-commit"
echo "✓ Installed post-commit hook"

# Create pre-commit hook to reset version before commit
cat > "$HOOKS_DIR/pre-commit" << 'EOF'
#!/bin/bash
# Pre-commit hook: ensure VERSION contains only base version (no commit suffix)
# This prevents the commit hash from being committed to the repo

REPO_ROOT="$(git rev-parse --show-toplevel)"
VERSION_FILE="$REPO_ROOT/VERSION"

if [[ -f "$VERSION_FILE" ]]; then
    # Extract base version (remove any -hash or -dirty suffix)
    BASE_VERSION=$(head -1 "$VERSION_FILE" | sed 's/-.*//')
    
    # Check if VERSION is staged and has a suffix
    CURRENT=$(head -1 "$VERSION_FILE")
    if [[ "$CURRENT" != "$BASE_VERSION" ]]; then
        echo "$BASE_VERSION" > "$VERSION_FILE"
        git add "$VERSION_FILE"
        echo "Reset VERSION to base: $BASE_VERSION"
    fi
fi
EOF

chmod +x "$HOOKS_DIR/pre-commit"
echo "✓ Installed pre-commit hook"

echo ""
echo "Git hooks installed successfully!"
echo ""
echo "How it works:"
echo "  - pre-commit: Ensures VERSION file contains only base version (e.g., 0.3.0)"
echo "  - post-commit: Updates VERSION with commit hash (e.g., 0.3.0-abc1234)"
echo ""
echo "Your local VERSION will show: 0.3.0-<commit-hash>"
echo "The committed VERSION will show: 0.3.0"

