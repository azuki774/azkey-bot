import click
from .analyzer import NoteAnalyzer
from .next_analyzer import NextNoteAnalyzer
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


@click.command("next")
@click.option("--user-id", default="abjfigpearw5000a", help="User ID for the request")
@click.option("--limit", default=100, help="Number of notes to fetch")
@click.option("--total-count", default=100, help="Total number of notes to fetch for analysis")
@click.option("--post", is_flag=True, help="Post generated note to Misskey")
def next_command(user_id, limit, total_count, post):
    """Generate next note based on user's posting patterns"""
    try:
        click.echo(f"📊 {user_id} の過去 {total_count} 件の投稿を分析中...")
        data = get_all_notes_paginated(user_id, total_count, limit)
        
        click.echo("🤖 投稿パターンを学習して次のノートを生成中...")
        next_note = NextNoteAnalyzer.generate_next_note(data)
        
        click.echo("=== 生成されたノート ===")
        click.echo(next_note)
        click.echo("=" * 30)
        
        if post:
            click.echo("📝 投稿中...")
            result = create_note(text=next_note)
            click.echo(f"✅ 投稿が完了しました: {result.get('createdNote', {}).get('id', 'Unknown')}")
        else:
            click.echo("💡 投稿するには --post オプションを追加してください")
            
    except ValueError as e:
        click.echo(f"Error: {e}", err=True)
    except Exception as e:
        click.echo(f"Error making request: {e}", err=True)
