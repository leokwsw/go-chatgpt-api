from __future__ import annotations

import base64
import os
from pathlib import Path

from openai import OpenAI


BASE_URL = os.getenv("BASE_URL", "http://127.0.0.1:8080/imitate/v1")
API_KEY = os.getenv("IMITATE_API_KEY") or os.getenv("IMITATE_ACCESS_TOKEN") or os.environ["OPENAI_API_KEY"]
MODEL = os.getenv("IMITATE_MODEL", "gpt-5-3")
IMAGE_PATH = Path(os.getenv("IMITATE_IMAGE", Path(__file__).resolve().parents[2] / "医疗.jpg"))
PROMPT = os.getenv("IMITATE_PROMPT", "请提取这张图片中的文字。")

client = OpenAI(base_url=BASE_URL, api_key=API_KEY)


def to_data_url(path: Path) -> str:
    suffix = path.suffix.lower()
    mime = {
        ".jpg": "image/jpeg",
        ".jpeg": "image/jpeg",
        ".png": "image/png",
        ".gif": "image/gif",
        ".webp": "image/webp",
    }.get(suffix, "application/octet-stream")
    data = base64.b64encode(path.read_bytes()).decode("ascii")
    return f"data:{mime};base64,{data}"


def main() -> None:
    uploaded = client.files.create(file=IMAGE_PATH, purpose="vision")
    print("uploaded file_id:", uploaded.id)

    chat_completion = client.chat.completions.create(
        model=MODEL,
        messages=[
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": PROMPT},
                    {"type": "image_url", "image_url": {"url": to_data_url(IMAGE_PATH)}},
                ],
            }
        ],
    )
    print("chat.completions:", chat_completion.choices[0].message.content)

    response = client.responses.create(
        model=MODEL,
        input=[
            {
                "type": "message",
                "role": "user",
                "content": [
                    {"type": "input_text", "text": PROMPT},
                    {"type": "input_image", "file_id": uploaded.id},
                ],
            }
        ],
    )
    print("responses:", response.output_text)

    response_from_data_url = client.responses.create(
        model=MODEL,
        input=[
            {
                "type": "message",
                "role": "user",
                "content": [
                    {"type": "input_text", "text": PROMPT},
                    {"type": "input_image", "image_url": to_data_url(IMAGE_PATH), "detail": "auto"},
                ],
            }
        ],
    )
    print("responses data URL:", response_from_data_url.output_text)


if __name__ == "__main__":
    main()
