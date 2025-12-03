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
# Post-commit hook: auto-increment patch version after each commit

REPO_ROOT="$(git rev-parse --show-toplevel)"
UPDATE_SCRIPT="$REPO_ROOT/scripts/update-version.sh"

if [[ -x "$UPDATE_SCRIPT" ]]; then
    "$UPDATE_SCRIPT"
fi
EOF

chmod +x "$HOOKS_DIR/post-commit"
echo "✓ Installed post-commit hook"

echo ""
echo "Git hooks installed successfully!"
echo ""
echo "How it works:"
echo "  - post-commit: Auto-increments patch version (0.3.0 → 0.3.1 → 0.3.2)"
echo ""
echo "Version progression:"
echo "  - Each commit increments patch: 0.3.0 → 0.3.1 → 0.3.2 ..."
echo "  - Bump minor manually for features: 0.3.x → 0.4.0"
echo "  - Bump major manually for breaking changes: 0.x.y → 1.0.0"

