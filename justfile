# Jivetalking - Just Commands

# List commands
default:
    @just --list

# Check ffmpeg-statigo submodule is present
_check-submodule:
    #!/usr/bin/env bash
    if [ ! -f "third_party/ffmpeg-statigo/go.mod" ]; then
        echo "Error: ffmpeg-statigo submodule not initialised. Run 'just setup' first."
        exit 1
    fi
    if [ ! -f "third_party/ffmpeg-statigo/lib/$(go env GOOS)_$(go env GOARCH)/libffmpeg.a" ]; then
        echo "Error: ffmpeg-statigo library not downloaded. Run 'just setup' first."
        exit 1
    fi

# Get latest stable ffmpeg-statigo release tag from GitHub
_get-latest-tag:
    #!/usr/bin/env bash
    if command -v jq &> /dev/null; then
        curl -s https://api.github.com/repos/linuxmatters/ffmpeg-statigo/releases | \
            jq -r '[.[] | select(.prerelease == false and .draft == false and (.tag_name | startswith("v")))][0].tag_name'
    else
        curl -s https://api.github.com/repos/linuxmatters/ffmpeg-statigo/releases | \
            grep -B5 '"prerelease": false' | grep '"tag_name"' | grep -v 'lib-' | head -1 | cut -d'"' -f4
    fi

# Setup or update ffmpeg-statigo submodule and library
setup:
    #!/usr/bin/env bash
    set -e
    echo "Configuring git for submodule-friendly pulls..."
    git config pull.ff only
    git config submodule.recurse true

    TAG=$(just _get-latest-tag)
    if [ -z "$TAG" ] || [ "$TAG" = "null" ]; then
        echo "Error: Could not fetch latest release tag"
        exit 1
    fi

    if [ ! -f "third_party/ffmpeg-statigo/go.mod" ]; then
        echo "Initialising ffmpeg-statigo submodule..."
        git submodule update --init --recursive
    fi

    cd third_party/ffmpeg-statigo
    git fetch --tags
    CURRENT=$(git describe --tags --exact-match 2>/dev/null || echo "")

    if [ "$CURRENT" = "$TAG" ]; then
        echo "ffmpeg-statigo already at latest version ($TAG)"
        cd ../..
    else
        if [ -n "$CURRENT" ]; then
            echo "Updating ffmpeg-statigo from $CURRENT to $TAG..."
        else
            echo "Setting up ffmpeg-statigo $TAG..."
        fi
        git checkout "$TAG"
        cd ../..
        rm -f third_party/ffmpeg-statigo/lib/*/libffmpeg.a
        git add third_party/ffmpeg-statigo
    fi

    echo "Checking ffmpeg-statigo libraries..."
    cd third_party/ffmpeg-statigo && go run ./cmd/download-lib
    cd ../..

    if git diff --cached --quiet third_party/ffmpeg-statigo; then
        echo "Setup complete!"
    else
        echo ""
        echo "Setup complete! Submodule updated to $TAG"
        echo "Don't forget to commit: git commit -m 'chore: update ffmpeg-statigo to $TAG'"
    fi

# Build jivetalking
build: _check-submodule
    #!/usr/bin/env bash
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    echo "Building jivetalking version: $VERSION"
    CGO_ENABLED=1 go build -ldflags="-X main.version=$VERSION" -o jivetalking ./cmd/jivetalking

# Clean build artifacts
clean:
    rm -fv jivetalking 2>/dev/null || true

# Run tests
test: _check-submodule
    go test ./...

# Install jivetalking to ~/.local/bin
install: build
    @mkdir -p ~/.local/bin 2>/dev/null || true
    @mv ./jivetalking ~/.local/bin/jivetalking
    @echo "Installed jivetalking to ~/.local/bin/jivetalking"
    @echo "Make sure ~/.local/bin is in your PATH"

# Make a VHS tape recording
vhs: build
    @vhs ./jivetalking.tape

# Process Mark
mark: build
    rm -f testdata/LMP-69-mark-processed.*
    ./jivetalking --logs testdata/LMP-69-mark.flac
    cat testdata/LMP-69-mark-processed.log

# Process Martin
martin: build
    rm -f testdata/LMP-69-martin-processed.*
    ./jivetalking --logs testdata/LMP-69-martin.flac
    cat testdata/LMP-69-martin-processed.log

# Process popey
popey: build
    rm -f testdata/LMP-69-popey-processed.*
    ./jivetalking --logs testdata/LMP-69-popey.flac
    cat testdata/LMP-69-popey-processed.log
