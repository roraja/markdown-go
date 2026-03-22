# mdviewer-go

A lightweight local web app to browse and render Markdown files (`.md`, `.markdown`) from a folder.

## Quick Install

**macOS / Linux:**
```bash
curl -fsSL https://raw.githubusercontent.com/roraja/markdown-go/master/install.sh | sh
```

**Windows (PowerShell):**
```powershell
iex (iwr 'https://raw.githubusercontent.com/roraja/markdown-go/master/install.ps1').Content
```

Then run:
```bash
mdviewer -root ~/your-notes -port 8080
```

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

The sidebar **live-updates** every 5 seconds — new files, deletions, and modification time changes appear automatically without refreshing the page.

## Options

- `-root` (default `.`): Root directory scanned recursively for Markdown files.
- `-port` (default `8080`): HTTP port to listen on.
- `-podcast-watch` (optional): Comma-separated list of directories and/or glob patterns to watch for auto podcast generation.

### Auto Podcast Generation (`-podcast-watch`)

Automatically generates podcasts for new or updated markdown files in watched locations.

```bash
# Watch specific directories
mdviewer -root ./docs -podcast-watch "guides,blog/posts"

# Watch filename patterns (matches across ALL directories)
mdviewer -root ./workspace -podcast-watch "*-review.md,*-concepts.md"

# Mix directories and patterns
mdviewer -root ./workspace -podcast-watch "journal,01-projects/pi-router,*code-review*.md,*-concepts.md"
```

**How it works:**
- Scans every 30 seconds for `.md` files in watched directories
- Glob patterns (`*`, `?`) match against filenames in any directory under root
- Auto-triggers podcast generation when:
  - A new `.md` file appears without a `.podcast.mp3` sibling
  - An existing `.md` file is newer than its `.podcast.mp3` (re-generates)
- One podcast generates at a time (queued) to avoid CPU overload
- State tracked in `~/.mdviewer/podcast-watch-state.json`

**Pattern rules:**
- Entry containing `*` or `?` → treated as a glob pattern (matched against filename only)
- Entry without wildcards → treated as a directory path (relative to `-root`)

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

## 🎙️ Podcast Generation

mdviewer can generate a two-person podcast conversation from any markdown document. Click the 🎙️ button in the toolbar to generate.

**How it works:**
1. An LLM reads the markdown and writes a natural two-person dialogue script
2. [Kokoro TTS](https://github.com/thewh1teagle/kokoro-onnx) (82M ONNX model) synthesizes speech with two distinct voices
3. The result is an MP3 with natural pauses between speakers

The podcast is a real conversation — not a read-aloud. The host (Sarah) explains concepts using analogies and the guest (Michael) asks genuine questions and pushes back.

### Setup

#### 1. Install Python dependencies

```bash
pip install kokoro-onnx pydub soundfile numpy
```

#### 2. Install ffmpeg (needed for MP3 export)

```bash
# macOS
brew install ffmpeg

# Ubuntu/Debian
sudo apt install ffmpeg

# Windows (via chocolatey)
choco install ffmpeg
```

#### 3. Download Kokoro TTS model (~338MB total)

```bash
mkdir -p ~/.local/share/kokoro
curl -L "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx" \
  -o ~/.local/share/kokoro/kokoro-v1.0.onnx
curl -L "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin" \
  -o ~/.local/share/kokoro/voices-v1.0.bin
```

#### 4. Place `podcast_gen.py`

The script must be in one of these locations:
- Same directory as the `mdviewer` binary
- `/usr/local/share/mdviewer/podcast_gen.py`
- `~/src/markdown-go/podcast_gen.py`

#### 5. Configure an LLM provider

Set **one** of these (auto-detected in order):

| Provider | Environment Variables |
|----------|----------------------|
| Any OpenAI-compatible API | `PODCAST_API_URL` + `PODCAST_API_TOKEN` |
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |

Example with OpenAI:
```bash
export OPENAI_API_KEY="sk-..."
mdviewer -root ~/docs -port 8080
```

Example with a local LLM (Ollama, LM Studio, etc.):
```bash
export PODCAST_API_URL="http://localhost:11434/v1/chat/completions"
mdviewer -root ~/docs -port 8080
```

### Standalone CLI Usage

You can also use `podcast_gen.py` directly:

```bash
# Generate podcast with OpenAI
OPENAI_API_KEY="sk-..." python3 podcast_gen.py document.md

# Use a specific API endpoint
python3 podcast_gen.py document.md --api-url http://localhost:11434/v1/chat/completions

# Generate script only (no TTS)
python3 podcast_gen.py document.md --script-only

# Re-synthesize from existing script
python3 podcast_gen.py document.md --from-script document.podcast-script.txt

# Specify model
python3 podcast_gen.py document.md --model claude-opus-4.6
```

### Performance

- **Script generation**: 10-40s depending on LLM provider and document length
- **TTS synthesis**: ~1.6x realtime on CPU (a 3-min podcast takes ~5 min to generate)
- **Output**: MP3 at 128kbps, typically 2-6 min for a standard document
- No GPU required — runs entirely on CPU

### Custom Model Paths

Override default model locations with environment variables:

```bash
export KOKORO_MODEL="/path/to/kokoro-v1.0.onnx"
export KOKORO_VOICES="/path/to/voices-v1.0.bin"
```
