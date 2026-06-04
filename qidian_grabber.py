#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
起点小说章节自动抓取 + OCR 脚本（Mac / Chrome 版）

支持三种 OCR 模式（OCR_MODE 配置项）：
  "paddle"  — 纯 PaddleOCR（原始逻辑）
  "llm"     — 纯 LLM OCR：批量上传所有分段图，一次性提取全文
  "hybrid"  — 先 Paddle 得初稿，再将初稿 + 分段图一并发给 LLM 校对，
               LLM 返回替换 / 插入修正方案，程序应用修正后保存

流程（截图部分三种模式相同）：
  1. 读取 urls.txt（每行一个章节 URL）
  2. Chrome 新标签 + GoFullPage 快捷键 Alt+Shift+P 整页截图
  3. 匹配文件名，移动到 output/<book_id>/images/<chapter_id>.png
  4. 裁侧边栏 → 智能切段 → OCR（按模式） → 按锚点裁正文 → 保存

依赖：
    pip install pyautogui pillow pyperclip numpy openai
    pip install paddlepaddle paddleocr   # paddle / hybrid 模式需要
"""

import base64
import io
import json
import logging
import os
import random
import re
import shutil
import sys
import time
from pathlib import Path

import numpy as np
import pyautogui
import pyperclip
from PIL import Image


# =====================================================================
# 配 置 区 ── 所有可调参数集中于此
# =====================================================================

# OCR 模式："paddle" | "llm" | "hybrid"
OCR_MODE = "hybrid"

# ── 路径 ──
URL_FILE          = "/Users/wenhuicheng/Library/CloudStorage/OneDrive-Personal/code/course/ubuntu/玄鉴仙族/玄鉴仙族url.txt"
OUTPUT_DIR        = Path("/Users/wenhuicheng/Library/CloudStorage/OneDrive-Personal/code/course/ubuntu/玄鉴仙族/out")
DOWNLOAD_DIR      = Path.home() / "Downloads"
FAILED_FILE       = Path("/Users/wenhuicheng/Library/CloudStorage/OneDrive-Personal/code/course/ubuntu/玄鉴仙族/failed.txt")
NEEDS_REVIEW_FILE = Path("/Users/wenhuicheng/Library/CloudStorage/OneDrive-Personal/code/course/ubuntu/玄鉴仙族/needs_review.txt")

# ── 内容 ──
AUTHOR_NAME        = "季越人"    # 用于匹配章节末尾锚点
CROP_LEFT_PX       = 547         # 裁剪左边界（像素）
CROP_RIGHT_PX      = 1347        # 裁剪右边界（像素）
MAX_SEGMENT_HEIGHT = 4000        # 每段最大高度（像素）

# ── 浏览器 / 截图 ──
SLEEP_MIN        = 4             # 章节间随机休息下限（秒）
SLEEP_MAX        = 8             # 章节间随机休息上限（秒）
SCREENSHOT_WAIT  = 6             # GoFullPage 生成截图等待时间（秒）
DOWNLOAD_TIMEOUT = 30            # 等待下载文件出现超时（秒）


base_url= "http://127.0.0.1:8080/v1/"
gpt4_model='claude-sonnet-4-6-thinking' #'claude-3-7-sonnet-20250219' #'claude-3-5-haiku-20241022'  #
acc=os.getenv("QIDIAN_LLM_API_KEY") or os.getenv("OPENAI_API_KEY") or ""


#base_url= "http://127.0.0.1:8000/v1/"
#acc=os.getenv("GOOGLE_API_KEY", "")
#base_url="https://generativelanguage.googleapis.com/v1beta/openai/"

#gpt4_model="gemini-3-flash-preview"

# ── LLM（llm / hybrid 模式使用）──
LLM_API_KEY      = acc
LLM_BASE_URL     = base_url
LLM_MODEL        = gpt4_model
LLM_MAX_TOKENS   = 1024*32
LLM_TEMPERATURE  = 0.2
LLM_MAX_RETRIES  = 3
LLM_JPEG_QUALITY = 90


# =====================================================================
# 常 量
# =====================================================================

URL_PATTERN      = re.compile(r"/chapter/(\d+)/(\d+)")
START_ANCHOR_RE  = re.compile(r"本章含\d+条段评")
DATETIME_LINE_RE = re.compile(
    r"\d{4}\s*年\s*\d{1,2}\s*月\s*\d{1,2}\s*日\s*\d{1,2}\s*:\s*\d{2}"
)
GOFULLPAGE_PATTERN = re.compile(
    r"screencapture-qidian-chapter-(\d+)-(\d+)-[\d\-_]+\.png"
)

CORRECTIONS_START = "<corrections>"
CORRECTIONS_END   = "</corrections>"


# =====================================================================
# 日 志
# =====================================================================

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    handlers=[
        logging.StreamHandler(sys.stdout),
        logging.FileHandler("run.log", encoding="utf-8"),
    ],
)
log = logging.getLogger("qidian")


# =====================================================================
# 通 用 工 具
# =====================================================================

def parse_ids(url: str) -> tuple:
    m = URL_PATTERN.search(url)
    if not m:
        return None, None
    return m.group(1), m.group(2)


def load_urls() -> list:
    if not Path(URL_FILE).exists():
        log.error(f"URL 文件不存在：{URL_FILE}")
        sys.exit(1)
    with open(URL_FILE, "r", encoding="utf-8") as f:
        return [l.strip() for l in f if l.strip() and not l.startswith("#")]


def get_paths(book_id: str, chapter_id: str):
    base = OUTPUT_DIR / book_id
    return (
        base / "images" / f"{chapter_id}.png",
        base / "texts"  / f"{chapter_id}.txt",
        base / "texts"  / f"{chapter_id}.raw.txt",
    )


def ensure_dirs(book_id: str):
    base = OUTPUT_DIR / book_id
    (base / "images").mkdir(parents=True, exist_ok=True)
    (base / "texts").mkdir(parents=True, exist_ok=True)


def record_line(path: Path, url: str, reason: str):
    with open(path, "a", encoding="utf-8") as f:
        f.write(f"{url}\t{reason}\n")


# =====================================================================
# 浏 览 器 操 作
# =====================================================================

from pynput.keyboard import Controller as KBController, Key
_kb = KBController()


def open_new_tab_with_url(url: str):
    pyautogui.hotkey("command", "t")
    time.sleep(0.6)
    pyperclip.copy(url)
    pyautogui.hotkey("command", "v")
    time.sleep(0.3)
    pyautogui.press("enter")


def close_tab():
    pyautogui.hotkey("command", "w")
    time.sleep(0.5)


def capture_full_page():
    _kb.press(Key.alt)
    _kb.press(Key.shift)
    time.sleep(0.05)
    _kb.press('p')
    time.sleep(0.05)
    _kb.release('p')
    _kb.release(Key.shift)
    _kb.release(Key.alt)
    time.sleep(SCREENSHOT_WAIT)


# =====================================================================
# 文 件 处 理
# =====================================================================

def find_chapter_download(book_id: str, chapter_id: str,
                          after_time: float, timeout: int = DOWNLOAD_TIMEOUT):
    deadline = time.time() + timeout
    target   = f"chapter-{book_id}-{chapter_id}-"
    while time.time() < deadline:
        for p in DOWNLOAD_DIR.glob("screencapture-qidian-chapter-*.png"):
            if target in p.name and p.stat().st_mtime > after_time:
                s1 = p.stat().st_size
                time.sleep(0.6)
                if p.exists() and p.stat().st_size == s1 and s1 > 0:
                    return p
        time.sleep(0.5)
    return None


def crop_sidebar(img_path: Path) -> Image.Image:
    img = Image.open(img_path)
    return img.crop((CROP_LEFT_PX, 0, CROP_RIGHT_PX, img.height))


# =====================================================================
# 图 片 切 割
# =====================================================================

def split_image_smart(img: Image.Image) -> list:
    """智能切割长图，沿空白行切割，返回 PIL Image 列表。"""
    if img.mode in ("RGBA", "P"):
        img = img.convert("RGB")
    arr = np.array(img)

    border = np.concatenate([arr[0,:], arr[-1,:], arr[:,0], arr[:,-1]])
    q      = border // 10 * 10
    unique, counts = np.unique(q.reshape(-1, 3), axis=0, return_counts=True)
    bg     = unique[counts.argmax()].astype(float)
    log.info(f"  背景色: RGB{tuple(bg.astype(int))}")

    diff     = np.abs(arr.astype(float) - bg).mean(axis=(1, 2))
    is_blank = diff < 10

    segments, start, h = [], 0, arr.shape[0]
    while start < h:
        end = min(start + MAX_SEGMENT_HEIGHT, h)
        if end < h:
            cut = None
            for i in range(end, max(start, end - MAX_SEGMENT_HEIGHT // 3), -1):
                if is_blank[i]:
                    gs, ge = i, i
                    while gs > start and is_blank[gs - 1]: gs -= 1
                    while ge < h - 1 and is_blank[ge + 1]: ge += 1
                    if ge - gs >= 10:
                        cut = (gs + ge) // 2
                        break
            end = cut if cut else end
        segments.append(img.crop((0, start, img.width, end)))
        log.info(f"  切段 {len(segments)}: {start}~{end}px")
        start = end
    return segments


def make_named_segments(cropped: Image.Image) -> list:
    """切割后给每段赋文件名，返回 [(name, img), ...]。"""
    return [(f"seg_{i:03d}.png", seg)
            for i, seg in enumerate(split_image_smart(cropped), 1)]


# =====================================================================
# PaddleOCR
# =====================================================================

_ocr_instance = None

def get_ocr():
    global _ocr_instance
    if _ocr_instance is None:
        from paddleocr import PaddleOCR
        log.info("初始化 PaddleOCR...")
        _ocr_instance = PaddleOCR(
            use_doc_orientation_classify=False,
            use_doc_unwarping=False,
            use_textline_orientation=False,
            lang="ch",
        )
    return _ocr_instance


def ocr_single(pil_img: Image.Image) -> list:
    """对单张图跑 Paddle，返回文本行列表。"""
    arr     = np.array(pil_img.convert("RGB"))
    results = get_ocr().predict(arr)
    if not results:
        return []

    items = []
    for res in results:
        data = res.json.get('res', {})
        for text, box in zip(data.get('rec_texts', []), data.get('rec_boxes', [])):
            if text and text.strip():
                items.append((int(box[1]), int(box[0]), text.strip()))

    if not items:
        return []

    items.sort(key=lambda t: t[0])
    lines, cur, y_tol = [], [items[0]], 15
    for it in items[1:]:
        avg_y = sum(x[0] for x in cur) / len(cur)
        if abs(it[0] - avg_y) <= y_tol:
            cur.append(it)
        else:
            cur.sort(key=lambda e: e[1])
            lines.append("".join(e[2] for e in cur))
            cur = [it]
    cur.sort(key=lambda e: e[1])
    lines.append("".join(e[2] for e in cur))
    return lines


def paddle_ocr_segments(named_segments: list) -> list:
    """对所有分段依次跑 Paddle，合并返回行列表。"""
    all_lines = []
    for idx, (name, seg) in enumerate(named_segments, 1):
        log.info(f"  Paddle {idx}/{len(named_segments)}：{name}")
        seg_lines = ocr_single(seg)
        log.info(f"    → {len(seg_lines)} 行")
        all_lines.extend(seg_lines)
    return all_lines


# =====================================================================
# LLM 公 共 工 具
# =====================================================================

_llm_client = None

def get_llm_client():
    global _llm_client
    if _llm_client is None:
        if not LLM_API_KEY:
            raise RuntimeError("Set QIDIAN_LLM_API_KEY or OPENAI_API_KEY before using llm/hybrid OCR mode.")
        from openai import OpenAI
        _llm_client = OpenAI(api_key=LLM_API_KEY, base_url=LLM_BASE_URL)
    return _llm_client


def pil_to_b64(img: Image.Image) -> str:
    buf = io.BytesIO()
    img.save(buf, format="JPEG", quality=LLM_JPEG_QUALITY)
    return base64.b64encode(buf.getvalue()).decode()


def build_images_content(named_segments: list) -> list:
    """构造多图消息块：每张图前附 [filename] 文本标注。"""
    content = []
    for name, img in named_segments:
        content.append({"type": "text", "text": f"[{name}]"})
        content.append({
            "type": "image_url",
            "image_url": {"url": f"data:image/jpeg;base64,{pil_to_b64(img)}"},
        })
    return content


# =====================================================================
# 模 式 二：纯 LLM OCR
# =====================================================================

def _make_llm_ocr_prompt(filenames: list) -> str:
    """动态生成 prompt，将每个文件名的标签模板直接嵌入，强迫模型逐一填写。"""
    blocks = "\n\n".join(
        f"<{n}>\n（此处填 {n} 的提取文字）\n</{n}>" for n in filenames
    )
    return (
        f"你是专业的图片文字提取助手。\n"
        f"用户发给你 {len(filenames)} 张图片，是同一长图按顺序切割的各段。\n\n"
        f"请按下方格式，为每一张图片单独输出提取结果，一张都不能遗漏：\n\n"
        f"{blocks}\n\n"
        f"规则：\n"
        f"- 必须按顺序输出以上全部 {len(filenames)} 个标签块\n"
        f"- 忠实还原图中所有可见文字，不添加、不修改、不翻译\n"
        f"- 保留原文换行；若某张图无文字，对应标签内留空\n"
        f"- 标签外不输出任何解释"
    )


def _extract_segment_texts(reply: str, filenames: list):
    """从回复中按文件名提取各段文本；有任何标签缺失则返回 None。"""
    data, missing = {}, []
    for name in filenames:
        s = reply.find(f"<{name}>")
        e = reply.find(f"</{name}>")
        if s == -1 or e == -1 or e <= s:
            missing.append(name)
        else:
            data[name] = reply[s + len(name) + 2 : e].strip()
    if missing:
        log.warning(f"  LLM OCR 标签缺失：{missing}")
        return None
    return data


def llm_ocr_segments(named_segments: list) -> list:
    """
    一次性上传所有分段图，LLM 逐段提取文字。
    返回合并后的文本行列表；失败返回空列表。
    """
    client    = get_llm_client()
    filenames = [n for n, _ in named_segments]
    system    = _make_llm_ocr_prompt(filenames)
    user_content = (
        [{"type": "text",
          "text": f"以下是长图切割后的 {len(named_segments)} 张图片，请逐一提取文字。"}]
        + build_images_content(named_segments)
    )

    for attempt in range(1, LLM_MAX_RETRIES + 1):
        log.info(f"  LLM OCR 第 {attempt} 次（{len(named_segments)} 张图）...")
        resp = get_llm_client().chat.completions.create(
            model=LLM_MODEL, max_tokens=LLM_MAX_TOKENS,
            temperature=LLM_TEMPERATURE,
            messages=[{"role": "system", "content": system},
                      {"role": "user",   "content": user_content}],
        )
        reply = resp.choices[0].message.content
        print(reply)
        data  = _extract_segment_texts(reply, filenames)
        if data is None:
            log.warning(f"  重试（{attempt}/{LLM_MAX_RETRIES}）...")
            continue
        log.info("  ✓ LLM OCR 完成")
        all_lines = []
        for name in filenames:
            all_lines.extend(data[name].splitlines())
        return all_lines

    log.error(f"  LLM OCR {LLM_MAX_RETRIES} 次均失败")
    return []


# =====================================================================
# 模 式 三：Paddle 初 稿 + LLM 校 对
# =====================================================================

_REVIEW_SYSTEM = (
    "你是专业的中文文字校对助手。\n"
    "用户提供：\n"
    "  ① 通过 OCR 软件识别的中文文本初稿（可能含识别错误或遗漏）\n"
    "  ② 对应的原始图片（已分段，每段标注了文件名）\n\n"
    "你的任务：\n"
    "  1. 认真对比每张图片与 OCR 初稿，找出所有错误和遗漏\n"
    "  2. 先用自然语言写出你的逐段分析（写在 marker 外面）\n"
    "  3. 再将所有修正方案以 JSON 数组放入以下 marker 之间：\n\n"
    f"{CORRECTIONS_START}\n"
    "[修正数组]\n"
    f"{CORRECTIONS_END}\n\n"
    "JSON 数组元素有两种类型：\n\n"
    "【替换错误字词】\n"
    '{"type":"replace","wrong":"OCR中错误的词或短语","correct":"正确的词或短语",'
    '"context":"包含该错误的完整行，必须与OCR初稿某一行完全一致"}\n\n'
    "【补充遗漏内容】\n"
    '{"type":"insert","content":"需要补充的文字",'
    '"after":"插入位置前方的完整行，必须与OCR初稿某一行完全一致；若插在开头则填空字符串",'
    '"note":"简要说明为何有此遗漏"}\n\n'
    "注意事项：\n"
    "- context / after 必须与 OCR 初稿中某一行逐字相同，程序将用它定位行号\n"
    "- wrong 必须完整出现在 context 对应行中\n"
    "- 若 OCR 初稿完全正确，返回空数组 []\n"
    "- 只修正实际错误，不修改文风或用词"
)


def _parse_corrections(reply: str):
    """从回复中提取并解析修正 JSON 数组；失败返回 None。"""
    s = reply.find(CORRECTIONS_START)
    e = reply.find(CORRECTIONS_END)
    if s == -1 or e == -1 or e <= s:
        log.warning("  未找到 corrections marker")
        return None
    raw = reply[s + len(CORRECTIONS_START):e].strip()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as ex:
        log.warning(f"  JSON 解析失败：{ex}")
        return None
    if not isinstance(data, list):
        log.warning("  corrections 不是数组")
        return None
    return data


def _validate_corrections(corrections: list, ocr_text: str) -> tuple:
    """
    校验每条修正的结构与可定位性。
    返回 (通过: bool, 错误消息列表: list[str])。
    """
    errors = []
    for i, c in enumerate(corrections):
        t = c.get("type")
        if t == "replace":
            for f in ("wrong", "correct", "context"):
                if f not in c:
                    errors.append(f"[{i}] replace 缺少字段 '{f}'")
            if "context" in c and c["context"] not in ocr_text:
                errors.append(f"[{i}] context 未在OCR文本中找到：{c['context'][:40]!r}")
            elif "wrong" in c and "context" in c and c["wrong"] not in c["context"]:
                errors.append(f"[{i}] wrong 不在 context 中：{c['wrong']!r}")
        elif t == "insert":
            for f in ("content", "after", "note"):
                if f not in c:
                    errors.append(f"[{i}] insert 缺少字段 '{f}'")
            if "after" in c and c["after"] != "" and c["after"] not in ocr_text:
                errors.append(f"[{i}] after 未在OCR文本中找到：{c['after'][:40]!r}")
        else:
            errors.append(f"[{i}] 未知 type：{t!r}")
    return len(errors) == 0, errors


def _apply_corrections(ocr_text: str, corrections: list) -> str:
    """
    将 LLM 给出的修正方案逐条应用到 OCR 文本，返回修正后的完整文本。

    replace：在 context 所在行内将 wrong 替换为 correct；
             找不到精确行时降级为全局替换并记录警告。
    insert ：在 after 所在行之后插入 content；
             after 为空字符串时插入到文本开头。
    """
    lines = ocr_text.split("\n")

    for c in corrections:
        if c["type"] == "replace":
            wrong, correct, context = c["wrong"], c["correct"], c["context"]
            replaced = False
            for i, line in enumerate(lines):
                if line == context:
                    lines[i] = line.replace(wrong, correct)
                    replaced = True
                    log.info(f"  replace [{i}]：{wrong!r} → {correct!r}")
                    break
            if not replaced:
                log.warning(f"  replace 未精确定位，全局替换：{wrong!r} → {correct!r}")
                lines = [l.replace(wrong, correct) for l in lines]

        elif c["type"] == "insert":
            content, after = c["content"], c["after"]
            if after == "":
                lines.insert(0, content)
                log.info(f"  insert 开头：{content[:30]!r}")
            else:
                inserted = False
                for i, line in enumerate(lines):
                    if line == after:
                        lines.insert(i + 1, content)
                        log.info(f"  insert 行 {i+1} 后：{content[:30]!r}")
                        inserted = True
                        break
                if not inserted:
                    log.warning(f"  insert 未找到 after，追加末尾：{after[:40]!r}")
                    lines.append(content)

    return "\n".join(lines)


def llm_review_and_correct(ocr_text: str, named_segments: list) -> str:
    """
    将 OCR 初稿 + 所有分段图发给 LLM 校对。
    校验通过后应用修正，返回修正后文本；全部失败则返回原始 ocr_text。
    """
    user_content = (
        [{"type": "text", "text": "【OCR 初稿文本】\n\n" + ocr_text},
         {"type": "text", "text": f"\n\n【原始图片，共 {len(named_segments)} 段】"}]
        + build_images_content(named_segments)
    )

    for attempt in range(1, LLM_MAX_RETRIES + 1):
        log.info(f"  LLM 校对第 {attempt} 次（{len(named_segments)} 张图）...")
        resp = get_llm_client().chat.completions.create(
            model=LLM_MODEL, max_tokens=LLM_MAX_TOKENS,
            temperature=LLM_TEMPERATURE,
            messages=[{"role": "system", "content": _REVIEW_SYSTEM},
                      {"role": "user",   "content": user_content}],
        )
        reply = resp.choices[0].message.content
        print(reply)

        # 打印 LLM 思考分析（marker 之前的部分，截取前 500 字）
        thinking_end = reply.find(CORRECTIONS_START)
        if thinking_end > 0:
            preview = reply[:thinking_end].strip()[:500]
            log.info(f"  LLM 分析（节选）：\n{preview}")

        corrections = _parse_corrections(reply)
        if corrections is None:
            log.warning(f"  解析失败，重试（{attempt}/{LLM_MAX_RETRIES}）...")
            continue

        ok, errs = _validate_corrections(corrections, ocr_text)
        if not ok:
            log.warning(f"  校验失败（{attempt}/{LLM_MAX_RETRIES}）：")
            for err in errs:
                log.warning(f"    {err}")
            continue

        if not corrections:
            log.info("  ✓ LLM 认为初稿无误，无需修正")
            return ocr_text

        log.info(f"  ✓ 校验通过，共 {len(corrections)} 条修正，开始应用...")
        return _apply_corrections(ocr_text, corrections)

    log.error(f"  LLM 校对 {LLM_MAX_RETRIES} 次均失败，保留 Paddle 初稿")
    return ocr_text


# =====================================================================
# 正 文 裁 剪（三种模式共用）
# =====================================================================

def extract_body(lines: list, author: str) -> tuple:
    """过滤日期行、按锚点截取正文。返回 (body | None, full_text)。"""
    lines     = [l for l in lines if not DATETIME_LINE_RE.search(l)]
    full_text = "\n".join(lines)

    start_idx = next(
        (i + 1 for i, l in enumerate(lines) if START_ANCHOR_RE.search(l)), None
    )
    end_re  = re.compile(re.escape(author) + r"\s*[·•．\.]?\s*作家说")
    end_idx = next(
        (i for i, l in enumerate(lines) if end_re.search(l)), None
    )

    if start_idx is None or end_idx is None or end_idx <= start_idx:
        return None, full_text
    return "\n".join(lines[start_idx:end_idx]).strip(), full_text


# =====================================================================
# OCR 调 度（按模式分支）
# =====================================================================

def run_ocr(cropped: Image.Image, chapter_id: str) -> tuple:
    """
    按 OCR_MODE 执行对应流程。
    返回 (body | None, full_text)。
    """
    named_segments = make_named_segments(cropped)
    log.info(f"  共切割为 {len(named_segments)} 段")

    if OCR_MODE == "paddle":
        log.info("  模式：PaddleOCR")
        lines = paddle_ocr_segments(named_segments)
        if not lines:
            return None, ""
        return extract_body(lines, AUTHOR_NAME)

    elif OCR_MODE == "llm":
        log.info("  模式：LLM OCR")
        lines = llm_ocr_segments(named_segments)
        if not lines:
            return None, ""
        return extract_body(lines, AUTHOR_NAME)

    elif OCR_MODE == "hybrid":
        log.info("  模式：Paddle 初稿 + LLM 校对")
        lines = paddle_ocr_segments(named_segments)
        if not lines:
            return None, ""
        ocr_text = "\n".join(lines)
        log.info(f"  Paddle 初稿：{len(lines)} 行，{len(ocr_text)} 字")
        corrected = llm_review_and_correct(ocr_text, named_segments)
        return extract_body(corrected.splitlines(), AUTHOR_NAME)

    else:
        log.error(f"未知 OCR_MODE：{OCR_MODE!r}，支持 paddle / llm / hybrid")
        return None, ""


# =====================================================================
# 主 流 程
# =====================================================================

def process_one(url: str):
    book_id, chapter_id = parse_ids(url)
    if not book_id:
        log.error(f"URL 格式异常：{url}")
        record_line(FAILED_FILE, url, "bad_url")
        return

    ensure_dirs(book_id)
    img_path, txt_path, raw_path = get_paths(book_id, chapter_id)

    # ── 已有最终正文，直接跳过 ──
    if txt_path.exists():
        log.info(f"[{chapter_id}] 已有正文，跳过")
        return

    log.info(f"[{chapter_id}] 开始：{url}")

    # ── 有 raw.txt → 跳过截图/OCR，直接重试锚点 ──
    if raw_path.exists():
        log.info(f"[{chapter_id}] 发现 raw.txt，重试锚点提取...")
        raw_text = raw_path.read_text(encoding="utf-8")
        body, _ = extract_body(raw_text.splitlines(), AUTHOR_NAME)
        if body:
            txt_path.write_text(body, encoding="utf-8")
            raw_path.unlink()          # raw 已用完，删掉
            log.info(f"[{chapter_id}] ✅ 锚点提取成功（{len(body)} 字），raw.txt 已删除")
        else:
            log.warning(f"[{chapter_id}] ⚠ 锚点仍未匹配，保留 raw.txt 待人工确认")
        return                         # 无论成功与否，本章处理完毕

    # ── 截图 ──
    if not img_path.exists():
        open_new_tab_with_url(url)
        time.sleep(2)
        before = time.time()
        capture_full_page()
        latest = find_chapter_download(book_id, chapter_id, before)
        if not latest:
            log.error(f"[{chapter_id}] 未发现截图文件")
            close_tab()
            record_line(FAILED_FILE, url, "screenshot_not_found")
            return
        shutil.move(str(latest), str(img_path))
        log.info(f"[{chapter_id}] 截图已存：{img_path}")
        close_tab()
    else:
        log.info(f"[{chapter_id}] 图已存在，跳过截图")

    # ── OCR ──
    if not txt_path.exists() and not raw_path.exists():
        log.info(f"[{chapter_id}] OCR（模式：{OCR_MODE}）...")
        cropped = crop_sidebar(img_path)
        cropped.save(img_path.with_suffix(".cropped.png"))

        body, full_text = run_ocr(cropped, chapter_id)

        if body is None and not full_text:
            log.warning(f"[{chapter_id}] OCR 无结果")
            record_line(FAILED_FILE, url, "ocr_empty")
            return

        if body:
            txt_path.write_text(body, encoding="utf-8")
            log.info(f"[{chapter_id}] ✅ 正文已保存（{len(body)} 字）")
        else:
            raw_path.write_text(full_text, encoding="utf-8")
            log.warning(f"[{chapter_id}] ⚠ 未匹配锚点，保存为 raw.txt")
            record_line(NEEDS_REVIEW_FILE, url, "anchor_not_matched")


def preflight():
    print("=" * 64)
    print(f" 起点章节抓取脚本  [OCR 模式：{OCR_MODE}]")
    print("=" * 64)
    print(f" URL 列表    : {URL_FILE}")
    print(f" 输出目录    : {OUTPUT_DIR.resolve()}")
    print(f" 下载目录    : {DOWNLOAD_DIR}")
    print(f" 作者名      : 「{AUTHOR_NAME}」")
    print(f" 切段阈值    : {MAX_SEGMENT_HEIGHT}px")
    if OCR_MODE in ("llm", "hybrid"):
        print(f" LLM 模型    : {LLM_MODEL}")
        print(f" LLM 地址    : {LLM_BASE_URL}")
    print("-" * 64)
    print(" 运行前请确认：")
    print("   1) 已在 Chrome 登录起点账号")
    print("   2) Chrome 窗口在最前、未最小化")
    print("   3) 终端已获「辅助功能」权限")
    print("   4) GoFullPage 已安装并开启「自动保存」，快捷键 Alt+Shift+P")
    print("=" * 64)
    try:
        input("准备好后按回车继续（5 秒内切到 Chrome） ...")
    except EOFError:
        pass
    for i in range(5, 0, -1):
        print(f"  倒计时 {i} ...")
        time.sleep(1)


def main():
    urls = load_urls()
    log.info(f"共 {len(urls)} 个章节，OCR 模式：{OCR_MODE}")
    preflight()

    if OCR_MODE in ("paddle", "hybrid"):
        get_ocr()

    for i, url in enumerate(urls, 1):
        log.info(f"========== [{i}/{len(urls)}] ==========")
        try:
            process_one(url)
        except pyautogui.FailSafeException:
            log.error("触发 fail-safe（鼠标移到屏幕左上角），终止")
            break
        except KeyboardInterrupt:
            log.warning("收到 Ctrl+C，终止")
            break
        except Exception as e:
            log.exception(f"处理 {url} 异常：{e}")
            record_line(FAILED_FILE, url, f"exception:{type(e).__name__}")
            try:
                close_tab()
            except Exception:
                pass

        if i < len(urls):
            s = random.uniform(SLEEP_MIN, SLEEP_MAX)
            log.info(f"休息 {s:.1f}s ...")
            time.sleep(s)

    log.info("全部完成")


if __name__ == "__main__":
    pyautogui.FAILSAFE = True
    main()
