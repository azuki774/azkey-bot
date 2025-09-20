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
        click.echo("📊 フォローバック結果:")
        click.echo(f"  👥 フォロワー数: {result['total_followers']}人")
        click.echo(f"  ➡️  フォロー中: {result['total_following']}人")
        click.echo(f"  🔄 フォローバック対象: {result['users_to_follow_back']}人")
        click.echo(f"  ✅ 成功: {result['success_count']}人")
        click.echo(f"  ❌ 失敗: {result['failure_count']}人")

        if result["successful_follows"]:
            click.echo("\n✅ フォローバック成功:")
            for user_id in result["successful_follows"]:
                click.echo(f"  - {user_id}")

        if result["failed_follows"]:
            click.echo("\n❌ フォローバック失敗:")
            for failed in result["failed_follows"]:
                click.echo(f"  - {failed['follow_id']}: {failed['error']}")

        if result["users_to_follow_back"] == 0:
            click.echo("\n🎉 すべてのフォロワーを既にフォロー済みです！")

    except ValueError as e:
        click.echo(f"設定エラー: {e}", err=True)
    except Exception as e:
        click.echo(f"エラーが発生しました: {e}", err=True)


@click.command("dakoku")
@click.option("--user-id", required=True, help="User ID to check in")
def dakoku_command(user_id):
    """Debug command: Manual check-in for specified user"""
    try:
        # Load configuration
        usecases = Usecases()
        usecases.load_environment_variables()

        click.echo("🔧 デバッグ打刻を実行中...")
        click.echo(f"👤 ユーザーID: {user_id}")

        # Get username from API if not provided
        click.echo("📡 ユーザー名をAPIから取得中...")
        try:
            username = usecases.get_username_from_userid(user_id)
            click.echo(f"📝 取得したユーザー名: {username}")
        except Exception as e:
            click.echo(f"⚠️  ユーザー名取得失敗: {e}")
            username = "unknown_user"

        # Perform check-in
        result = usecases.checkin_roumu(user_id)

        click.echo("=" * 50)
        if result.get("already_checked_in", False):
            click.echo("⚠️  既に本日打刻済みです")
            click.echo(f"👤 ユーザー名: {username}")
            click.echo(f"📅 前回打刻: {result['last_checkin']}")
            click.echo(f"🔢 連続回数: {result['consecutive_count']}回")
        else:
            click.echo("✅ 打刻完了!")
            click.echo(f"👤 ユーザー名: {username}")
            click.echo(f"📅 打刻時刻: {result['last_checkin']}")
            click.echo(f"🔢 連続回数: {result['consecutive_count']}回")
            if result.get("was_new_user", False):
                click.echo("🆕 新規ユーザーです")

        click.echo("\n📊 CSV ファイル 'roumu.csv' に記録されました")

    except Exception as e:
        click.echo(f"エラーが発生しました: {e}", err=True)
