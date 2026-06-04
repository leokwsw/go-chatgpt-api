#!/usr/bin/env python3
"""Validate /imitate compatibility with official Files, Chat Completions, and Responses payloads."""

from __future__ import annotations

import argparse
import base64
import json
import mimetypes
import os
import sys
import time
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional

try:
    import requests
except Exception as exc:  # pragma: no cover
    raise SystemExit(
        "This script requires the 'requests' package. Install it with: pip install requests\n"
        f"Import error: {exc}"
    )


DEFAULT_PROMPT = "请提取这张图片中的文字。"
DEFAULT_BASE_URL = "http://127.0.0.1:8080/imitate/v1"


class APIError(RuntimeError):
    pass


def parse_args() -> argparse.Namespace:
    repo_root = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Validate official-style /imitate API compatibility")
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
        help="Image file to upload and reference",
    )
    parser.add_argument(
        "--prompt",
        default=DEFAULT_PROMPT,
        help=f"OCR prompt, default: {DEFAULT_PROMPT}",
    )
    parser.add_argument(
        "--model",
        default=os.getenv("IMITATE_MODEL", "gpt-5-3"),
        help="Model name, default: gpt-5-3",
    )
    parser.add_argument(
        "--purpose",
        default="vision",
        help="Upload purpose for /files, default: vision",
    )
    parser.add_argument(
        "--expires-after-seconds",
        type=int,
        default=0,
        help="Optional official Files expires_after seconds, 3600..2592000",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=180.0,
        help="Request timeout in seconds, default: 180",
    )
    parser.add_argument(
        "--expected-substring",
        action="append",
        default=[],
        help="Expected OCR substring. Repeat this option for multiple substrings.",
    )
    parser.add_argument(
        "--also-stream",
        action="store_true",
        help="Also validate Responses API streaming events",
    )
    parser.add_argument(
        "--also-responses-data-url",
        action="store_true",
        help="Also validate official Responses input_image.image_url with a base64 data URL",
    )
    parser.add_argument(
        "--also-chat-url",
        action="store_true",
        help="Also validate official chat.completions image_url.url with --image-url",
    )
    parser.add_argument(
        "--image-url",
        default=os.getenv("IMITATE_IMAGE_URL", ""),
        help="Remote image URL used with --also-chat-url",
    )
    parser.add_argument(
        "--expect-no-image",
        action="store_true",
        help="Also validate deterministic missing-image fallback returns exactly 无图片",
    )
    parser.add_argument(
        "--also-responses-image-generation",
        action="store_true",
        help="Also validate Responses image_generation tool output and decode output[].result base64",
    )
    parser.add_argument(
        "--image-generation-prompt",
        default="Create a simple vocabulary teaching image for the word 'sincere'.",
        help="Prompt used with --also-responses-image-generation",
    )
    parser.add_argument(
        "--image-generation-output",
        default="",
        help="Optional path to write decoded image_generation result bytes",
    )
    parser.add_argument(
        "--retry-count",
        type=int,
        default=6,
        help="Retry count for eventual-consistency file checks, default: 6",
    )
    parser.add_argument(
        "--retry-interval",
        type=float,
        default=2.0,
        help="Seconds between eventual-consistency retries, default: 2",
    )
    parser.add_argument(
        "--skip-chat",
        action="store_true",
        help="Skip official chat.completions validation",
    )
    parser.add_argument(
        "--skip-responses",
        action="store_true",
        help="Skip official responses validation",
    )
    return parser.parse_args()


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


def encode_data_url(path: Path) -> str:
    mime_type = guess_mime(path)
    data = base64.b64encode(path.read_bytes()).decode("ascii")
    return f"data:{mime_type};base64,{data}"


def upload_file(
    base_url: str,
    token: str,
    image_path: Path,
    purpose: str,
    timeout: float,
    expires_after_seconds: int,
) -> Dict[str, Any]:
    headers = {"Authorization": f"Bearer {token}"}
    mime_type = guess_mime(image_path)
    data = {"purpose": purpose}
    if expires_after_seconds:
        data["expires_after[anchor]"] = "created_at"
        data["expires_after[seconds]"] = str(expires_after_seconds)
    with image_path.open("rb") as fh:
        resp = requests.post(
            f"{base_url.rstrip('/')}/files",
            headers=headers,
            files={"file": (image_path.name, fh, mime_type)},
            data=data,
            timeout=timeout,
        )
    ensure_ok(resp, "file upload")
    return resp.json()


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
    retry_count: int,
    retry_interval: float,
) -> Dict[str, Any]:
    last_error: Optional[APIError] = None
    attempts = max(1, retry_count)
    for attempt in range(1, attempts + 1):
        try:
            return retrieve_file(base_url, token, file_id, timeout)
        except APIError as exc:
            last_error = exc
            if "HTTP 404" not in str(exc) or attempt == attempts:
                raise
            print(f"GET /files/{{id}} not ready yet, retry {attempt}/{attempts} ...")
            time.sleep(retry_interval)
    raise last_error or APIError("retrieve file failed")


def list_files(base_url: str, token: str, purpose: str, timeout: float) -> Dict[str, Any]:
    resp = requests.get(
        f"{base_url.rstrip('/')}/files",
        headers=build_headers(token),
        params={"purpose": purpose, "limit": 20},
        timeout=timeout,
    )
    ensure_ok(resp, "list files")
    return resp.json()


def list_files_until_contains(
    base_url: str,
    token: str,
    purpose: str,
    timeout: float,
    expected_file_id: str,
    retry_count: int,
    retry_interval: float,
) -> Dict[str, Any]:
    attempts = max(1, retry_count)
    last_response: Optional[Dict[str, Any]] = None
    for attempt in range(1, attempts + 1):
        last_response = list_files(base_url, token, purpose, timeout)
        listed_ids = {item.get("id") for item in last_response.get("data") or []}
        if expected_file_id in listed_ids:
            return last_response
        if attempt != attempts:
            print(f"GET /files list not ready yet, retry {attempt}/{attempts} ...")
            time.sleep(retry_interval)
    raise APIError(f"uploaded file {expected_file_id} not found in GET /files list response")


def retrieve_file_content(base_url: str, token: str, file_id: str, timeout: float) -> bytes:
    resp = requests.get(
        f"{base_url.rstrip('/')}/files/{file_id}/content",
        headers=build_headers(token, accept="application/octet-stream"),
        timeout=timeout,
    )
    ensure_ok(resp, "retrieve file content")
    return resp.content


def retrieve_file_content_with_retry(
    base_url: str,
    token: str,
    file_id: str,
    timeout: float,
    retry_count: int,
    retry_interval: float,
) -> bytes:
    last_error: Optional[APIError] = None
    attempts = max(1, retry_count)
    for attempt in range(1, attempts + 1):
        try:
            return retrieve_file_content(base_url, token, file_id, timeout)
        except APIError as exc:
            last_error = exc
            if "HTTP 404" not in str(exc) or attempt == attempts:
                raise
            print(f"GET /files/{{id}}/content not ready yet, retry {attempt}/{attempts} ...")
            time.sleep(retry_interval)
    raise last_error or APIError("retrieve file content failed")


def chat_completion(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    resp = requests.post(
        f"{base_url.rstrip('/')}/chat/completions",
        headers=build_headers(token),
        json=payload,
        timeout=timeout,
    )
    ensure_ok(resp, "chat completion")
    return resp.json()


def responses_create(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> Dict[str, Any]:
    resp = requests.post(
        f"{base_url.rstrip('/')}/responses",
        headers=build_headers(token),
        json=payload,
        timeout=timeout,
    )
    ensure_ok(resp, "responses create")
    return resp.json()


def responses_stream(base_url: str, token: str, payload: Dict[str, Any], timeout: float) -> str:
    resp = requests.post(
        f"{base_url.rstrip('/')}/responses",
        headers=build_headers(token, accept="text/event-stream"),
        json=payload,
        timeout=timeout,
        stream=True,
    )
    ensure_ok(resp, "responses stream")
    resp.encoding = "utf-8"

    text_chunks: List[str] = []
    current_event = ""
    for raw_line in resp.iter_lines(decode_unicode=True):
        if raw_line is None:
            continue
        line = raw_line.strip()
        if not line:
            current_event = ""
            continue
        if line.startswith("event: "):
            current_event = line[7:]
            continue
        if not line.startswith("data: "):
            continue
        data = line[6:]
        try:
            payload_json = json.loads(data)
        except Exception:
            continue
        event_type = payload_json.get("type") or current_event
        if event_type == "response.output_text.delta":
            delta = payload_json.get("delta") or ""
            if delta:
                text_chunks.append(delta)
        elif event_type == "response.output_text.done" and not text_chunks:
            text_chunks.append(payload_json.get("text") or "")
    return "".join(text_chunks)


def responses_stream_with_retry(
    base_url: str,
    token: str,
    payload: Dict[str, Any],
    timeout: float,
    retry_count: int,
    retry_interval: float,
) -> str:
    attempts = max(1, retry_count)
    for attempt in range(1, attempts + 1):
        text = responses_stream(base_url, token, payload, timeout)
        if text.strip():
            return text
        if attempt != attempts:
            print(f"Responses stream text empty, retry {attempt}/{attempts} ...")
            time.sleep(retry_interval)
    return ""


def responses_create_with_retry(
    base_url: str,
    token: str,
    payload: Dict[str, Any],
    timeout: float,
    expected_substrings: Iterable[str],
    retry_count: int,
    retry_interval: float,
) -> Dict[str, Any]:
    attempts = max(1, retry_count)
    last_error: Optional[APIError] = None
    for attempt in range(1, attempts + 1):
        response_json = responses_create(base_url, token, payload, timeout)
        text = extract_responses_text(response_json)
        try:
            assert_contains(text, expected_substrings, "official responses response")
            return response_json
        except APIError as exc:
            last_error = exc
            if attempt != attempts:
                print(f"Responses text not ready yet, retry {attempt}/{attempts} ...")
                time.sleep(retry_interval)
                continue
            raise
    raise last_error or APIError("responses create failed")


def build_chat_image_payload(model: str, prompt: str, image_url: str) -> Dict[str, Any]:
    return {
        "model": model,
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": prompt},
                    {
                        "type": "image_url",
                        "image_url": {
                            "url": image_url,
                        },
                    },
                ],
            }
        ],
    }


def build_responses_image_payload(model: str, prompt: str, image_part: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "model": model,
        "input": [
            {
                "type": "message",
                "role": "user",
                "content": [
                    {"type": "input_text", "text": prompt},
                    image_part,
                ],
            }
        ],
    }


def build_responses_image_generation_payload(model: str, prompt: str) -> Dict[str, Any]:
    return {
        "model": model,
        "input": [{"role": "user", "content": prompt}],
        "tools": [{"type": "image_generation", "size": "1024x1024", "quality": "auto", "format": "png"}],
        "tool_choice": {"type": "image_generation"},
        "store": False,
    }


def extract_chat_text(response_json: Dict[str, Any]) -> str:
    choices = response_json.get("choices") or []
    if not choices:
        return ""
    message = choices[0].get("message") or {}
    content = message.get("content")
    return content if isinstance(content, str) else ""


def extract_responses_text(response_json: Dict[str, Any]) -> str:
    output = response_json.get("output") or []
    for item in output:
        if item.get("type") != "message":
            continue
        for content in item.get("content") or []:
            if content.get("type") == "output_text":
                text = content.get("text")
                if isinstance(text, str):
                    return text
    return ""


def extract_responses_image_generation_result(response_json: Dict[str, Any]) -> bytes:
    for item in response_json.get("output") or []:
        if not isinstance(item, dict) or item.get("type") != "image_generation_call":
            continue
        image_base64 = item.get("result")
        if not isinstance(image_base64, str) or not image_base64:
            raise APIError("image_generation_call missing non-empty result")
        try:
            image_bytes = base64.b64decode(image_base64, validate=True)
        except Exception as exc:
            raise APIError(f"image_generation_call.result is not valid base64: {exc}") from exc
        if not image_bytes:
            raise APIError("image_generation_call.result decoded to empty bytes")
        return image_bytes
    raise APIError("responses image_generation did not include image_generation_call")


def assert_contains(text: str, expected_substrings: Iterable[str], label: str) -> None:
    text = text.strip()
    if not text:
        raise APIError(f"{label} returned empty assistant text")
    expected = [item for item in expected_substrings if item]
    if not expected:
        return
    missing = [item for item in expected if item not in text]
    if missing:
        raise APIError(f"{label} missing expected substrings: {missing}\nActual reply:\n{text}")


def assert_exact_no_image(text: str, label: str) -> None:
    actual = text.strip()
    if actual != "无图片":
        raise APIError(f"{label} = {actual!r}, want exact '无图片'")


def print_json(title: str, payload: Dict[str, Any]) -> None:
    print(title)
    print(json.dumps(payload, ensure_ascii=False, indent=2))


def main() -> int:
    args = parse_args()
    image_path = Path(args.image)
    if not image_path.is_file():
        raise SystemExit(f"Image file not found: {image_path}")

    print("[1/6] Uploading file via official Files API shape")
    upload_resp = upload_file(
        args.base_url,
        args.token,
        image_path,
        args.purpose,
        args.timeout,
        args.expires_after_seconds,
    )
    print_json("Upload response:", upload_resp)
    file_id = upload_resp["id"]
    if args.expires_after_seconds:
        if not isinstance(upload_resp.get("expires_at"), int):
            raise APIError("file upload response missing integer expires_at")
        expected_expires_at = upload_resp.get("created_at", 0) + args.expires_after_seconds
        if upload_resp["expires_at"] != expected_expires_at:
            raise APIError(
                f"expires_at = {upload_resp['expires_at']}, want {expected_expires_at}"
            )

    print("\n[2/6] Retrieving uploaded file metadata")
    retrieve_resp = retrieve_file_with_retry(
        args.base_url,
        args.token,
        file_id,
        args.timeout,
        args.retry_count,
        args.retry_interval,
    )
    print_json("Retrieve response:", retrieve_resp)

    print("\n[3/6] Listing files with official pagination params")
    list_resp = list_files_until_contains(
        args.base_url,
        args.token,
        retrieve_resp.get("purpose", args.purpose),
        args.timeout,
        file_id,
        args.retry_count,
        args.retry_interval,
    )
    print_json("List response:", list_resp)

    if args.skip_chat:
        print("\n[4/6] Skip official chat.completions validation")
    else:
        print("\n[4/6] Sending official chat.completions image_url payload")
        chat_payload = build_chat_image_payload(args.model, args.prompt, encode_data_url(image_path))
        chat_resp = chat_completion(args.base_url, args.token, chat_payload, args.timeout)
        print_json("Chat response:", chat_resp)
        chat_text = extract_chat_text(chat_resp)
        print("\nChat text:")
        print(chat_text or "<empty>")
        assert_contains(chat_text, args.expected_substring, "official chat.completions response")

        if args.also_chat_url:
            if not args.image_url:
                raise SystemExit("--also-chat-url requires --image-url or IMITATE_IMAGE_URL")
            print("\n[4b/6] Sending official chat.completions remote image_url payload")
            chat_url_payload = build_chat_image_payload(args.model, args.prompt, args.image_url)
            chat_url_resp = chat_completion(args.base_url, args.token, chat_url_payload, args.timeout)
            print_json("Chat URL response:", chat_url_resp)
            chat_url_text = extract_chat_text(chat_url_resp)
            print("\nChat URL text:")
            print(chat_url_text or "<empty>")
            assert_contains(chat_url_text, args.expected_substring, "official chat.completions URL response")

    if args.skip_responses:
        print("\n[5/6] Skip official responses validation")
    else:
        print("\n[5/6] Sending official responses input_image payload")
        responses_payload = build_responses_image_payload(
            args.model,
            args.prompt,
            {"type": "input_image", "file_id": file_id},
        )
        responses_resp = responses_create_with_retry(
            args.base_url,
            args.token,
            responses_payload,
            args.timeout,
            args.expected_substring,
            args.retry_count,
            args.retry_interval,
        )
        print_json("Responses response:", responses_resp)
        responses_text = extract_responses_text(responses_resp)
        print("\nResponses text:")
        print(responses_text or "<empty>")

        if args.also_stream:
            stream_payload = dict(responses_payload)
            stream_payload["stream"] = True
            stream_text = responses_stream_with_retry(
                args.base_url,
                args.token,
                stream_payload,
                args.timeout,
                args.retry_count,
                args.retry_interval,
            )
            print("\nResponses stream text:")
            print(stream_text or "<empty>")
            assert_contains(stream_text, args.expected_substring, "official responses stream")

        if args.also_responses_data_url:
            print("\n[5b/6] Sending official responses input_image.image_url data URL payload")
            responses_data_url_payload = build_responses_image_payload(
                args.model,
                args.prompt,
                {"type": "input_image", "image_url": encode_data_url(image_path), "detail": "auto"},
            )
            responses_data_url_resp = responses_create_with_retry(
                args.base_url,
                args.token,
                responses_data_url_payload,
                args.timeout,
                args.expected_substring,
                args.retry_count,
                args.retry_interval,
            )
            print_json("Responses data URL response:", responses_data_url_resp)
            responses_data_url_text = extract_responses_text(responses_data_url_resp)
            print("\nResponses data URL text:")
            print(responses_data_url_text or "<empty>")

    if args.expect_no_image:
        print("\n[5c/6] Sending official responses missing-image fallback payload")
        no_image_payload = build_responses_image_payload(
            args.model,
            args.prompt,
            {"type": "input_image", "file_id": "file_does_not_exist"},
        )
        no_image_resp = responses_create(args.base_url, args.token, no_image_payload, args.timeout)
        print_json("No-image response:", no_image_resp)
        no_image_text = extract_responses_text(no_image_resp)
        print("\nNo-image text:")
        print(no_image_text or "<empty>")
        assert_exact_no_image(no_image_text, "official responses no-image fallback")

    if args.also_responses_image_generation:
        print("\n[5d/6] Sending official responses image_generation payload")
        image_generation_payload = build_responses_image_generation_payload(
            args.model,
            args.image_generation_prompt,
        )
        image_generation_resp = responses_create(args.base_url, args.token, image_generation_payload, args.timeout)
        print_json("Responses image_generation response:", image_generation_resp)
        image_generation_bytes = extract_responses_image_generation_result(image_generation_resp)
        if args.image_generation_output:
            Path(args.image_generation_output).write_bytes(image_generation_bytes)
        print(f"Decoded image_generation result bytes: {len(image_generation_bytes)}")

    print("\n[6/6] Retrieving raw file content")
    content_bytes = retrieve_file_content_with_retry(
        args.base_url,
        args.token,
        file_id,
        args.timeout,
        args.retry_count,
        args.retry_interval,
    )
    original_bytes = image_path.read_bytes()
    if content_bytes != original_bytes:
        raise APIError("GET /files/{id}/content bytes do not match the uploaded file")
    print(f"Retrieved {len(content_bytes)} bytes")

    print("\nValidation passed.")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except APIError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
