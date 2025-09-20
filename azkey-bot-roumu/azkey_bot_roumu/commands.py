import click
from .usecases import Usecases


@click.command("status")
def status_command():
    """Show current status"""
    click.echo("azkey-bot-roumu is running!")


@click.command("follow")
@click.option("--limit", default=10, help="Number of notifications to fetch")
def follow_command(limit):
    """Get latest follow notifications"""
    try:
        # Load configuration
        usecases = Usecases()
        usecases.load_environment_variables()
        click.echo("🔄 フォローバック処理を開始します...")
        result = usecases.follow_back()
        
        click.echo("=" * 50)
        click.echo(f"📊 フォローバック結果:")
        click.echo(f"  👥 フォロワー数: {result['total_followers']}人")
        click.echo(f"  ➡️  フォロー中: {result['total_following']}人")
        click.echo(f"  🔄 フォローバック対象: {result['users_to_follow_back']}人")
        click.echo(f"  ✅ 成功: {result['success_count']}人")
        click.echo(f"  ❌ 失敗: {result['failure_count']}人")
        
        if result['successful_follows']:
            click.echo("\n✅ フォローバック成功:")
            for user_id in result['successful_follows']:
                click.echo(f"  - {user_id}")
        
        if result['failed_follows']:
            click.echo("\n❌ フォローバック失敗:")
            for failed in result['failed_follows']:
                click.echo(f"  - {failed['follow_id']}: {failed['error']}")
                
        if result['users_to_follow_back'] == 0:
            click.echo("\n🎉 すべてのフォロワーを既にフォロー済みです！")

    except ValueError as e:
        click.echo(f"設定エラー: {e}", err=True)
    except Exception as e:
        click.echo(f"エラーが発生しました: {e}", err=True)
