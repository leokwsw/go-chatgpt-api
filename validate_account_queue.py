#!/usr/bin/env python3
"""Validate same-account request serialization for /imitate/v1/chat/completions.

This script starts two streaming requests at the same time with the same token.
When account serialization is working, the second request should not receive its
first response byte until after the first request has finished.
"""

from __future__ import annotations

import argparse
import json
import threading
import time
import urllib.error
import urllib.request
from typing import Any


DEFAULT_FIRST_PROMPT = (
    "请输出数字 1 到 200，每行一个数字，不要添加额外说明。"
)
DEFAULT_SECOND_PROMPT = "只回复：ok"


def build_payload(model: str, prompt: str) -> bytes:
    return json.dumps(
        {
            "model": model,
            "stream": True,
            "messages": [
                {
                    "role": "user",
                    "content": prompt,
                }
            ],
        }
    ).encode("utf-8")


def send_stream_request(
    name: str,
    url: str,
    token: str,
    model: str,
    prompt: str,
    timeout: float,
    start_barrier: threading.Barrier,
    results: dict[str, dict[str, Any]],
) -> None:
    req = urllib.request.Request(
        url,
        data=build_payload(model, prompt),
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
        method="POST",
    )

    started_at = time.monotonic()
    first_byte_at = None
    status = None
    error = None

    try:
        start_barrier.wait(timeout=timeout)
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = getattr(resp, "status", None)
            while True:
                chunk = resp.read(1024)
                if not chunk:
                    break
                if first_byte_at is None:
                    first_byte_at = time.monotonic()
        finished_at = time.monotonic()
    except (urllib.error.URLError, TimeoutError, BrokenPipeError, threading.BrokenBarrierError) as exc:
        finished_at = time.monotonic()
        error = str(exc)
    except Exception as exc:  # pragma: no cover - best effort diagnostics
        finished_at = time.monotonic()
        error = repr(exc)

    results[name] = {
        "started_at": started_at,
        "first_byte_at": first_byte_at,
        "finished_at": finished_at,
        "status": status,
        "error": error,
    }


def monotonic_delta(start: float, ts: float | None) -> str:
    if ts is None:
        return "-"
    return f"{ts - start:.3f}s"


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate same-token request serialization.")
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--token", required=True)
    parser.add_argument("--model", default="gpt-5-3")
    parser.add_argument("--timeout", type=float, default=180.0)
    parser.add_argument("--first-prompt", default=DEFAULT_FIRST_PROMPT)
    parser.add_argument("--second-prompt", default=DEFAULT_SECOND_PROMPT)
    args = parser.parse_args()

    url = args.base_url.rstrip("/") + "/imitate/v1/chat/completions"
    barrier = threading.Barrier(2)
    results: dict[str, dict[str, Any]] = {}

    first = threading.Thread(
        target=send_stream_request,
        args=(
            "first",
            url,
            args.token,
            args.model,
            args.first_prompt,
            args.timeout,
            barrier,
            results,
        ),
    )
    second = threading.Thread(
        target=send_stream_request,
        args=(
            "second",
            url,
            args.token,
            args.model,
            args.second_prompt,
            args.timeout,
            barrier,
            results,
        ),
    )

    started_at = time.monotonic()
    first.start()
    second.start()
    first.join()
    second.join()

    print(f"url: {url}")
    for name in ("first", "second"):
        result = results.get(name, {})
        print(f"{name}:")
        print(f"  status: {result.get('status')}")
        print(f"  error: {result.get('error')}")
        print(f"  first_byte: {monotonic_delta(started_at, result.get('first_byte_at'))}")
        print(f"  finished: {monotonic_delta(started_at, result.get('finished_at'))}")

    first_result = results.get("first", {})
    second_result = results.get("second", {})
    first_finished = first_result.get("finished_at")
    second_first_byte = second_result.get("first_byte_at")

    if first_result.get("error") or second_result.get("error"):
        print("result: unable to verify because at least one request failed")
        return 1

    if first_finished is None or second_first_byte is None:
        print("result: unable to verify because timing data is incomplete")
        return 1

    if second_first_byte >= first_finished:
        print("result: PASS - second request started responding only after the first completed")
        return 0

    print("result: FAIL - second request began responding before the first completed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
