import os
import signal
import time

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
def serve_command(interval):
    """Serve mode: Run follow and check commands continuously with specified interval"""
    logger = setup_logger(__name__)

    # Flag to control the main loop
    shutdown_requested = False

    def signal_handler(signum, _frame):
        nonlocal shutdown_requested
        signal_name = signal.Signals(signum).name
        logger.info(
            f'action=signal_received signal={signal_name} message="Shutdown requested"'
        )
        shutdown_requested = True

    # Register signal handlers for graceful shutdown
    signal.signal(signal.SIGTERM, signal_handler)
    signal.signal(signal.SIGINT, signal_handler)

    try:
        csv_dir = os.getenv("ROUMU_DATA_DIR")
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
                    f'action=check_execute cycle={cycle_count} message="Executing check operations"'
                )
                timeline = usecases.get_timeline(limit=100)
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
                else:
                    logger.info(
                        f'action=timeline_empty cycle={cycle_count} message="Timeline is empty"'
                    )
            except Exception as e:
                logger.error(f'action=check_error cycle={cycle_count} error="{e}"')

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
