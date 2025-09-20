"""Next note generation using AI analysis"""

from .openrouter import post_prompt


class NextNoteAnalyzer:
    """Analyzer for generating next note based on past posting patterns"""

    def generate_next_note(data):
        """Generate next note based on past posting patterns

        Args:
            data: List of past notes

        Returns:
            Generated note text
        """
        extracted_data = NextNoteAnalyzer.extract(data)
        next_note = NextNoteAnalyzer.analyze_and_generate(extracted_data)
        return next_note

    def extract(data):
        results = []
        for note in data:
            created_at = note.get("createdAt", "N/A")
            text = note.get("text", "")
            reaction_count = note.get("reactionCount", 0)

            formatted_line = f"{created_at}: {text} - ReactionCount={reaction_count}"
            results.append(formatted_line)

        return "\n".join(results)

    def analyze_and_generate(extracted_data):
        prompt = f"""
以下は、あるユーザーの過去の投稿データです。
このユーザーの投稿パターン、話題の傾向、文体、投稿時間などを分析して、
そのユーザーらしい次の投稿を生成してください。

【分析観点】
- 投稿の文体やトーン
- よく使う単語や表現
- 話題の傾向
- 投稿時間のパターン
- リアクションを得やすい投稿の特徴

【生成ルール】
- 140文字以内で簡潔に
- そのユーザーらしい自然な投稿
- 実在の人物や個人情報に言及しない
- 不適切な内容は含めない

【過去の投稿データ】
{extracted_data}

【出力】
次の投稿として適切なテキストのみを出力してください。説明や解説は不要です。
"""
        return post_prompt(prompt, "")
