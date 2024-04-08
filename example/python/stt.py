import os

import openai

PROMPT = "An eco-friendly computer from the 90s in the style of vaporwave"

openai.api_key = "<Your API Key>"
openai.base_url = "http://localhost:8080/platform/v1/"

audio_file = open("/Users/leowu/Desktop/01 零時起哄.mp3", "rb")

transcriptions = openai.audio.transcriptions.create(
    model="whisper-1",
    file=audio_file,
    response_format="json"
)

print(transcriptions)
