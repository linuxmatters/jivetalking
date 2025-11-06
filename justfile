# Jivetalking - Just Commands

# Default recipe (shows available commands)
default:
    @just --list

# Build the jivetalking binary
build: clean
    go build -o jivetalking ./cmd/jivetalking

# Clean build artifacts
clean:
    rm -fv jivetalking 2>/dev/null || true

# Make a VHS tape recording
vhs: build
    @vhs ./jivetalking.tape

mark: build
    rm -f testdata/LMP-69-mark-processed.*
    ./jivetalking --logs testdata/LMP-69-mark.flac

martin: build
    rm -f testdata/LMP-69-martin-processed.*
    ./jivetalking --logs testdata/LMP-69-martin.flac

popey: build
    rm -f testdata/LMP-69-popey-processed.*
    ./jivetalking --logs testdata/LMP-69-popey.flac

# Run tests
test:
    go test ./...
