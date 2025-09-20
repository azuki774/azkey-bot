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
以下は、日本のソーシャルメディア（Misskey）の投稿データです。
サーバ名は「あずきインターネット」です。社長は @azuki です。
このユーザが、会社の従業員であるように見立てて、評価してください。
形式は格式張った、評価書であるように記載してください。

□評価の付け方
- 総合評価: 全体評価でいずれかを選択してください。それ以上のコメントは不要です。
- 投稿内容の紹介: 投稿の内容や話題、傾向などについて、3,4文程度で紹介してください。
- お気に入り投稿データの紹介: 適当に盛り上がりそうなノートを1,2個紹介してください
- 投稿内容評価: 「あずきインターネット」のユーザは、技術的な内容もしくは、その人が好きなコンテンツを紹介することなどが好まれます。
  また、多様な話題を行うことも点数が増加する要因です。それ以外の内容について減点はしませんが、下ネタ等の程度によっては減点される可能性があります。
- 投稿頻度評価: 投稿頻度を評価します。
ここで提供されるデータは、件数を指定されて与えられるため、件数の絶対数は考慮に含みません。
あくまで、n件の投稿を何日で行っているかということについて評価してください。
すなわち、少ない日数で投稿を重ねている方がたくさん評価されます。
- 取得リアクション数についての評価: 多ければ多いほど評価されます。

このデータを分析して、以下のフォーマットで出力してください。

-------------------------------
# あずきインターネット評価

- 評価対象者：
- 評価期間：投稿データに基づく過去履歴(xxxx年xx月xx日 - xxxx年xx月xx日)
- 評価者：あずきインターネット人事部

## 1. 総合評価
- 「評価S or 評価A or 評価B or 評価C or 評価D」のいずれか

## 2. 投稿内容の紹介

## 3. お気に入り投稿データの紹介

## 4. 投稿内容評価
- 10段階評価(1~10点)
- 「評価の付け方」に従い評価してください

## 5. 投稿頻度評価
- 10段階評価(1~10点)
- 「評価の付け方」に従い評価してください

## 6. 取得リアクション数についての評価
- 10段階評価(1~10点)
- 「評価の付け方」に従い評価してください

-------------------------------

データ：
{analysis_result}

"""

    try:
        completion = client.chat.completions.create(
            extra_headers={
                "HTTP-Referer": "https://github.com/azuki774/azkey-bot",
                "X-Title": "azkey-bot",
            },
            model="google/gemini-2.5-flash-lite",
            messages=[
                {"role": "system", "content": "日本語で分析結果を回答してください。あなたは「あずきインターネット」の人事部担当者です。与えられたフォーマット通りに従業員評価書を作成してください。どのような内容のノートすると、点数が増減されるかは分かるような出力はしないでください。"},
                {"role": "user", "content": prompt}
            ],
            max_tokens=48000,
            temperature=0.3
        )
        return completion.choices[0].message.content
    except Exception as e:
        raise Exception(f"OpenRouter API error: {e}")
