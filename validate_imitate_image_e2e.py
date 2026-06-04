#!/usr/bin/env python3
"""End-to-end validation for /imitate image upload + image chat."""

from __future__ import annotations

import argparse
import json
import mimetypes
import os
import sys
import time
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Tuple

try:
    import requests
except Exception as exc:  # pragma: no cover
    raise SystemExit(
        "This script requires the 'requests' package. Install it with: pip install requests\n"
        f"Import error: {exc}"
    )


MISSING_IMAGE_REPLY = "无图片"
DEFAULT_PROMPT = "请提取这张图片中的文字。"
DEFAULT_BASE_URL = "http://127.0.0.1:8080/imitate/v1"


class APIError(RuntimeError):
    pass


def parse_args() -> argparse.Namespace:
    repo_root = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Validate /imitate image upload + OCR chat flow")
    parser.add_argument(
        "--base-url",
        default=os.getenv("IMITATE_BASE_URL", DEFAULT_BASE_URL),
        help=f"Base URL for imitate v1, default: {DEFAULT_BASE_URL}",
    )
    parser.add_argument(
        "--token",
        default=os.getenv("IMITATE_API_KEY") or os.getenv("IMITATE_ACCESS_TOKEN"),
        help="Bearer token accepted by /imitate/v1: use IMITATE_API_KEY or a direct ChatGPT access token, not sk-*",
    )
    parser.add_argument(
        "--image",
        default=str(repo_root / "医疗.jpg"),
        help="Image file to upload, default: ./医疗.jpg",
    )
    parser.add_argument(
        "--prompt",
        default=DEFAULT_PROMPT,
        help=f"Prompt sent with the uploaded image, default: {DEFAULT_PROMPT}",
    )
    parser.add_argument(
        "--model",
        default=os.getenv("IMITATE_MODEL", "gpt-5-3"),
        help="Model name, default: gpt-5-3",
    )
    parser.add_argument(
        "--purpose",
        default="vision",
        help="Upload purpose, default: vision",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=180.0,
        help="Request timeout in seconds, default: 180",
    )
    parser.add_argument(
        "--skip-file-check",
        action="store_true",
        help="Skip GET /files/{id} verification after upload",
    )
    parser.add_argument(
        "--file-check-retries",
        type=int,
        default=6,
        help="Number of GET /files/{id} retries on 404, default: 6",
    )
    parser.add_argument(
        "--file-check-interval",
        type=float,
        default=2.0,
        help="Seconds between GET /files/{id} retries, default: 2",
    )
    parser.add_argument(
        "--also-stream",
        action="store_true",
        help="Run an additional streaming chat validation after the non-stream request",
    )
    parser.add_argument(
        "--stream-only",
        action="store_true",
        help="Only run streaming chat validation and skip the non-stream request",
    )
    parser.add_argument(
        "--chat-file-id",
        help="Override the file_id used in /chat/completions. Defaults to the uploaded file id.",
    )
    parser.add_argument(
        "--expected-substring",
        action="append",
        default=[],
        help="Expected OCR substring. Repeat this option for multiple substrings.",
    )
    parser.add_argument(
        "--expect-no-image",
        action="store_true",
        help=f"Assert that the assistant reply is exactly {MISSING_IMAGE_REPLY!r}",
    )
    parser.add_argument(
        "--response-json",
        help="Optional file path to store the non-stream JSON response",
    )
    args = parser.parse_args()
    if args.expect_no_image and args.expected_substring:
        raise SystemExit("--expect-no-image cannot be used together with --expected-substring")
    return args


def build_headers(token: str, accept: str = "application/json") -> Dict[str, str]:
    if not token:
        raise SystemExit("Missing token. Pass --token or set IMITATE_API_KEY / IMITATE_ACCESS_TOKEN.")
    return {
        "Authorization": f"Bearer {token}",
        "Accept": accept,
    }


def ensure_ok(resp: requests.Response, stage: str) -> None:
    if resp.ok:
        return
    text = resp.text.strip()
    try:
        text = json.dumps(resp.json(), ensure_ascii=False, indent=2)
    except Exception:
        pass
    raise APIError(f"{stage} failed: HTTP {resp.status_code}\n{text}")


def guess_mime(path: Path) -> str:
    mime, _ = mimetypes.guess_type(path.name)
    return mime or "application/octet-stream"


def upload_file(base_url: str, token: str, image_path: Path, purpose: str, timeout: float) -> Dict[str, Any]:
    headers = {"Authorization": f"Bearer {token}"}
    mime_type = guess_mime(image_path)
    with image_path.open("rb") as fh:
        resp = requests.post(
            f"{base_url.rstrip('/')}/files",
            headers=headers,
            files={"file": (image_path.name, fh, mime_type)},
            data={"purpose": purpose},
            timeout=timeout,
        )
    ensure_ok(resp, "file upload")
    payload = resp.json()
    if "id" not in payload:
        raise APIError(f"file upload returned no id:\n{json.dumps(payload, ensure_ascii=False, indent=2)}")
    return payload


def retrieve_file(base_url: str, token: str, file_id: str, timeout: float) -> Dict[str, Any]:
    resp = requests.get(
        f"{base_url.rstrip('/')}/files/{file_id}",
        headers=build_headers(token),
        timeout=timeout,
    )
    ensure_ok(resp, "retrieve file")
    return resp.json()


def retrieve_file_with_retry(
    base_url: str,
    token: str,
    file_id: str,
    timeout: float,
    retries: int,
    interval: float,
) -> Dict[str, Any]:
    last_error: Optional[APIError] = None
    attempts = max(1, retries)
    for attempt in range(1, attempts + 1):
        try:
            return retrieve_file(base_url, token, file_id, timeout)
        except APIError as exc:
            last_error = exc
            if "HTTP 404" not in str(exc) or attempt == attempts:
                raise
            print(f"GET /files/{{id}} not ready yet, retry {attempt}/{attempts} ...")
            time.sleep(interval)
    raise last_error or APIError("retrieve file failed")


def build_chat_payload(model: str, prompt: str, file_id: str, stream: bool) -> Dict[str, Any]:
    return {
        "model": model,
        "stream": stream,
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "input_text", "text": prompt},
                    {"type": "input_image", "file_id": file_id},
                ],
            }
        ],
    }


def nonstream_completion(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    resp = requests.post(
        f"{base_url.rstrip('/')}/chat/completions",
        headers=build_headers(token),
        json=payload,
        timeout=timeout,
    )
    ensure_ok(resp, "chat completion")
    return resp.json()


def stream_completion(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> Tuple[str, List[str]]:
    resp = requests.post(
        f"{base_url.rstrip('/')}/chat/completions",
        headers=build_headers(token, accept="text/event-stream"),
        json=payload,
        timeout=timeout,
        stream=True,
    )
    ensure_ok(resp, "chat completion(stream)")
    resp.encoding = "utf-8"

    chunks: List[str] = []
    raw_events: List[str] = []
    for raw_line in resp.iter_lines(decode_unicode=True):
        if not raw_line:
            continue
        line = raw_line.strip()
        if not line.startswith("data: "):
            continue
        data = line[6:]
        raw_events.append(data)
        if data == "[DONE]":
            break
        try:
            chunk = json.loads(data)
        except Exception:
            continue
        delta = ((chunk.get("choices") or [{}])[0].get("delta") or {})
        text = delta.get("content")
        if text:
            chunks.append(text)
    return "".join(chunks), raw_events


def extract_text(response_json: Dict[str, Any]) -> str:
    choices = response_json.get("choices") or []
    if not choices:
        return ""
    message = choices[0].get("message") or {}
    content = message.get("content")
    if isinstance(content, str):
        return content
    return ""


def assert_text(text: str, expected_substrings: Iterable[str], expect_no_image: bool, label: str) -> None:
    text = text.strip()
    if expect_no_image:
        if text != MISSING_IMAGE_REPLY:
            raise APIError(f"{label} expected exact reply {MISSING_IMAGE_REPLY!r}, got:\n{text}")
        return

    expected = [item for item in expected_substrings if item]
    if expected:
        if text == MISSING_IMAGE_REPLY:
            raise APIError(f"{label} returned {MISSING_IMAGE_REPLY!r}, image context was not attached")
        missing = [item for item in expected if item not in text]
        if missing:
            raise APIError(f"{label} missing expected substrings: {missing}\nActual reply:\n{text}")
        return

    if not text:
        raise APIError(f"{label} returned empty assistant content")


def print_json(title: str, payload: Dict[str, Any]) -> None:
    print(title)
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def main() -> int:
    args = parse_args()
    image_path = Path(args.image)
    if not image_path.is_file():
        raise SystemExit(f"Image file not found: {image_path}")

    print(f"[1/4] Uploading image: {image_path}")
    upload_resp = upload_file(args.base_url, args.token, image_path, args.purpose, args.timeout)
    print_json("Upload response:", upload_resp)
    file_id = upload_resp["id"]

    if args.skip_file_check:
        print("\n[2/4] Skip GET /files/{id}")
    else:
        print("\n[2/4] Verifying uploaded file metadata")
        retrieve_resp = retrieve_file_with_retry(
            args.base_url,
            args.token,
            file_id,
            args.timeout,
            args.file_check_retries,
            args.file_check_interval,
        )
        print_json("Retrieve response:", retrieve_resp)

    chat_file_id = args.chat_file_id or file_id
    if chat_file_id != file_id:
        print(f"Using overridden chat file_id: {chat_file_id}")

    if args.stream_only:
        print("\n[3/4] Skip non-stream validation")
    else:
        print("\n[3/4] Sending non-stream image chat request")
        payload = build_chat_payload(args.model, args.prompt, chat_file_id, stream=False)
        response_json = nonstream_completion(args.base_url, args.token, payload, args.timeout)
        assistant_text = extract_text(response_json)
        print_json("Chat response:", response_json)
        print("\nAssistant text:")
        print(assistant_text or "<empty>")
        assert_text(assistant_text, args.expected_substring, args.expect_no_image, "non-stream response")

        if args.response_json:
            Path(args.response_json).write_text(json.dumps(response_json, ensure_ascii=False, indent=2), encoding="utf-8")
            print(f"\nSaved non-stream response JSON to: {args.response_json}")

    if args.also_stream or args.stream_only:
        label = "[4/4]" if not args.stream_only else "[4/4]"
        print(f"\n{label} Sending streaming image chat request")
        stream_payload = build_chat_payload(args.model, args.prompt, chat_file_id, stream=True)
        stream_text, raw_events = stream_completion(args.base_url, args.token, stream_payload, args.timeout)
        print("Streaming assistant text:")
        print(stream_text or "<empty>")
        assert_text(stream_text, args.expected_substring, args.expect_no_image, "stream response")
        print(f"Received {len(raw_events)} SSE events")
    else:
        print("\n[4/4] Skip streaming validation")

    print("\nValidation passed.")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except APIError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
