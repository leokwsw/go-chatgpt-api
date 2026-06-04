# AGENTS.md

## Project goal

Implement a reliable end-to-end image upload + image chat flow for the `/imitate` API.

The target behavior is:

1. `POST /imitate/v1/files` accepts a multipart image upload.
2. The response returns an OpenAI-style file object with `id`.
3. `POST /imitate/v1/chat/completions` can reference that uploaded image by `file_id`.
4. If the upstream model can see the image, it should extract the image text.
5. If the image does not reach the model context, the assistant must reply exactly: `无图片`.

## Scope

Focus on `/imitate` only unless a small shared helper change is required.

Prefer minimal, high-confidence changes.
Do not redesign unrelated modules.
Do not refactor for style only.

## Existing hints in this repo

- There is already `/imitate/v1/files` routing in the server.
- There are HAR parsing scripts in the repo for upload and conversation flows.
- Use existing HAR artifacts as the protocol ground truth.
- Prefer matching real ChatGPT upload flow rather than inventing a new protocol.

## Required output from Codex before coding

First produce:
1. current state summary
2. exact gap list
3. implementation plan
4. files to change
5. acceptance plan

Do not start editing until the plan is explicit.

## Constraints

- Keep API compatibility with current `/imitate/v1/chat/completions`.
- Do not commit secrets, tokens, cookies, or HAR files with private data.
- Do not remove existing chat functionality.
- Keep fallback behavior deterministic for acceptance.

## Done definition

The task is done only if all of the following are true:

1. an image can be uploaded through `/imitate/v1/files`
2. a later `/imitate/v1/chat/completions` request can reference that image
3. the repo contains a runnable end-to-end verification script
4. the script can distinguish:
   - image seen -> extracted text
   - image not seen -> exact output `无图片`
5. failure paths return controlled errors without panic

## Verification expectations

After code changes, run the smallest possible validation steps.
If full upstream validation is impossible due to missing credentials or HAR material, still:
- run compile/build validation
- add local regression coverage
- provide exact manual verification commands
- clearly mark what remains unverified