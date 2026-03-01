#!/bin/bash
# Markdown Viewer command helpers
# Usage: source .vscode/commands.sh

if [ -n "${ZSH_VERSION:-}" ]; then
  SCRIPT_SOURCE="${(%):-%x}"
else
  SCRIPT_SOURCE="${BASH_SOURCE[0]}"
fi
PROJECT_DIR="$(cd "$(dirname "$SCRIPT_SOURCE")/.." && pwd)"
BIN_DIR="$PROJECT_DIR/bin"
APP_BIN="$BIN_DIR/mdviewer"
DEFAULT_TARGET="/workspace/edge/src/.vscode/areaSelectionPR/iter2"
DEFAULT_PORT="${MDVIEWER_PORT:-8080}"

MDVIEWER_COMMANDS=(
  "Build:Build mdviewer binary"
  "Compile:Compile mdviewer binary (alias of Build)"
  "Install:Build and install mdviewer binary"
  "Run:Build and run the app (optional args: <root> <port>)"
  "Run.Target:Run with explicit target folder and optional port"
  "Run.Dev:Run with go run (optional args: <root> <port>)"
  "Fmt:Format Go sources"
  "Test:Run Go tests"
  "Clean:Remove built binaries"
  "ReleaseNewVersion:Increment version tag and trigger a GitHub release"
  "Help:Print available commands"
  "ff:Interactive command picker (fzf)"
)

Help() {
  echo "Available commands:"
  for item in "${MDVIEWER_COMMANDS[@]}"; do
    echo "  - ${item%%:*}: ${item#*:}"
  done
}

Build() {
  mkdir -p "$BIN_DIR"
  (cd "$PROJECT_DIR" && go build -o "$APP_BIN" .)
}

Compile() {
  Build "$@"
}

FirstWritablePathDir() {
  local dir
  local old_ifs="$IFS"
  IFS=':'
  for dir in $PATH; do
    [ -z "$dir" ] && continue
    if [ -d "$dir" ] && [ -w "$dir" ]; then
      echo "$dir"
      IFS="$old_ifs"
      return 0
    fi
  done
  IFS="$old_ifs"
  return 1
}

DefaultInstallDir() {
  case "$(uname -s)" in
    Linux|Darwin)
      echo "$HOME/.local/bin"
      ;;
    MINGW*|MSYS*|CYGWIN*)
      echo "$HOME/bin"
      ;;
    *)
      echo "$HOME/bin"
      ;;
  esac
}

Install() {
  local install_dir
  local destination

  Build || return 1

  install_dir="$(FirstWritablePathDir || true)"
  if [ -z "$install_dir" ]; then
    install_dir="${MDVIEWER_INSTALL_DIR:-$(DefaultInstallDir)}"
  fi

  mkdir -p "$install_dir" || return 1
  destination="$install_dir/mdviewer"
  cp "$APP_BIN" "$destination" || return 1
  chmod +x "$destination" || return 1

  echo "Installed mdviewer to $destination"
  case ":$PATH:" in
    *":$install_dir:"*) ;;
    *) echo "Add to PATH: export PATH=\"$install_dir:\$PATH\"" ;;
  esac
}

Run() {
  local target="${1:-$DEFAULT_TARGET}"
  local port="${2:-$DEFAULT_PORT}"
  Build || return 1
  echo "Running mdviewer on http://localhost:${port} (root: ${target})"
  "$APP_BIN" -root "$target" -port "$port"
}

Run.Target() {
  local target="${1:-$DEFAULT_TARGET}"
  local port="${2:-$DEFAULT_PORT}"
  Run "$target" "$port"
}

Run.Dev() {
  local target="${1:-$DEFAULT_TARGET}"
  local port="${2:-$DEFAULT_PORT}"
  (cd "$PROJECT_DIR" && go run . -root "$target" -port "$port")
}

Fmt() {
  (cd "$PROJECT_DIR" && find . -name "*.go" -print0 | xargs -0 gofmt -w)
}

Test() {
  (cd "$PROJECT_DIR" && go test ./...)
}

Clean() {
  rm -rf "$BIN_DIR"
  echo "Removed $BIN_DIR"
}

ReleaseNewVersion() {
  cd "$PROJECT_DIR" || return 1

  # Ensure working tree is clean
  if ! git diff --quiet || ! git diff --cached --quiet; then
    echo "Error: working tree has uncommitted changes. Commit or stash first."
    return 1
  fi

  # Get the latest version tag (default v0.0.0 if none)
  local latest
  latest=$(git tag --sort=-v:refname --list 'v*' | head -n1)
  if [ -z "$latest" ]; then
    latest="v0.0.0"
  fi

  # Parse major.minor.patch
  local version="${latest#v}"
  local major minor patch
  IFS='.' read -r major minor patch <<< "$version"
  major="${major:-0}"; minor="${minor:-0}"; patch="${patch:-0}"

  echo "Current version: v${major}.${minor}.${patch}"
  echo ""
  echo "Bump type:"
  echo "  1) patch  (v${major}.${minor}.$((patch+1)))"
  echo "  2) minor  (v${major}.$((minor+1)).0)"
  echo "  3) major  (v$((major+1)).0.0)"
  printf "Select [1/2/3] (default: 1): "
  read -r choice

  case "${choice:-1}" in
    1) patch=$((patch+1)) ;;
    2) minor=$((minor+1)); patch=0 ;;
    3) major=$((major+1)); minor=0; patch=0 ;;
    *) echo "Invalid choice"; return 1 ;;
  esac

  local new_tag="v${major}.${minor}.${patch}"
  echo ""
  echo "New version: $new_tag"
  printf "Proceed? [y/N]: "
  read -r confirm
  if [ "${confirm,,}" != "y" ]; then
    echo "Aborted."
    return 0
  fi

  git tag -a "$new_tag" -m "Release $new_tag" || return 1
  git push origin "$new_tag" || return 1
  echo ""
  echo "Pushed tag $new_tag — GitHub Actions will create the release."
}

ff() {
  if ! command -v fzf >/dev/null 2>&1; then
    echo "fzf is not installed"
    return 1
  fi
  local selected
  selected=$(printf '%s\n' "${MDVIEWER_COMMANDS[@]}" | fzf --prompt="mdviewer command > ") || return 0
  local cmd="${selected%%:*}"
  eval "$cmd"
}
