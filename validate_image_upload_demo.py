#!/usr/bin/env python3
"""Validate image upload + chat flow against /imitate/v1.

Usage:
  python validate_image_upload_demo.py \
    --token "$IMITATE_API_KEY" \
    --image /path/to/demo.png \
    --prompt "请详细描述这张图"

The script performs two steps:
1) POST /files to upload the image and get file_id
2) POST /chat/completions with input_text + input_image(file_id)
"""

from __future__ import annotations

import argparse
import json
import mimetypes
import os
import sys
from pathlib import Path
from typing import Any, Dict, Iterable, Optional

try:
    import requests
except Exception as exc:  # pragma: no cover
    raise SystemExit(
        "This demo requires the 'requests' package. Install it with: pip install requests\n"
        f"Import error: {exc}"
    )


class APIError(RuntimeError):
    pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Validate image upload + multimodal chat against /imitate/v1")
    parser.add_argument("--base-url", default=os.getenv("IMITATE_BASE_URL", "http://127.0.0.1:8081/imitate/v1"),
                        help="Base URL for imitate v1, default: http://127.0.0.1:8081/imitate/v1")
    parser.add_argument("--token", default=os.getenv("IMITATE_API_KEY") or os.getenv("IMITATE_ACCESS_TOKEN"),
                        help="Bearer token accepted by /imitate/v1: IMITATE_API_KEY or direct ChatGPT access token, not sk-*")
    parser.add_argument("--image", required=True, help="Path to image file to upload")
    parser.add_argument("--prompt", required=True, help="Prompt to send together with the uploaded image")
    parser.add_argument("--model", default=os.getenv("IMITATE_MODEL", "gpt-5-3"), help="Model name, default gpt-5-3")
    parser.add_argument("--purpose", default="assistants", help="Upload purpose, default assistants")
    parser.add_argument("--stream", action="store_true", help="Use streaming response")
    parser.add_argument("--timeout", type=float, default=120.0, help="Request timeout in seconds")
    parser.add_argument("--verify-file", action="store_true", help="GET /files/{id} after upload and print result")
    parser.add_argument("--dump-payload", action="store_true", help="Print the final chat payload before sending")
    parser.add_argument("--output", help="Optional path to save final JSON response (non-stream mode)")
    return parser.parse_args()


def build_headers(token: str) -> Dict[str, str]:
    if not token:
        raise SystemExit("Missing token. Pass --token or set IMITATE_API_KEY / IMITATE_ACCESS_TOKEN.")
    return {
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
    }


def ensure_ok(resp: requests.Response, stage: str) -> None:
    if resp.ok:
        return
    text = resp.text.strip()
    try:
        payload = resp.json()
        text = json.dumps(payload, ensure_ascii=False, indent=2)
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
        files = {
            "file": (image_path.name, fh, mime_type),
        }
        data = {"purpose": purpose}
        resp = requests.post(f"{base_url.rstrip('/')}/files", headers=headers, files=files, data=data, timeout=timeout)
    ensure_ok(resp, "file upload")
    payload = resp.json()
    if "id" not in payload:
        raise APIError(f"file upload returned no id:\n{json.dumps(payload, ensure_ascii=False, indent=2)}")
    return payload



def retrieve_file(base_url: str, token: str, file_id: str, timeout: float) -> Dict[str, Any]:
    headers = build_headers(token)
    resp = requests.get(f"{base_url.rstrip('/')}/files/{file_id}", headers=headers, timeout=timeout)
    ensure_ok(resp, "retrieve file")
    return resp.json()



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



def stream_completion(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> None:
    headers = build_headers(token)
    headers["Accept"] = "text/event-stream"
    resp = requests.post(
        f"{base_url.rstrip('/')}/chat/completions",
        headers=headers,
        json=payload,
        timeout=timeout,
        stream=True,
    )
    ensure_ok(resp, "chat completion(stream)")

    print("\n===== stream begin =====\n")
    for raw_line in resp.iter_lines(decode_unicode=True):
        if not raw_line:
            continue
        line = raw_line.strip()
        if not line.startswith("data: "):
            continue
        data = line[6:]
        if data == "[DONE]":
            print("\n===== stream done =====")
            return
        try:
            chunk = json.loads(data)
        except Exception:
            print(f"[raw] {data}")
            continue
        try:
            delta = chunk["choices"][0].get("delta", {})
            text = delta.get("content", "")
            if text:
                print(text, end="", flush=True)
        except Exception:
            print(f"\n[chunk] {json.dumps(chunk, ensure_ascii=False)}")
    print("\n===== stream ended without [DONE] =====")



def nonstream_completion(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    headers = build_headers(token)
    resp = requests.post(
        f"{base_url.rstrip('/')}/chat/completions",
        headers=headers,
        json=payload,
        timeout=timeout,
    )
    ensure_ok(resp, "chat completion")
    return resp.json()



def extract_text(response_json: Dict[str, Any]) -> str:
    choices = response_json.get("choices") or []
    if not choices:
        return ""
    message = choices[0].get("message") or {}
    content = message.get("content")
    if isinstance(content, str):
        return content
    return ""



def main() -> int:
    args = parse_args()
    image_path = Path(args.image)
    if not image_path.is_file():
        raise SystemExit(f"Image file not found: {image_path}")

    print(f"[1/3] Uploading: {image_path}")
    upload_resp = upload_file(args.base_url, args.token, image_path, args.purpose, args.timeout)
    print(json.dumps(upload_resp, ensure_ascii=False, indent=2))
    file_id = upload_resp["id"]

    if args.verify_file:
        print("\n[2/3] Retrieving uploaded file metadata")
        retrieve_resp = retrieve_file(args.base_url, args.token, file_id, args.timeout)
        print(json.dumps(retrieve_resp, ensure_ascii=False, indent=2))
    else:
        print("\n[2/3] Skip GET /files/{id} verification")

    payload = build_chat_payload(args.model, args.prompt, file_id, args.stream)
    if args.dump_payload:
        print("\n[debug] chat payload:")
        print(json.dumps(payload, ensure_ascii=False, indent=2))

    print("\n[3/3] Sending chat/completions request")
    if args.stream:
        stream_completion(args.base_url, args.token, payload, args.timeout)
        return 0

    response_json = nonstream_completion(args.base_url, args.token, payload, args.timeout)
    pretty = json.dumps(response_json, ensure_ascii=False, indent=2)
    print(pretty)

    text = extract_text(response_json)
    if text:
        print("\n===== assistant text =====\n")
        print(text)

    if args.output:
        Path(args.output).write_text(pretty, encoding="utf-8")
        print(f"\nSaved response JSON to: {args.output}")

    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except APIError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
