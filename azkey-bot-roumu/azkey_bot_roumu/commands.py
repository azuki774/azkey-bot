import os
import signal
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

import click

from .logger import setup_logger
from .usecases import Usecases


@click.command("status")
def status_command():
    """Show current status"""
    logger = setup_logger(__name__)
    logger.info('action=status_check message="azkey-bot-roumu is running"')


@click.command("reset")
def reset_command():
    """Reset all users' count based on current state with structured logging"""
    logger = setup_logger(__name__)

    try:
        csv_dir = os.getenv("ROUMU_DATA_DIR")
        usecases = Usecases(csv_dir=csv_dir)

        # Log start
        logger.info('action=reset_start message="Starting reset process for all users"')

        result = usecases.reset_count()

        # Log results
        logger.info(
            f"action=reset_complete total_users={result['total_users']} "
            f"consecutive_count_reset={result['consecutive_count_reset']} "
            f"last_checkin_reset={result['last_checkin_reset']} "
            f'message="{result["message"]}"'
        )

    except Exception as e:
        logger.error(f'action=reset_error error="{e}"')
        raise


@click.command("serve")
@click.option(
    "--interval",
    default=300,
    help="Interval in seconds between runs (default: 300 = 5 minutes)",
)
@click.option(
    "--http-port",
    default=8080,
    type=int,
    help="HTTP server port for webhook endpoint (optional, default: disabled)",
)
def serve_command(interval, http_port):
    """Serve mode: Run follow and check commands continuously with specified interval"""
    logger = setup_logger(__name__)

    # Flag to control the main loop
    shutdown_requested = False

    def signal_handler(signum, _):
        nonlocal shutdown_requested
        signal_name = signal.Signals(signum).name
        logger.info(
            f'action=signal_received signal={signal_name} message="Shutdown requested"'
        )
        shutdown_requested = True

    # Register signal handlers for graceful shutdown
    signal.signal(signal.SIGTERM, signal_handler)
    signal.signal(signal.SIGINT, signal_handler)

    csv_dir = os.getenv("ROUMU_DATA_DIR")

    # HTTP server setup
    http_server = None
    if http_port:

        class ResetHTTPRequestHandler(BaseHTTPRequestHandler):
            def do_GET(self):
                """Handle GET requests for various endpoints"""
                if self.path == "/reset":
                    self._handle_reset()
                else:
                    self.send_response(404)
                    self.end_headers()

            def _handle_reset(self):
                """Handle /reset endpoint - execute reset_command logic"""
                try:
                    logger.info(
                        f'action=http_reset_start remote_addr={self.client_address[0]} message="HTTP reset request received"'
                    )

                    # Execute reset logic (same as reset_command)
                    usecases = Usecases(csv_dir=csv_dir)
                    result = usecases.reset_count()

                    # Log results
                    logger.info(
                        f"action=http_reset_complete total_users={result['total_users']} "
                        f"consecutive_count_reset={result['consecutive_count_reset']} "
                        f"last_checkin_reset={result['last_checkin_reset']} "
                        f'message="{result["message"]}"'
                    )

                    # Send 200 OK response with empty body
                    self.send_response(200)
                    self.end_headers()

                except Exception as e:
                    logger.error(f'action=http_reset_error error="{e}"')
                    self.send_response(500)
                    self.end_headers()

            def log_message(self, _format, *_args):
                """Suppress default HTTP server logs"""
                pass

        def start_http_server():
            nonlocal http_server
            try:
                http_server = HTTPServer(
                    ("0.0.0.0", http_port), ResetHTTPRequestHandler
                )
                logger.info(
                    f'action=http_server_start port={http_port} message="HTTP server started"'
                )
                http_server.serve_forever()
            except Exception as e:
                logger.error(f'action=http_server_error error="{e}"')

        # Start HTTP server in a separate thread
        http_thread = threading.Thread(target=start_http_server, daemon=True)
        http_thread.start()
        logger.info(
            f'action=http_server_thread_started port={http_port} message="HTTP server thread started"'
        )

    try:
        read_latest_id = None  # どこまで既に読み込み済か
        usecases = Usecases(csv_dir=csv_dir)
        usecases.load_environment_variables()

        logger.info(
            f'action=serve_start interval={interval} message="Starting serve mode"'
        )

        cycle_count = 0

        while not shutdown_requested:
            cycle_count += 1
            logger.info(
                f'action=serve_cycle_start cycle={cycle_count} message="Starting new cycle"'
            )

            # Execute follow and check operations directly
            try:
                logger.info(
                    f'action=follow_execute cycle={cycle_count} message="Executing follow operations"'
                )
                result = usecases.follow_back(limit=100)
                logger.info(
                    f"action=follow_complete cycle={cycle_count} "
                    f"users_to_follow_back={result.get('users_to_follow_back', 0)} "
                    f"success_count={result.get('success_count', 0)}"
                )
            except Exception as e:
                logger.error(f'action=follow_error cycle={cycle_count} error="{e}"')

            try:
                logger.info(
                    f'action=check_execute cycle={cycle_count} read_latest_id={read_latest_id} message="Executing check operations"'
                )
                timeline, next_read_latest_id = usecases.get_timeline(
                    limit=100, since_id=read_latest_id
                )
                notes_count = len(timeline) if timeline else 0
                logger.info(
                    f'action=check_execute cycle={cycle_count} notes_read={notes_count} message="Read timeline notes"'
                )
                if timeline:
                    TARGET_KEYWORDS = ["ログインボーナス", "ログボ", "打刻", "出勤"]
                    matching_posts = []
                    for post in timeline:
                        text = post.get("text", "")
                        if text and any(keyword in text for keyword in TARGET_KEYWORDS):
                            matching_posts.append(post)

                    successful_checkins = 0
                    failed_checkins = 0
                    already_checked_in = 0
                    for post in matching_posts:
                        user_id = post.get("user", {}).get("id")
                        if user_id:
                            try:
                                result = usecases.checkin_roumu(user_id)
                                if result.get("already_checked_in", False):
                                    already_checked_in += 1
                                else:
                                    successful_checkins += 1
                                    post_id = post.get("id")
                                    if post_id:
                                        try:
                                            usecases.add_reaction_to_note(post_id, "👍")
                                        except Exception as reaction_error:
                                            logger.warning(
                                                f'action=reaction_failed post_id={post_id} error="{reaction_error}"'
                                            )
                            except Exception as checkin_error:
                                failed_checkins += 1
                                logger.error(
                                    f'action=checkin_failed user_id={user_id} post_id={post.get("id")} error="{checkin_error}"'
                                )

                    logger.info(
                        f"action=check_complete cycle={cycle_count} "
                        f"matching_posts={len(matching_posts)} "
                        f"successful_checkins={successful_checkins} "
                        f"already_count={already_checked_in} "
                        f"failure_count={failed_checkins}"
                    )
                    read_latest_id = next_read_latest_id  # タイムラインを読み切ったので、次以降読まないように
                else:
                    logger.info(
                        f'action=timeline_empty cycle={cycle_count} message="Timeline is empty"'
                    )
            except Exception as e:
                logger.error(f'action=check_error cycle={cycle_count} error="{e}"')

            # メンションが来ていないか確認し、来ていたら処理する
            try:
                logger.info(
                    f'action=mention_check cycle={cycle_count} message="Checking for new mentions"'
                )

                # フォロー中ユーザーからのリアクションしていないメンション取得
                mentions = usecases.get_mentions_without_reaction(
                    limit=20, following=True
                )

                if mentions:
                    logger.info(
                        f'action=mentions_found cycle={cycle_count} count={len(mentions)} message="Processing mentions"'
                    )

                    for i, mention in enumerate(mentions, 1):
                        user = mention.get("user", {})
                        user_id = user.get("id", "")
                        username = user.get("username", "unknown")
                        mention_id = mention.get("id", "")
                        text = mention.get("text", "")

                        logger.info(
                            f"action=mention_process cycle={cycle_count} mention_number={i} "
                            f'user_id={user_id} username="{username}" mention_id={mention_id}'
                        )

                        try:
                            # ユーザー情報をリプライで返す
                            reply_result = usecases.reply_user_info(mention)

                            logger.info(
                                f"action=mention_reply_success cycle={cycle_count} "
                                f'user_id={user_id} username="{username}" mention_id={mention_id} '
                                f"reply_id={reply_result.get('createdNote', {}).get('id', 'unknown')}"
                            )

                            # メンションにリアクションを追加（処理済みマーク）
                            try:
                                usecases.add_reaction_to_note(mention_id, "👍")
                                logger.info(
                                    f"action=mention_reaction_added cycle={cycle_count} mention_id={mention_id} reaction=👍"
                                )
                            except Exception as reaction_error:
                                logger.warning(
                                    f"action=mention_reaction_failed cycle={cycle_count} "
                                    f'mention_id={mention_id} error="{reaction_error}"'
                                )

                        except Exception as reply_error:
                            logger.error(
                                f"action=mention_reply_failed cycle={cycle_count} "
                                f'user_id={user_id} username="{username}" mention_id={mention_id} error="{reply_error}"'
                            )

                    logger.info(
                        f"action=mention_processing_complete cycle={cycle_count} "
                        f'processed_count={len(mentions)} message="All mentions processed"'
                    )
                else:
                    logger.info(
                        f'action=no_new_mentions cycle={cycle_count} message="No new mentions found"'
                    )

            except Exception as e:
                logger.error(
                    f'action=mention_check_error cycle={cycle_count} error="{e}"'
                )

            logger.info(
                f'action=serve_cycle_complete cycle={cycle_count} message="Cycle completed"'
            )

            # Check for shutdown before sleeping
            if shutdown_requested:
                break

            # Wait for the specified interval with periodic checks for shutdown
            logger.info(
                f'action=serve_sleep cycle={cycle_count} interval={interval} message="Sleeping until next cycle"'
            )
            sleep_remaining = interval
            while sleep_remaining > 0 and not shutdown_requested:
                sleep_time = min(1, sleep_remaining)  # Check every second
                time.sleep(sleep_time)
                sleep_remaining -= sleep_time

        logger.info(
            f'action=serve_stop cycle={cycle_count} message="Serve mode stopped gracefully"'
        )

    except KeyboardInterrupt:
        logger.info(
            f'action=serve_stop cycle={cycle_count} message="Serve mode stopped by user (KeyboardInterrupt)"'
        )
    except Exception as e:
        logger.error(f'action=serve_error cycle={cycle_count} error="{e}"')
        raise
    finally:
        # Shutdown HTTP server if it's running
        if http_server:
            logger.info(
                'action=http_server_shutdown message="Shutting down HTTP server"'
            )
            http_server.shutdown()
            logger.info('action=http_server_stopped message="HTTP server stopped"')
