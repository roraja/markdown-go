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
