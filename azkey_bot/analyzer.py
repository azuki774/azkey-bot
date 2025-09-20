from .openrouter import analyze_with_ai


class NoteAnalyzer:
    def analyze(data):
        extracted_data = NoteAnalyzer.extract(data)
        ai_analysis_result = analyze_with_ai(extracted_data)
        return ai_analysis_result

    def extract(data):
        results = []
        for note in data:
            created_at = note.get("createdAt", "N/A")
            text = note.get("text", "")
            reaction_count = note.get("reactionCount", 0)

            formatted_line = f"{created_at}: {text} - ReactionCount={reaction_count}"
            results.append(formatted_line)

        return "\n".join(results)
