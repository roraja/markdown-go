#!/usr/bin/env python3
"""
podcast_gen.py — Markdown-to-Podcast generator for mdviewer.

Takes a markdown file, generates a two-person conversational dialogue via LLM,
then synthesizes speech using Kokoro TTS (ONNX, CPU).

Usage:
    python3 podcast_gen.py <markdown_file> [--output <output.mp3>]

LLM Provider (auto-detected in order):
    1. --api-url + --api-token (any OpenAI-compatible API)
    2. PODCAST_API_URL + PODCAST_API_TOKEN env vars
    3. OpenAI API: OPENAI_API_KEY env var
    4. Anthropic API: ANTHROPIC_API_KEY env var
    5. GitHub Copilot CLI: `gh copilot -p` (requires `gh auth login`)

Architecture:
    1. Read markdown → extract text
    2. LLM generates natural conversation: Host (Sarah) + Guest (Michael)
    3. Kokoro TTS generates audio per line with different voices
    4. pydub stitches segments with natural pauses
    5. Outputs MP3

Voices: af_heart (Sarah, female host), am_michael (Michael, male guest)
"""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path

# Heavy audio deps are imported lazily in synthesize_audio() so that
# --script-only works without numpy/pydub/soundfile/kokoro installed.

KOKORO_MODEL = os.environ.get("KOKORO_MODEL", os.path.expanduser("~/.local/share/kokoro/kokoro-v1.0.onnx"))
KOKORO_VOICES = os.environ.get("KOKORO_VOICES", os.path.expanduser("~/.local/share/kokoro/voices-v1.0.bin"))

HOST_VOICE = "af_heart"      # Sarah — warm female host
GUEST_VOICE = "am_michael"   # Michael — curious male guest
SPEED = 1.05                 # Slightly faster for natural podcast feel

# Pause durations in ms
PAUSE_BETWEEN_SPEAKERS = 400
PAUSE_WITHIN_SPEAKER = 150
PAUSE_SECTION_BREAK = 800

SYSTEM_PROMPT = """You are a podcast script writer. Your job is to transform a technical document into an engaging, natural conversation between two people.

Speakers:
- [Sarah]: The host. She has read the document and explains it to Michael. She's warm, clear, and uses analogies to make technical concepts accessible.
- [Michael]: The co-host. He's smart but hearing about this topic for the first time. He asks genuine questions, pushes back when something seems off, and makes connections to real-world scenarios.

CRITICAL RULES:
1. This is a CONVERSATION, not a reading. Never say "the document says" or "according to the text." Sarah explains things as if she learned them herself.
2. Michael should ask questions a real person would ask — "Wait, why does that matter?" or "So what happens if..."
3. Cover ALL key points but make them flow naturally. Don't follow the document's structure rigidly — reorganize for conversational flow if needed.
4. For diagrams/tables/code: describe them verbally. "Imagine a pipeline where..." or "Think of it like..." — use analogies.
5. Keep exchanges SHORT — 1-3 sentences each. Real conversations have quick back-and-forth.
6. Include natural reactions: surprise, agreement, mild disagreement, humor where appropriate.
7. Start with Sarah briefly introducing the topic (NOT "welcome to our podcast" — just dive in naturally).
8. End with a natural wrap-up, not a formal sign-off.
9. Don't be afraid of tangents if they help explain a concept.
10. Output ONLY dialogue lines, no stage directions or notes.

Format:
[Sarah] Text here.
[Michael] Text here.

BAD (reading the doc):
[Sarah] The document describes a system that uses modifier keys during drag and drop.
[Michael] What does the document say about effectAllowed?

GOOD (natural conversation):
[Sarah] So you know how when you drag a file in Finder, holding Option makes it copy instead of move?
[Michael] Yeah, I use that all the time actually.
[Sarah] Well, browsers just... don't do that. This proposal fixes it."""


def read_markdown(filepath: str) -> str:
    """Read markdown file and return contents."""
    with open(filepath, "r") as f:
        return f.read()


def detect_provider(args) -> dict:
    """Auto-detect LLM provider. Returns dict with 'type' and connection details."""
    # Model can come from CLI flag, env var, or provider-specific default
    model_override = args.model or os.environ.get("PODCAST_MODEL")

    # 1. Explicit API URL
    api_url = args.api_url or os.environ.get("PODCAST_API_URL")
    api_token = args.api_token or os.environ.get("PODCAST_API_TOKEN") or os.environ.get("OPENCLAW_TOKEN")
    if api_url:
        return {"type": "openai_compat", "url": api_url, "token": api_token, "model": model_override or "default"}

    # 2. OpenAI
    if os.environ.get("OPENAI_API_KEY"):
        return {"type": "openai_compat", "url": "https://api.openai.com/v1/chat/completions",
                "token": os.environ["OPENAI_API_KEY"], "model": model_override or "gpt-4o"}

    # 3. Anthropic
    if os.environ.get("ANTHROPIC_API_KEY"):
        return {"type": "anthropic", "token": os.environ["ANTHROPIC_API_KEY"],
                "model": model_override or "claude-sonnet-4-20250514"}

    # 4. GitHub Copilot CLI (gh copilot -p)
    try:
        result = subprocess.run(["gh", "auth", "status"], capture_output=True, timeout=5)
        if result.returncode == 0:
            return {"type": "gh_copilot", "model": model_override or "gpt-4o"}
    except (FileNotFoundError, subprocess.TimeoutExpired):
        pass

    return {"type": "none"}


def call_llm(provider: dict, system_prompt: str, user_msg: str) -> str:
    """Call LLM via detected provider."""
    import urllib.request

    if provider["type"] == "openai_compat":
        payload = {
            "model": provider["model"],
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": user_msg}
            ],
            "temperature": 0.7,
            "max_tokens": 4096
        }
        headers = {"Content-Type": "application/json"}
        if provider.get("token"):
            headers["Authorization"] = f"Bearer {provider['token']}"

        print(f"[llm] POST {provider['url']} model={provider['model']}", flush=True)
        req = urllib.request.Request(
            provider["url"], data=json.dumps(payload).encode(),
            headers=headers, method="POST"
        )
        timeout = int(os.environ.get("PODCAST_LLM_TIMEOUT", "600"))
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                result = json.loads(resp.read())
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", errors="replace")
            print(f"[llm] HTTP {e.code}: {body}", flush=True)
            raise
        return result["choices"][0]["message"]["content"]

    elif provider["type"] == "anthropic":
        payload = {
            "model": provider["model"],
            "max_tokens": 4096,
            "system": system_prompt,
            "messages": [{"role": "user", "content": user_msg}]
        }
        headers = {
            "Content-Type": "application/json",
            "x-api-key": provider["token"],
            "anthropic-version": "2023-06-01"
        }
        req = urllib.request.Request(
            "https://api.anthropic.com/v1/messages",
            data=json.dumps(payload).encode(), headers=headers, method="POST"
        )
        timeout = int(os.environ.get("PODCAST_LLM_TIMEOUT", "600"))
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                result = json.loads(resp.read())
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", errors="replace")
            print(f"[llm] HTTP {e.code}: {body}", flush=True)
            raise
        return result["content"][0]["text"]

    elif provider["type"] == "gh_copilot":
        # Use gh copilot CLI with -p flag for non-interactive prompting.
        # Combines system prompt and user message into a single prompt.
        full_prompt = f"{system_prompt}\n\n---\n\n{user_msg}"
        timeout = int(os.environ.get("PODCAST_LLM_TIMEOUT", "600"))
        print(f"[llm] gh copilot -p (timeout={timeout}s)", flush=True)
        result = subprocess.run(
            ["gh", "copilot", "-p", full_prompt],
            capture_output=True, text=True, timeout=timeout
        )
        if result.returncode != 0:
            raise RuntimeError(f"gh copilot failed (exit {result.returncode}): {result.stderr}")
        # Strip the usage stats footer that gh copilot appends
        output = result.stdout
        footer_idx = output.find("\nTotal usage est:")
        if footer_idx != -1:
            output = output[:footer_idx]
        return output.strip()

    else:
        raise RuntimeError(
            "No LLM provider found. Set one of:\n"
            "  - PODCAST_API_URL + PODCAST_API_TOKEN (any OpenAI-compatible API)\n"
            "  - OPENAI_API_KEY\n"
            "  - ANTHROPIC_API_KEY\n"
            "  - GitHub CLI: `gh auth login` (uses gh copilot -p)"
        )


def generate_script(markdown_content: str, provider: dict, filename: str) -> list[tuple[str, str]]:
    """Use LLM to generate dialogue script from markdown content."""
    user_msg = f"Convert this document into a podcast dialogue:\n\nFilename: {filename}\n\n---\n\n{markdown_content}"

    print(f"[script] Generating dialogue via {provider['type']}...", flush=True)
    t0 = time.time()

    script_text = call_llm(provider, SYSTEM_PROMPT, user_msg)

    print(f"[script] Generated in {time.time()-t0:.1f}s ({len(script_text)} chars)", flush=True)
    return parse_script(script_text)


def parse_script(script_text: str) -> list[tuple[str, str]]:
    """Parse [Speaker] Text lines into list of (speaker, text) tuples."""
    lines = []
    pattern = re.compile(r'^\[(\w+)\]\s*(.+)', re.MULTILINE)

    for match in pattern.finditer(script_text):
        speaker = match.group(1).strip()
        text = match.group(2).strip()
        if text:
            lines.append((speaker, text))

    if not lines:
        # Fallback: try without brackets
        for line in script_text.strip().split('\n'):
            line = line.strip()
            if line.startswith('Sarah:'):
                lines.append(('Sarah', line[6:].strip()))
            elif line.startswith('Michael:'):
                lines.append(('Michael', line[8:].strip()))

    return lines


def synthesize_audio(lines: list[tuple[str, str]], progress_callback=None):
    """Generate audio from dialogue lines using Kokoro TTS."""
    import numpy as np
    import soundfile as sf
    from pydub import AudioSegment
    from kokoro_onnx import Kokoro

    print(f"[tts] Loading Kokoro model...", flush=True)
    kokoro = Kokoro(KOKORO_MODEL, KOKORO_VOICES)

    combined = AudioSegment.silent(duration=500)  # Start with brief silence
    total = len(lines)
    prev_speaker = None

    for i, (speaker, text) in enumerate(lines):
        voice = HOST_VOICE if speaker == "Sarah" else GUEST_VOICE

        # Split long text into sentences for more natural delivery
        sentences = split_sentences(text)

        for j, sentence in enumerate(sentences):
            if not sentence.strip():
                continue

            try:
                samples, sr = kokoro.create(sentence, voice=voice, speed=SPEED)
                # Convert to AudioSegment
                with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as tmp:
                    sf.write(tmp.name, samples, sr)
                    segment = AudioSegment.from_wav(tmp.name)
                    os.unlink(tmp.name)

                combined += segment

                # Add pause within speaker between sentences
                if j < len(sentences) - 1:
                    combined += AudioSegment.silent(duration=PAUSE_WITHIN_SPEAKER)

            except Exception as e:
                print(f"[tts] Warning: failed to synthesize '{sentence[:50]}...': {e}", flush=True)
                continue

        # Add pause between speakers
        if i < total - 1:
            next_speaker = lines[i + 1][0]
            if next_speaker != speaker:
                combined += AudioSegment.silent(duration=PAUSE_BETWEEN_SPEAKERS)
            else:
                combined += AudioSegment.silent(duration=PAUSE_WITHIN_SPEAKER)

        pct = int((i + 1) / total * 100)
        print(f"[tts] {pct}% — {i+1}/{total} lines ({speaker}: {text[:60]}...)", flush=True)
        if progress_callback:
            progress_callback(pct, i + 1, total)

    return combined


def split_sentences(text: str) -> list[str]:
    """Split text into sentences for more natural TTS delivery."""
    parts = re.split(r'(?<=[.!?])\s+', text)
    result = []
    for part in parts:
        if len(part) > 300:
            sub = re.split(r'(?<=[,;])\s+', part)
            result.extend(sub)
        else:
            result.append(part)
    return [p.strip() for p in result if p.strip()]


def main():
    parser = argparse.ArgumentParser(description="Generate podcast from markdown")
    parser.add_argument("input", help="Input markdown file")
    parser.add_argument("--output", "-o", help="Output audio file (default: <input>.podcast.mp3)")
    parser.add_argument("--api-url", default=None, help="LLM API URL (OpenAI-compatible)")
    parser.add_argument("--api-token", default=None, help="API auth token")
    parser.add_argument("--model", default=None, help="Model name (default: auto per provider)")
    parser.add_argument("--script-only", action="store_true", help="Only generate script, don't synthesize")
    parser.add_argument("--from-script", help="Skip LLM, use existing script file")
    args = parser.parse_args()

    input_path = Path(args.input)
    if not input_path.exists():
        print(f"Error: {input_path} not found", file=sys.stderr)
        sys.exit(1)

    output_path = args.output or str(input_path.with_suffix('.podcast.mp3'))
    script_path = str(input_path.with_suffix('.podcast-script.txt'))

    # Step 1: Generate or load script
    if args.from_script:
        print(f"[script] Loading script from {args.from_script}", flush=True)
        with open(args.from_script) as f:
            lines = parse_script(f.read())
    else:
        provider = detect_provider(args)
        if provider["type"] == "none":
            print("Error: No LLM provider found. Set PODCAST_API_URL, OPENAI_API_KEY, or ANTHROPIC_API_KEY.", file=sys.stderr)
            sys.exit(1)
        markdown = read_markdown(str(input_path))
        lines = generate_script(markdown, provider, input_path.name)

        # Save script for debugging/reuse
        with open(script_path, 'w') as f:
            for speaker, text in lines:
                f.write(f"[{speaker}] {text}\n")
        print(f"[script] Saved script to {script_path}", flush=True)

    print(f"[script] {len(lines)} dialogue lines", flush=True)

    if args.script_only:
        print(f"[done] Script saved to {script_path}")
        return

    # Step 2: Synthesize audio
    t0 = time.time()
    audio = synthesize_audio(lines)
    duration_s = len(audio) / 1000
    gen_time = time.time() - t0

    # Step 3: Export
    print(f"[export] Saving {duration_s:.0f}s audio to {output_path}...", flush=True)
    audio.export(output_path, format="mp3", bitrate="128k")

    file_size_mb = os.path.getsize(output_path) / (1024 * 1024)
    print(f"[done] {duration_s:.0f}s podcast, {file_size_mb:.1f}MB, generated in {gen_time:.0f}s")
    print(f"[done] Output: {output_path}")


if __name__ == "__main__":
    main()
