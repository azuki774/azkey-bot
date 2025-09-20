import click
import json
from .analyzer import NoteAnalyzer
from .openrouter import analyze_with_ai
from .misskey import get_all_notes_paginated


@click.command("status")
def status_command():
    click.echo("azkey-bot is running!")


@click.command("get")
@click.option("--user-id", default="acdc20dugqdo00jp", help="User ID for the request")
@click.option("--limit", default=10, help="Number of notes to fetch")
@click.option("--with-replies", is_flag=True, default=True, help="Include replies")
@click.option("--analyze", is_flag=True, help="Analyze the response data")
@click.option("--total-count", default=500, help="Total number of notes to fetch when paginating")
def get_command(user_id, limit, with_replies, analyze, total_count):
    """Get user notes from azkey.azuki.blue API"""
    try:
        data = get_all_notes_paginated(user_id, total_count, limit)
        # Analyze the data
        analysis_result = NoteAnalyzer.extract(data)
        click.echo("=== Analysis Results ===")
        click.echo(analysis_result)
        if analyze:
            ai_analysis_result = analyze_with_ai(analysis_result)
            click.echo(ai_analysis_result)
        else:
            # Display raw data
            click.echo(json.dumps(data, indent=2, ensure_ascii=False))

    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
    except Exception as e:
        click.echo(f"Error making request: {e}", err=True)
