#!/usr/bin/env python3
"""
Parse ChatGPT HAR files and extract request bodies/headers used by:
  - /backend-api/conversation
  - /backend-anon/conversation
  - /sentinel/chat-requirements

Usage:
  python scripts/parse_chatgpt_har.py --har harPool/chat.openai.com.har
  python scripts/parse_chatgpt_har.py --har harPool/chat.openai.com.har --all --pretty
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any, Dict, List, Optional
from urllib.parse import urlparse


CONVERSATION_PATHS = {
    "/backend-api/conversation",
    "/backend-anon/conversation",
    "/backend-api/f/conversation",
    "/backend-anon/f/conversation",
}
CONVERSATION_PREPARE_PATHS = {
    "/backend-api/f/conversation/prepare",
    "/backend-anon/f/conversation/prepare",
}
REQUIREMENTS_PATHS = {
    "/backend-api/sentinel/chat-requirements",
    "/backend-anon/sentinel/chat-requirements",
}
TASKS_PATHS = {
    "/backend-api/tasks",
    "/backend-anon/tasks",
}


def _normalize_headers(headers: List[Dict[str, Any]]) -> Dict[str, str]:
    result: Dict[str, str] = {}
    for h in headers:
        name = str(h.get("name", "")).strip().lower()
        value = str(h.get("value", ""))
        if name:
            result[name] = value
    return result


def _parse_post_data(entry: Dict[str, Any]) -> Optional[Any]:
    text = (
        entry.get("request", {})
        .get("postData", {})
        .get("text")
    )
    if text is None:
        return None
    try:
        return json.loads(text)
    except Exception:
        return text


def _get_path(url: str) -> str:
    try:
        return urlparse(url).path or ""
    except Exception:
        return ""


def _match_kind(url: str, method: str, include_experimental: bool) -> Optional[str]:
    if method.upper() != "POST":
        return None

    path = _get_path(url)

    # Strict conversation endpoint matching (avoid experimental subpaths by default)
    if path in CONVERSATION_PATHS:
        return "conversation"
    if path in CONVERSATION_PREPARE_PATHS:
        return "conversation_prepare"

    if include_experimental and "/conversation/" in path:
        return "conversation_experimental"

    # chat-requirements and finalize
    for base in REQUIREMENTS_PATHS:
        if path == base:
            return "chat_requirements"
        if path == f"{base}/finalize":
            return "chat_requirements_finalize"
    return None


def _is_replayable_conversation(payload: Any) -> bool:
    if not isinstance(payload, dict):
        return False
    required_keys = {"action", "messages", "model"}
    return required_keys.issubset(payload.keys())


def _redact_header(value: str, include_sensitive_headers: bool) -> str:
    if include_sensitive_headers or not value:
        return value
    return f"<redacted:{len(value)} chars>"


def extract_entries(
    har_data: Dict[str, Any],
    include_experimental: bool,
    include_sensitive_headers: bool = False,
) -> List[Dict[str, Any]]:
    entries = har_data.get("log", {}).get("entries", [])
    extracted: List[Dict[str, Any]] = []

    for item in entries:
        req = item.get("request", {})
        method = str(req.get("method", ""))
        url = str(req.get("url", ""))
        kind = _match_kind(url, method, include_experimental)
        if not kind:
            continue

        headers = _normalize_headers(req.get("headers", []))
        payload = _parse_post_data(item)

        extracted_item = (
            {
                "startedDateTime": item.get("startedDateTime"),
                "kind": kind,
                "url": url,
                "method": method,
                "requestHeaders": {
                    "authorization": _redact_header(headers.get("authorization", ""), include_sensitive_headers),
                    "openai-sentinel-arkose-token": _redact_header(
                        headers.get("openai-sentinel-arkose-token", ""),
                        include_sensitive_headers,
                    ),
                    "openai-sentinel-chat-requirements-token": _redact_header(
                        headers.get("openai-sentinel-chat-requirements-token", ""),
                        include_sensitive_headers,
                    ),
                    "openai-sentinel-proof-token": _redact_header(
                        headers.get("openai-sentinel-proof-token", ""),
                        include_sensitive_headers,
                    ),
                    "openai-sentinel-turnstile-token": _redact_header(
                        headers.get("openai-sentinel-turnstile-token", ""),
                        include_sensitive_headers,
                    ),
                    "oai-device-id": _redact_header(
                        headers.get("oai-device-id", ""),
                        include_sensitive_headers,
                    ),
                    "oai-language": headers.get("oai-language", ""),
                    "user-agent": headers.get("user-agent", ""),
                    "origin": headers.get("origin", ""),
                    "referer": headers.get("referer", ""),
                },
                "requestBody": payload,
            }
        )
        if kind == "conversation" and not _is_replayable_conversation(payload):
            extracted_item["kind"] = "conversation_non_replayable"
        extracted.append(extracted_item)

    extracted.sort(key=lambda x: str(x.get("startedDateTime", "")))
    return extracted


def collect_conversation_like_urls(har_data: Dict[str, Any]) -> List[str]:
    entries = har_data.get("log", {}).get("entries", [])
    urls: List[str] = []
    for item in entries:
        req = item.get("request", {})
        method = str(req.get("method", "")).upper()
        if method != "POST":
            continue
        url = str(req.get("url", ""))
        path = _get_path(url)
        if "/conversation" in path:
            urls.append(url)
    # preserve order + de-dup
    seen = set()
    ordered = []
    for u in urls:
        if u not in seen:
            seen.add(u)
            ordered.append(u)
    return ordered


def _count_image_asset_pointers(value: Any) -> int:
    if isinstance(value, dict):
        count = 0
        if value.get("content_type") == "image_asset_pointer" and value.get("asset_pointer"):
            count += 1
        return count + sum(_count_image_asset_pointers(v) for v in value.values())
    if isinstance(value, list):
        return sum(_count_image_asset_pointers(v) for v in value)
    return 0


def _summarize_task(task: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "task_id": task.get("task_id"),
        "status": task.get("status"),
        "conversation_id": task.get("conversation_id"),
        "original_conversation_id": task.get("original_conversation_id"),
        "response_message_id": task.get("response_message_id"),
        "original_conversation_user_message_id": task.get("original_conversation_user_message_id"),
        "has_image_gen_message": isinstance(task.get("image_gen_message"), dict),
        "has_final_message": isinstance(task.get("final_message"), dict),
        "messages_count": len(task.get("messages") or []),
        "image_asset_pointer_count": _count_image_asset_pointers(task),
    }


def collect_task_summaries(har_data: Dict[str, Any]) -> List[Dict[str, Any]]:
    summaries: List[Dict[str, Any]] = []
    entries = har_data.get("log", {}).get("entries", [])
    for item in entries:
        req = item.get("request", {})
        method = str(req.get("method", "")).upper()
        if method != "GET":
            continue
        if _get_path(str(req.get("url", ""))) not in TASKS_PATHS:
            continue
        text = item.get("response", {}).get("content", {}).get("text")
        if not text:
            continue
        try:
            payload = json.loads(text)
        except Exception:
            continue
        tasks = payload.get("tasks") if isinstance(payload, dict) else None
        if not isinstance(tasks, list):
            continue
        summaries.append(
            {
                "startedDateTime": item.get("startedDateTime"),
                "tasks": [_summarize_task(t) for t in tasks if isinstance(t, dict)],
            }
        )
    return summaries


def main() -> None:
    parser = argparse.ArgumentParser(description="Extract ChatGPT conversation payloads from HAR.")
    parser.add_argument("--har", required=True, help="Path to HAR file.")
    parser.add_argument("--out", default="", help="Optional output JSON path.")
    parser.add_argument("--all", action="store_true", help="Output all matched entries.")
    parser.add_argument("--pretty", action="store_true", help="Pretty-print JSON.")
    parser.add_argument(
        "--include-experimental",
        action="store_true",
        help="Include non-standard /conversation/* experimental endpoints.",
    )
    parser.add_argument(
        "--only-replayable",
        action="store_true",
        help="When set, only output replayable conversation payloads (action/messages/model).",
    )
    parser.add_argument(
        "--include-sensitive-headers",
        action="store_true",
        help="Include raw authorization/sentinel/device headers. Defaults to redacted output.",
    )
    args = parser.parse_args()

    har_path = Path(args.har)
    if not har_path.exists():
        raise SystemExit(f"HAR file not found: {har_path}")

    with har_path.open("r", encoding="utf-8") as f:
        har_data = json.load(f)

    extracted = extract_entries(
        har_data,
        include_experimental=args.include_experimental,
        include_sensitive_headers=args.include_sensitive_headers,
    )
    if not extracted:
        raise SystemExit("No matching entries found in HAR.")
    conversation_like_urls = collect_conversation_like_urls(har_data)
    task_summaries = collect_task_summaries(har_data)

    output_obj: Any
    output_entries = extracted
    if args.only_replayable:
        output_entries = [
            e
            for e in extracted
            if e["kind"]
            in {
                "conversation",
                "conversation_prepare",
                "chat_requirements",
                "chat_requirements_finalize",
            }
        ]

    if args.all:
        output_obj = output_entries
    else:
        latest_conv = next((e for e in reversed(output_entries) if e["kind"] == "conversation"), None)
        latest_conv_prepare = next((e for e in reversed(output_entries) if e["kind"] == "conversation_prepare"), None)
        latest_req = next((e for e in reversed(output_entries) if e["kind"] == "chat_requirements"), None)
        latest_req_finalize = next((e for e in reversed(output_entries) if e["kind"] == "chat_requirements_finalize"), None)
        skipped_non_replayable = len([e for e in extracted if e["kind"] == "conversation_non_replayable"])
        latest_conv_candidate = latest_conv or latest_conv_prepare
        output_obj = {
            "latest_conversation": latest_conv,
            "latest_conversation_prepare": latest_conv_prepare,
            "latest_conversation_candidate": latest_conv_candidate,
            "latest_chat_requirements": latest_req,
            "latest_chat_requirements_finalize": latest_req_finalize,
            "total_matches": len(extracted),
            "total_output_entries": len(output_entries),
            "skipped_non_replayable_conversations": skipped_non_replayable,
            "conversation_like_urls_seen": conversation_like_urls,
            "task_summaries": task_summaries,
            "hint": (
                "No replayable /backend-api/conversation request found in this HAR. "
                "Found fallback candidates under /backend-api/f/conversation. "
                "Use latest_conversation_candidate or capture a normal send-message flow and export HAR again."
                if latest_conv_candidate is None
                else ""
            ),
        }

    if args.pretty:
        content = json.dumps(output_obj, ensure_ascii=False, indent=2)
    else:
        content = json.dumps(output_obj, ensure_ascii=False)

    if args.out:
        out_path = Path(args.out)
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(content + "\n", encoding="utf-8")
        print(f"Wrote output: {out_path}")
    else:
        print(content)


if __name__ == "__main__":
    main()
