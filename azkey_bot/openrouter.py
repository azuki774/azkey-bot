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
サーバ名は「あずきインターネット」です。
このユーザが、会社の従業員であるように見立てて、評価してください。
形式は格式張った、評価書であるように記載してください。
【注意】どのような内容のノートすると、点数が増減されるかは分かるような出力はしないでください。

このデータを分析して、以下のフォーマットで出力してください：

評価日時： xxxx年xx月xx日
評価対象者： xxxxxxxxxxxx
評価期間：投稿データに基づく過去履歴(xxxx年xx月xx日 - xxxx年xx月xx日)
評価者：あずきインターネット人事部

1. 総合評価
[評価S | 評価A | 評価B | 評価C | 評価D]

2. 投稿内容の紹介
投稿の内容や話題、傾向などについて、3,4文程度で紹介してください。

3. お気に入り投稿データの紹介
適当に盛り上がりそうなノートを1,2個紹介してください。

4. 投稿内容評価
10段階評価(1~10点)
「あずきインターネット」のユーザは、技術的な内容もしくは、その人が好きなコンテンツを紹介することなどが好まれます。
また、多様な話題を行うことも点数が増加する要因です。
それ以外の内容について減点はしませんが、下ネタ等の程度によっては減点される可能性があります。

5. 投稿頻度評価
10段階評価(1~10点)
投稿頻度を評価します。
ここで提供されるデータは、件数を指定されて与えられるため、件数の絶対数は考慮に含みません。
あくまで、n件の投稿を何日で行っているかということについて評価してください。
すなわち、少ない日数で投稿を重ねている方がたくさん評価されます。

6. 取得リアクション数についての寸評
10段階評価(1~10点)
多ければ多いほど評価されます。



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
