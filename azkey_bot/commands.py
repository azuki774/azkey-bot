import click
from .analyzer import NoteAnalyzer
from .misskey import get_all_notes_paginated, create_note


@click.command("status")
def status_command():
    click.echo("azkey-bot is running!")


@click.command("analyze")
@click.option("--user-id", default="abjfigpearw5000a", help="User ID for the request")
@click.option("--limit", default=100, help="Number of notes to fetch")
@click.option("--with-replies", is_flag=True, default=True, help="Include replies")
@click.option("--total-count", default=500, help="Total number of notes to fetch when paginating")
@click.option("--post", is_flag=True, help="Post analysis result to Misskey")
def analyze_command(user_id, limit, with_replies, total_count, post):
    """Analyze user notes from azkey.azuki.blue API"""
    try:
        data = get_all_notes_paginated(user_id, total_count, limit)
        # Analyze the data
        analysis_result = NoteAnalyzer.analyze(data)
        click.echo("=== Analysis Results ===")
        click.echo(analysis_result)

        if post:
            # Post analysis result to Misskey
            click.echo("\n投稿中...")
            result = create_note(
                text=analysis_result,
                cw=f"あずきインターネット評価書 ({user_id})"
            )
            click.echo(f"投稿が完了しました: {result.get('createdNote', {}).get('id', 'Unknown')}")

    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
    except Exception as e:
        click.echo(f"Error making request: {e}", err=True)
