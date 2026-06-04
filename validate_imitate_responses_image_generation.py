#!/usr/bin/env python3
"""Validate /imitate/v1/responses image_generation compatibility."""

from __future__ import annotations

import argparse
import base64
import json
import os
from pathlib import Path
from typing import Any, Dict, Iterable, Optional

try:
    import requests
except Exception as exc:  # pragma: no cover
    raise SystemExit(
        "This script requires the 'requests' package. Install it with: pip install requests\n"
        f"Import error: {exc}"
    )


DEFAULT_BASE_URL = "http://127.0.0.1:8080/imitate/v1"
DEFAULT_PROMPT = (
    "Create a simple vocabulary teaching image for the word 'sincere'. "
    "Then return JSON text with keys word and image_text."
)


class APIError(RuntimeError):
    pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Validate Responses image_generation output shape")
    parser.add_argument(
        "--base-url",
        default=os.getenv("IMITATE_BASE_URL", DEFAULT_BASE_URL),
        help=f"Base URL for imitate v1, default: {DEFAULT_BASE_URL}",
    )
    parser.add_argument(
        "--token",
        default=os.getenv("IMITATE_API_KEY") or os.getenv("IMITATE_ACCESS_TOKEN"),
        help="Bearer token accepted by /imitate/v1: use IMITATE_API_KEY or direct ChatGPT access token, not sk-*",
    )
    parser.add_argument(
        "--model",
        default=os.getenv("IMITATE_MODEL", "gpt-5.5"),
        help="Model name, default: gpt-5.5",
    )
    parser.add_argument("--prompt", default=DEFAULT_PROMPT, help="User prompt")
    parser.add_argument("--size", default="1024x1024", help="Requested image size")
    parser.add_argument("--quality", default="auto", help="Requested image quality")
    parser.add_argument("--image-format", default="png", choices=["png", "jpeg", "webp"])
    parser.add_argument("--timeout", type=float, default=300.0, help="Request timeout in seconds")
    parser.add_argument("--force-image-tool", action="store_true", help="Send tool_choice image_generation")
    parser.add_argument("--output-image", help="Optional path to write decoded image bytes")
    parser.add_argument("--response-json", help="Optional path to write raw response JSON")
    return parser.parse_args()


def build_headers(token: str) -> Dict[str, str]:
    if not token:
        raise SystemExit("Missing token. Pass --token or set IMITATE_API_KEY / IMITATE_ACCESS_TOKEN.")
    return {
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
        "Content-Type": "application/json",
    }


def ensure_ok(resp: requests.Response) -> None:
    if resp.ok:
        return
    text = resp.text.strip()
    try:
        text = json.dumps(resp.json(), ensure_ascii=False, indent=2)
    except Exception:
        pass
    raise APIError(f"responses image_generation failed: HTTP {resp.status_code}\n{text}")


def build_payload(args: argparse.Namespace) -> Dict[str, Any]:
    payload: Dict[str, Any] = {
        "model": args.model,
        "input": [
            {
                "role": "system",
                "content": (
                    "Use image generation when available. Return a compact JSON sidecar as text."
                ),
            },
            {"role": "user", "content": args.prompt},
        ],
        "tools": [
            {
                "type": "image_generation",
                "size": args.size,
                "quality": args.quality,
                "format": args.image_format,
            }
        ],
        "text": {
            "format": {
                "type": "json_schema",
                "name": "image_generation_sidecar",
                "schema": {
                    "type": "object",
                    "additionalProperties": True,
                    "properties": {
                        "word": {"type": "string"},
                        "image_text": {"type": "string"},
                    },
                },
                "strict": False,
            }
        },
        "max_output_tokens": 1200,
        "store": False,
    }
    if args.force_image_tool:
        payload["tool_choice"] = {"type": "image_generation"}
    return payload


def responses_create(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    resp = requests.post(
        f"{base_url.rstrip('/')}/responses",
        headers=build_headers(token),
        json=payload,
        timeout=timeout,
    )
    ensure_ok(resp)
    return resp.json()


def iter_output_items(response_json: Dict[str, Any]) -> Iterable[Dict[str, Any]]:
    output = response_json.get("output") or []
    for item in output:
        if isinstance(item, dict):
            yield item


def find_image_generation_call(response_json: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    for item in iter_output_items(response_json):
        if item.get("type") == "image_generation_call":
            return item
    return None


def collect_output_text(response_json: Dict[str, Any]) -> str:
    chunks: list[str] = []
    for item in iter_output_items(response_json):
        for content in item.get("content") or []:
            if isinstance(content, dict) and content.get("type") == "output_text":
                chunks.append(str(content.get("text") or ""))
    return "".join(chunks).strip()


def main() -> None:
    args = parse_args()
    payload = build_payload(args)
    response_json = responses_create(args.base_url, args.token, payload, args.timeout)

    if args.response_json:
        Path(args.response_json).write_text(json.dumps(response_json, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    image_call = find_image_generation_call(response_json)
    if image_call is None:
        raise APIError("response did not include image_generation_call")
    image_base64 = str(image_call.get("result") or "")
    if not image_base64:
        raise APIError("image_generation_call did not include result")
    try:
        image_bytes = base64.b64decode(image_base64, validate=True)
    except Exception as exc:
        raise APIError(f"image_generation_call.result is not valid base64: {exc}") from exc
    if not image_bytes:
        raise APIError("image_generation_call.result decoded to empty bytes")

    output_text = collect_output_text(response_json)
    if not output_text:
        raise APIError("response did not include output_text sidecar")

    if args.output_image:
        Path(args.output_image).write_bytes(image_bytes)

    print("PASS: image_generation_call.result decoded")
    print(f"response_id: {response_json.get('id')}")
    print(f"image_bytes: {len(image_bytes)}")
    print("output_text:")
    print(output_text)


if __name__ == "__main__":
    main()
