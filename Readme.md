# mdviewer-go

A lightweight local web app to browse and render Markdown files (`.md`, `.markdown`) from a folder.

## Requirements

- Go 1.22+

## Run

From the project root:

```bash
go run . -root . -port 8080
```

Then open:

- `http://localhost:8080`

## Run for a specific folder

Use `-root` with the folder you want to scan:

```bash
go run . -root /path/to/your/folder -port 8080
```

Example:

```bash
go run . -root ~/notes -port 8080
```

## Build and use the binary

Build a local binary:

```bash
mkdir -p bin
go build -o bin/mdviewer .
```

Run it:

```bash
./bin/mdviewer -root ~/notes -port 8080
```

## Install

### Option 1: Use the project helper command

```bash
source .vscode/commands.sh
Install
```

This builds and installs `mdviewer` into the first writable directory in your `PATH` (or `~/.local/bin` if needed).

### Option 2: Install with Go

```bash
go install .
```

Then run the installed binary from your Go bin directory (for example `$(go env GOPATH)/bin/mdviewer-go`).

## How to use

1. Start the server with `go run .` or the built/installed binary.
2. Open `http://localhost:8080` in your browser.
3. Select a Markdown file from the left sidebar.
4. Use **Hide Sidebar** for full-width reading, or toggle to **Show Raw**.

## Options

- `-root` (default `.`): Root directory scanned recursively for Markdown files.
- `-port` (default `8080`): HTTP port to listen on.

## Optional direct file link

You can open a specific file on startup using the `file` query parameter:

- `http://localhost:8080/?file=docs/readme.md`

For a shared full-screen view without the sidebar, add `fullscreen=1` (or `sidebar=hidden`):

- `http://localhost:8080/?file=docs/readme.md&fullscreen=1`

## Health endpoint

- `GET /api/health` returns `{"status":"ok"}`.

## Building from Source

### Prerequisites

- Go 1.22+

### Build for your current platform

```bash
mkdir -p bin
go build -o bin/mdviewer .
```

### Cross-compile for other platforms

```bash
# Linux (amd64)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mdviewer-linux-amd64 .

# Linux (arm64)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mdviewer-linux-arm64 .

# macOS Intel
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mdviewer-macos-amd64 .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mdviewer-macos-arm64 .

# Windows (amd64)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mdviewer-windows-amd64.exe .

# Windows (arm64)
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mdviewer-windows-arm64.exe .
```

## Releases

Pre-built binaries for Linux, macOS, and Windows are available on the [Releases](https://github.com/roraja/markdown-go/releases) page.

### Download and run

1. Download the binary for your platform from the latest release.
2. Make it executable (Linux/macOS):
   ```bash
   chmod +x mdviewer-*
   ```
3. Run it:
   ```bash
   ./mdviewer-linux-amd64 -root ~/notes -port 8080
   ```

### macOS Gatekeeper

macOS may block unsigned binaries. To allow it:

```bash
xattr -d com.apple.quarantine mdviewer-macos-arm64
```

Or go to **System Settings → Privacy & Security** and click **Open Anyway**.

### Creating a new release

Releases are automated via GitHub Actions. To publish a new version:

```bash
git tag v1.1.0
git push origin v1.1.0
```

This triggers the workflow in `.github/workflows/release.yml`, which builds binaries for all platforms and creates a GitHub Release with checksums.
