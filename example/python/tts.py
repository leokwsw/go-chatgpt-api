from pathlib import Path

import openai

PROMPT = "An eco-friendly computer from the 90s in the style of vaporwave"

openai.api_key = "<Your API Key>"
openai.base_url = "http://localhost:8080/platform/v1/"

speech_file_path = Path(__file__).parent / "speech.mp3"
response = openai.audio.speech.create(
    model="tts-1",
    voice="alloy",
    input="Hello, i am OpenAI ChatGPT!",
    response_format="mp3",
    speed=0.8
)

response.write_to_file(speech_file_path)