import os
from openai import OpenAI


def post_prompt(prompt, system_prompt):
    """Analyze data using OpenRouter AI

    Args:
        analysis_result: Text data to analyze

    Returns:
        AI analysis result

    Raises:
        ValueError: If OpenRouter API key is not set
        Exception: If API request fails
    """
    api_key = os.getenv("OPENROUTER_API_KEY")
    if not api_key:
        raise ValueError("Environment variable 'OPENROUTER_API_KEY' is not set")

    client = OpenAI(
        base_url="https://openrouter.ai/api/v1",
        api_key=api_key,
    )

    try:
        completion = client.chat.completions.create(
            extra_headers={
                "HTTP-Referer": "https://github.com/azuki774/azkey-bot",
                "X-Title": "azkey-bot",
            },
            model="x-ai/grok-4-fast:free",
            messages=[
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": prompt}
            ],
            max_tokens=48000,
            temperature=0.3
        )
        return completion.choices[0].message.content
    except Exception as e:
        raise Exception(f"OpenRouter API error: {e}")
