#!/usr/bin/env python3
"""
podcast_gen.py — Markdown-to-Podcast generator for mdviewer.

Takes a markdown file, generates a two-person dialogue script via LLM,
then synthesizes speech using Kokoro TTS (ONNX, CPU).

Usage:
    python3 podcast_gen.py <markdown_file> [--output <output.mp3>] [--api-url <url>] [--api-token <token>]

Architecture:
    1. Read markdown → extract text, describe mermaid/tables verbally
    2. LLM generates dialogue script: Host (Sarah) + Guest (Michael)
    3. Kokoro TTS generates audio per line with different voices
    4. pydub stitches segments with natural pauses
    5. Outputs MP3

Voices: af_heart (Sarah, female host), am_michael (Michael, male guest)
"""

import argparse
import json
import os
import re
import sys
import tempfile
import time
from pathlib import Path

import numpy as np
import soundfile as sf
from pydub import AudioSegment

KOKORO_MODEL = os.environ.get("KOKORO_MODEL", os.path.expanduser("~/.local/share/kokoro/kokoro-v1.0.onnx"))
KOKORO_VOICES = os.environ.get("KOKORO_VOICES", os.path.expanduser("~/.local/share/kokoro/voices-v1.0.bin"))

HOST_VOICE = "af_heart"      # Sarah — warm female host
GUEST_VOICE = "am_michael"   # Michael — curious male guest
SPEED = 1.05                 # Slightly faster for natural podcast feel

# Pause durations in ms
PAUSE_BETWEEN_SPEAKERS = 400
PAUSE_WITHIN_SPEAKER = 150
PAUSE_SECTION_BREAK = 800

DEFAULT_API_URL = "http://localhost:18789/v1/chat/completions"

SYSTEM_PROMPT = """You are a podcast script writer. Convert the given document into a natural two-person dialogue.

Speakers:
- [Sarah]: The host. Explains concepts clearly, guides the conversation. Warm, knowledgeable tone.
- [Michael]: The guest/co-host. Asks clarifying questions, reacts naturally, adds insights. Curious, engaged tone.

Rules:
1. Start with a brief intro: Sarah welcomes listeners, introduces the topic.
2. Cover ALL key points from the document. Don't skip sections.
3. For mermaid diagrams: describe the flow/architecture verbally. Say "imagine a diagram where..." or "the flow goes like this..."
4. For tables: summarize the data conversationally. Don't read cell by cell — highlight patterns and key rows.
5. For code blocks: explain what the code does in plain English. Only quote tiny snippets if critical.
6. For issue/bug tables: discuss the most important ones, mention priority and why they matter.
7. Keep it conversational — use filler words sparingly ("right", "exactly", "interesting").
8. Each line should be 1-3 sentences max. Short, punchy exchanges.
9. End with a brief wrap-up/summary.
10. Output ONLY the dialogue, no stage directions or notes.

Format each line as:
[Sarah] Text here.
[Michael] Text here.

Do NOT include any other formatting, headers, or markdown."""


def read_markdown(filepath: str) -> str:
    """Read markdown file and return contents."""
    with open(filepath, "r") as f:
        return f.read()


def generate_script(markdown_content: str, api_url: str, api_token: str | None, filename: str) -> list[tuple[str, str]]:
    """Use LLM to generate dialogue script from markdown content."""
    import urllib.request

    user_msg = f"Convert this document into a podcast dialogue:\n\nFilename: {filename}\n\n---\n\n{markdown_content}"

    payload = {
        "model": "default",
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": user_msg}
        ],
        "temperature": 0.7,
        "max_tokens": 4096
    }

    headers = {"Content-Type": "application/json"}
    if api_token:
        headers["Authorization"] = f"Bearer {api_token}"

    req = urllib.request.Request(
        api_url,
        data=json.dumps(payload).encode(),
        headers=headers,
        method="POST"
    )

    print(f"[script] Generating dialogue via LLM...", flush=True)
    t0 = time.time()

    with urllib.request.urlopen(req, timeout=120) as resp:
        result = json.loads(resp.read())

    script_text = result["choices"][0]["message"]["content"]
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


def synthesize_audio(lines: list[tuple[str, str]], progress_callback=None) -> AudioSegment:
    """Generate audio from dialogue lines using Kokoro TTS."""
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
    # Split on sentence boundaries but keep short
    parts = re.split(r'(?<=[.!?])\s+', text)
    result = []
    for part in parts:
        if len(part) > 300:
            # Further split on commas/semicolons for very long sentences
            sub = re.split(r'(?<=[,;])\s+', part)
            result.extend(sub)
        else:
            result.append(part)
    return [p.strip() for p in result if p.strip()]


def main():
    parser = argparse.ArgumentParser(description="Generate podcast from markdown")
    parser.add_argument("input", help="Input markdown file")
    parser.add_argument("--output", "-o", help="Output audio file (default: <input>.podcast.mp3)")
    parser.add_argument("--api-url", default=DEFAULT_API_URL, help="LLM API URL")
    parser.add_argument("--api-token", default=os.environ.get("OPENCLAW_TOKEN"), help="API auth token")
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
        markdown = read_markdown(str(input_path))
        lines = generate_script(markdown, args.api_url, args.api_token, input_path.name)

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
