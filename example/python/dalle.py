import os

import openai

PROMPT = "An eco-friendly computer from the 90s in the style of vaporwave"

openai.api_key = "<Your API Key>"
openai.base_url = "http://localhost:8080/platform/v1/"

response = openai.images.generate(
    prompt=PROMPT,
    n=1,
    size="256x256",
)

print(response.data[0].url)
