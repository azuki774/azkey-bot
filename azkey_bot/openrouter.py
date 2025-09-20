"""AI analysis using OpenRouter"""

import os
from openai import OpenAI


def analyze_with_ai(analysis_result: str) -> str:
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

    prompt = f"""
以下は日本のソーシャルメディア（Misskey）の投稿データです。
このデータを分析して、以下の観点から洞察を提供してください：

1. 投稿の傾向や特徴
2. 投稿時間の分析
3. リアクション数の分析
4. 内容の特徴や感情分析
5. その他の興味深い発見

データ：
{analysis_result}

日本語で分析結果を回答してください。"""

    try:
        completion = client.chat.completions.create(
            model="x-ai/grok-4-fast:free",
            messages=[
                {"role": "user", "content": prompt}
            ],
            max_tokens=4048,
            temperature=0.7
        )
        return completion.choices[0].message.content
    except Exception as e:
        raise Exception(f"OpenRouter API error: {e}")
