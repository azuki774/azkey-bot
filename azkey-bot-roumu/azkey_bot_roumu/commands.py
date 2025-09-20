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


@click.command("timeline")
@click.option("--limit", default=10, help="Number of posts to fetch (default: 10)")
@click.option("--until-id", help="Get posts before this ID (for pagination)")
@click.option("--verbose", "-v", is_flag=True, help="Show detailed post information")
def timeline_command(limit, until_id, verbose):
    """Debug command: Fetch and display timeline posts"""
    try:
        # Load configuration
        usecases = Usecases()
        usecases.load_environment_variables()

        click.echo("🔍 タイムライン取得中...")
        click.echo(f"📊 取得件数: {limit}件")
        if until_id:
            click.echo(f"🔄 ページネーション: {until_id} より前")

        # Get timeline
        timeline = usecases.get_timeline(limit=limit, until_id=until_id)

        if not timeline:
            click.echo("📭 タイムラインが空です")
            return

        click.echo("=" * 60)
        click.echo(f"📝 取得したタイムライン: {len(timeline)}件")
        click.echo("=" * 60)

        for i, post in enumerate(timeline, 1):
            user = post.get("user", {})
            username = user.get("username", "unknown")
            name = user.get("name") or username

            # Post basic info
            post_id = post.get("id", "")
            created_at = post.get("createdAt", "")
            text = post.get("text", "")

            click.echo(f"\n📌 投稿 {i}")
            click.echo(f"👤 {name} (@{username})")
            click.echo(f"🆔 ID: {post_id}")
            click.echo(f"📅 投稿日時: {created_at}")

            if text:
                # Truncate long text
                display_text = text[:100] + "..." if len(text) > 100 else text
                click.echo(f"📝 内容: {display_text}")
            else:
                click.echo("📝 内容: (テキストなし)")

            if verbose:
                # Show additional details
                reactions = post.get("reactions", {})
                if reactions:
                    reaction_summary = ", ".join([f"{k}: {v}" for k, v in reactions.items()])
                    click.echo(f"💝 リアクション: {reaction_summary}")

                files = post.get("files", [])
                if files:
                    click.echo(f"📎 添付ファイル: {len(files)}個")

                reply_id = post.get("replyId")
                if reply_id:
                    click.echo(f"💬 返信先: {reply_id}")

                renote_id = post.get("renoteId")
                if renote_id:
                    click.echo(f"🔄 リノート元: {renote_id}")

            click.echo("-" * 40)

        # Show last post ID for pagination
        if timeline:
            last_post_id = timeline[-1].get("id")
            click.echo(f"\n🔄 次のページ取得用ID: {last_post_id}")
            click.echo(f"💡 コマンド例: azkey-bot-roumu timeline --limit {limit} --until-id {last_post_id}")

    except ValueError as e:
        click.echo(f"設定エラー: {e}", err=True)
    except Exception as e:
        click.echo(f"エラーが発生しました: {e}", err=True)


@click.command("check")
def check_command():
    """Check timeline for keywords and perform dakoku for matching users"""
    # 検索対象キーワード（定数として定義）
    TARGET_KEYWORDS = [
        "ログインボーナス",
        "ログボ",
        "打刻"
    ]

    try:
        # Load configuration
        usecases = Usecases()
        usecases.load_environment_variables()

        click.echo("🔍 タイムライン取得中...")
        click.echo(f"🔍 検索キーワード: {', '.join(TARGET_KEYWORDS)}")

        # Get timeline (最近100件)
        timeline = usecases.get_timeline(limit=100)

        if not timeline:
            click.echo("📭 タイムラインが空です")
            return

        click.echo(f"📝 取得したタイムライン: {len(timeline)}件")

        # キーワードが含まれているノートを抽出
        matching_posts = []
        for post in timeline:
            text = post.get("text", "")
            if text:
                # いずれかのキーワードが含まれているかチェック
                for keyword in TARGET_KEYWORDS:
                    if keyword in text:
                        matching_posts.append(post)
                        break  # 一つでもマッチしたら追加して次の投稿へ

        click.echo(f"🎯 キーワードが含まれる投稿: {len(matching_posts)}件")

        if not matching_posts:
            click.echo("🔍 該当する投稿が見つかりませんでした")
            return

        click.echo("=" * 50)

        # 打刻処理
        successful_checkins = []
        failed_checkins = []
        already_checked_in = []

        for i, post in enumerate(matching_posts, 1):
            user = post.get("user", {})
            user_id = user.get("id", "")
            username = user.get("username", "unknown")
            name = user.get("name") or username
            text = post.get("text", "")

            # マッチしたキーワードを特定
            matched_keyword = ""
            for keyword in TARGET_KEYWORDS:
                if keyword in text:
                    matched_keyword = keyword
                    break

            click.echo(f"\n📌 該当投稿 {i}")
            click.echo(f"👤 {name} (@{username})")
            click.echo(f"🆔 ユーザーID: {user_id}")
            click.echo(f"🎯 マッチしたキーワード: {matched_keyword}")
            display_text = text[:50] + "..." if len(text) > 50 else text
            click.echo(f"📝 内容: {display_text}")

            if not user_id:
                click.echo("⚠️  ユーザーIDが取得できませんでした")
                failed_checkins.append({"user_id": user_id, "username": username, "error": "ユーザーID不明"})
                continue

            # 打刻実行
            try:
                result = usecases.checkin_roumu(user_id)

                if result.get("already_checked_in", False):
                    click.echo("⚠️  既に本日打刻済みです")
                    already_checked_in.append({"user_id": user_id, "username": username})
                else:
                    click.echo("✅ 打刻完了!")
                    click.echo(f"🔢 連続回数: {result['consecutive_count']}回")
                    if result.get("was_new_user", False):
                        click.echo("🆕 新規ユーザーです")
                    successful_checkins.append({"user_id": user_id, "username": username, "consecutive_count": result['consecutive_count']})

            except Exception as e:
                click.echo(f"❌ 打刻失敗: {e}")
                failed_checkins.append({"user_id": user_id, "username": username, "error": str(e)})

            click.echo("-" * 30)

        # 結果サマリー
        click.echo("=" * 50)
        click.echo("📊 打刻処理結果:")
        click.echo(f"  🎯 該当投稿: {len(matching_posts)}件")
        click.echo(f"  ✅ 新規打刻: {len(successful_checkins)}人")
        click.echo(f"  ⚠️  既に打刻済み: {len(already_checked_in)}人")
        click.echo(f"  ❌ 失敗: {len(failed_checkins)}人")

        if successful_checkins:
            click.echo("\n✅ 新規打刻成功:")
            for checkin in successful_checkins:
                click.echo(f"  - {checkin['username']} (連続{checkin['consecutive_count']}回)")

        if already_checked_in:
            click.echo("\n⚠️  既に打刻済み:")
            for checkin in already_checked_in:
                click.echo(f"  - {checkin['username']}")

        if failed_checkins:
            click.echo("\n❌ 打刻失敗:")
            for failed in failed_checkins:
                click.echo(f"  - {failed['username']}: {failed['error']}")

        click.echo("\n📊 CSV ファイル 'roumu.csv' に記録されました")

    except ValueError as e:
        click.echo(f"設定エラー: {e}", err=True)
    except Exception as e:
        click.echo(f"エラーが発生しました: {e}", err=True)
