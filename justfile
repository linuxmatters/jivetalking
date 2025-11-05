# Jivetalking - Just Commands

# Default recipe (shows available commands)
default:
    @just --list

# Build the jivetalking binary
build:
    go build -o jivetalking ./cmd/jivetalking

# Clean build artifacts
clean:
    rm -fv jivetalking 2>/dev/null || true

# Make a VHS tape recording
vhs: build
    @vhs ./jivetalking.tape

# Run tests
test:
    go test ./...
