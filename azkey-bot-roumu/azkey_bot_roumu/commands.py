import click

from .logger import setup_logger
from .usecases import Usecases


@click.command("status")
def status_command():
    """Show current status"""
    logger = setup_logger(__name__)
    logger.info("action=status_check message=\"azkey-bot-roumu is running\"")


@click.command("follow")
@click.option("--limit", default=100, help="Number of users to check for follow back")
def follow_command(limit):
    """Execute follow back process with structured logging"""
    logger = setup_logger(__name__)
    
    try:
        usecases = Usecases()
        usecases.load_environment_variables()
        
        # Log start
        logger.info(f"action=follow_start limit={limit}")
        
        result = usecases.follow_back(limit=limit)
        
        # Log results
        logger.info(f"action=follow_complete total_followers={result['total_followers']} "
                   f"total_following={result['total_following']} "
                   f"users_to_follow_back={result['users_to_follow_back']} "
                   f"success_count={result['success_count']} "
                   f"failure_count={result['failure_count']}")
        
        # Log successful follows
        for user_id in result["successful_follows"]:
            logger.info(f"action=follow_success user_id={user_id}")
        
        # Log failed follows
        for failed in result["failed_follows"]:
            logger.warning(f"action=follow_failed user_id={failed['follow_id']} error=\"{failed['error']}\"")
        
        # Log final result summary
        if result["users_to_follow_back"] > 0:
            logger.info(f"action=follow_summary message=\"Follow back completed\" "
                       f"success_count={result['success_count']} failure_count={result['failure_count']}")
        else:
            logger.info("action=follow_summary message=\"No users to follow back\"")
            
    except Exception as e:
        logger.error(f"action=follow_error error=\"{e}\"")
        raise


@click.command("dakoku")
@click.option("--user-id", required=True, help="User ID to check in")
def dakoku_command(user_id):
    """Debug command: Manual check-in for specified user"""
    logger = setup_logger(__name__)
    
    try:
        usecases = Usecases()
        usecases.load_environment_variables()

        logger.info(f"action=dakoku_start user_id={user_id}")

        # Get username from API
        try:
            username = usecases.get_username_from_userid(user_id)
            logger.info(f"action=username_retrieved user_id={user_id} username=\"{username}\"")
        except Exception as e:
            logger.warning(f"action=username_failed user_id={user_id} error=\"{e}\"")
            username = "unknown_user"

        # Perform check-in
        result = usecases.checkin_roumu(user_id)

        if result.get("already_checked_in", False):
            logger.info(f"action=already_checked_in user_id={user_id} username=\"{username}\" "
                       f"consecutive_count={result['consecutive_count']} last_checkin=\"{result['last_checkin']}\" "
                       f"message=\"Already checked in today\"")
        else:
            logger.info(f"action=checkin_success user_id={user_id} username=\"{username}\" "
                       f"consecutive_count={result['consecutive_count']} last_checkin=\"{result['last_checkin']}\" "
                       f"was_new_user={result.get('was_new_user', False)} message=\"Check-in successful\"")

    except Exception as e:
        logger.error(f"action=dakoku_error user_id={user_id} error=\"{e}\"")
        raise


@click.command("timeline")
@click.option("--limit", default=10, help="Number of posts to fetch (default: 10)")
@click.option("--until-id", help="Get posts before this ID (for pagination)")
@click.option("--verbose", "-v", is_flag=True, help="Show detailed post information")
def timeline_command(limit, until_id, verbose):
    """Debug command: Fetch and display timeline posts"""
    logger = setup_logger(__name__)
    
    try:
        usecases = Usecases()
        usecases.load_environment_variables()

        logger.info(f"action=timeline_start limit={limit} until_id={until_id}")

        timeline = usecases.get_timeline(limit=limit, until_id=until_id)

        if not timeline:
            logger.info("action=timeline_empty message=\"Timeline is empty\"")
            return

        logger.info(f"action=timeline_retrieved post_count={len(timeline)} message=\"Retrieved timeline posts\"")

        for i, post in enumerate(timeline, 1):
            user = post.get("user", {})
            username = user.get("username", "unknown")
            name = user.get("name") or username
            
            post_id = post.get("id", "")
            created_at = post.get("createdAt", "")
            text = post.get("text", "")
            
            # Log post details
            post_data = f"action=timeline_post post_number={i} user_id={user.get('id', '')} username=\"{username}\" post_id={post_id} created_at=\"{created_at}\""
            
            if text:
                display_text = text[:100] + "..." if len(text) > 100 else text
                post_data += f" text=\"{display_text}\""
            else:
                post_data += " text=\"(no text)\""

            if verbose:
                reactions = post.get("reactions", {})
                if reactions:
                    reaction_summary = ", ".join([f"{k}: {v}" for k, v in reactions.items()])
                    post_data += f" reactions=\"{reaction_summary}\""
                
                files = post.get("files", [])
                if files:
                    post_data += f" files_count={len(files)}"
                
                reply_id = post.get("replyId")
                if reply_id:
                    post_data += f" reply_to={reply_id}"
                
                renote_id = post.get("renoteId")
                if renote_id:
                    post_data += f" renote_of={renote_id}"
            
            logger.info(post_data)

        # Log pagination info
        if timeline:
            last_post_id = timeline[-1].get("id")
            logger.info(f"action=timeline_pagination last_post_id={last_post_id} "
                       f"next_command=\"azkey-bot-roumu timeline --limit {limit} --until-id {last_post_id}\"")

    except Exception as e:
        logger.error(f"action=timeline_error error=\"{e}\"")
        raise


@click.command("check")
def check_command():
    """Check timeline for keywords and perform dakoku with structured logging"""
    logger = setup_logger(__name__)
    
    # 検索対象キーワード（定数として定義）
    TARGET_KEYWORDS = ["ログインボーナス", "ログボ", "打刻"]
    
    try:
        usecases = Usecases()
        usecases.load_environment_variables()

        logger.info(f"action=check_start keywords=\"{','.join(TARGET_KEYWORDS)}\"")

        # Get timeline (最近100件)
        timeline = usecases.get_timeline(limit=100)

        if not timeline:
            logger.info("action=timeline_empty message=\"Timeline is empty\"")
            return

        logger.info(f"action=timeline_retrieved post_count={len(timeline)} message=\"Retrieved timeline posts\"")

        # キーワードが含まれているノートを抽出
        matching_posts = []
        for post in timeline:
            text = post.get("text", "")
            if text:
                for keyword in TARGET_KEYWORDS:
                    if keyword in text:
                        matching_posts.append(post)
                        break

        logger.info(f"action=keyword_match_complete matching_posts={len(matching_posts)} message=\"Keyword matching completed\"")

        if not matching_posts:
            logger.info("action=no_matches message=\"No matching posts found\"")
            return

        # 打刻処理
        successful_checkins = []
        failed_checkins = []
        already_checked_in = []

        for i, post in enumerate(matching_posts, 1):
            user = post.get("user", {})
            user_id = user.get("id", "")
            username = user.get("username", "unknown")
            text = post.get("text", "")
            post_id = post.get("id", "")

            # マッチしたキーワードを特定
            matched_keyword = ""
            for keyword in TARGET_KEYWORDS:
                if keyword in text:
                    matched_keyword = keyword
                    break

            logger.info(f"action=process_post post_number={i} user_id={user_id} "
                       f"username=\"{username}\" post_id={post_id} matched_keyword=\"{matched_keyword}\"")

            if not user_id:
                logger.warning(f"action=user_id_missing post_id={post_id}")
                failed_checkins.append({"user_id": user_id, "username": username, "note_id": post_id, "error": "ユーザーID不明"})
                continue

            # 打刻実行
            try:
                result = usecases.checkin_roumu(user_id)

                if result.get("already_checked_in", False):
                    logger.info(f"action=already_checked_in user_id={user_id} username=\"{username}\" "
                               f"post_id={post_id} consecutive_count={result['consecutive_count']}")
                    already_checked_in.append({"user_id": user_id, "username": username, "note_id": post_id})
                else:
                    logger.info(f"action=checkin_success user_id={user_id} username=\"{username}\" "
                               f"post_id={post_id} consecutive_count={result['consecutive_count']} "
                               f"was_new_user={result.get('was_new_user', False)}")
                    
                    # 根拠ノートにリアクションを追加
                    try:
                        usecases.add_reaction_to_note(post_id, "👍")
                        logger.info(f"action=reaction_added post_id={post_id} reaction=👍")
                    except Exception as reaction_error:
                        logger.warning(f"action=reaction_failed post_id={post_id} error=\"{reaction_error}\"")
                    
                    successful_checkins.append({"user_id": user_id, "username": username, "note_id": post_id, "consecutive_count": result['consecutive_count']})

            except Exception as e:
                logger.error(f"action=checkin_failed user_id={user_id} username=\"{username}\" "
                            f"post_id={post_id} error=\"{e}\"")
                failed_checkins.append({"user_id": user_id, "username": username, "note_id": post_id, "error": str(e)})

        # 結果サマリー
        logger.info(f"action=check_complete matching_posts={len(matching_posts)} "
                   f"success_count={len(successful_checkins)} "
                   f"already_count={len(already_checked_in)} "
                   f"failure_count={len(failed_checkins)} "
                   f"message=\"Timeline check completed\"")

    except Exception as e:
        logger.error(f"action=check_error error=\"{e}\"")
        raise
