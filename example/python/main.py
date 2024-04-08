import openai

# openai.api_key = "<Your JWT Token>"
openai.api_key = "python"
openai.base_url = "http://localhost:8080/imitate/v1/"

while True:
    text = input("Please enter a question：")
    response = openai.chat.completions.create(
        model='gpt-3.5-turbo',
        messages=[
            {'role': 'user', 'content': text},
        ],
        stream=True
    )

    for chunk in response:
        if chunk.choices[0].delta.content is not None:
            print(chunk.choices[0].delta.content, end="", flush=True)
    print("\n")
