#!/bin/bash

# Build and install kvist
go build -o ~/.local/bin/kvist && echo "✓ Kvist installed to ~/.local/bin/kvist"

# Create kvist-dev script for development
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEV_SCRIPT="$HOME/.local/bin/kvist-dev"

if [ ! -f "$DEV_SCRIPT" ]; then
    cat > "$DEV_SCRIPT" << EOF
#!/bin/bash
cd "$SCRIPT_DIR" && go run main.go "\$@"
EOF
    chmod +x "$DEV_SCRIPT"
    echo "✓ Development script installed to ~/.local/bin/kvist-dev"
else
    echo "✓ Development script already exists at ~/.local/bin/kvist-dev"
fi