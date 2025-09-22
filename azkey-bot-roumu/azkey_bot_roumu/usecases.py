"""Usecases class for azkey-bot-roumu"""

import os

from .roumu_data import RoumuData


class Usecases:
    """Main usecases class for handling configuration and API endpoints"""

    def __init__(self, csv_dir: str = None):
        """Initialize Usecases class

        Args:
            csv_dir: Directory path for CSV file storage (default: current directory)
        """
        self.i = None
        self.openrouter_api_key = None
        self.misskey_endpoint = os.getenv(
            "MISSKEY_ENDPOINT", "https://azkey.azuki.blue"
        )

        # Configure CSV file path
        if csv_dir:
            csv_file_path = os.path.join(csv_dir, "roumu.csv")
        else:
            csv_file_path = "roumu.csv"

        self.roumu_data = RoumuData(csv_file_path)

    def load_environment_variables(self):
        """Load environment variables i and OPENROUTER_API_KEY

        Raises:
            ValueError: If required environment variables are not set
        """
        self.i = os.getenv("i")
        self.openrouter_api_key = os.getenv("OPENROUTER_API_KEY")

        if not self.i:
            raise ValueError("Environment variable 'i' is not set")

        if not self.openrouter_api_key:
            raise ValueError("Environment variable 'OPENROUTER_API_KEY' is not set")

    def is_configured(self) -> bool:
        """Check if all required configuration is loaded

        Returns:
            True if all configuration is available, False otherwise
        """
        return self.i is not None and self.openrouter_api_key is not None

    def get_misskey_client(self):
        """Get configured Misskey client instance

        Returns:
            Misskey: Configured Misskey client

        Raises:
            ValueError: If configuration is not loaded
        """
        from .misskey import Misskey

        if not self.is_configured():
            raise ValueError(
                "Configuration not loaded. Call load_environment_variables() first."
            )

        return Misskey(self.misskey_endpoint, self.i)

    def get_followers(self, user_id: str, limit: int = 100) -> dict:
        """Get user's followers list

        Args:
            user_id: Target user ID to get followers
            limit: Number of followers to fetch (default: 100)

        Returns:
            API response containing followers list

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()
        return misskey.get_followers(user_id=user_id, limit=limit)

    def get_following(self, user_id: str, limit: int = 100) -> dict:
        """Get user's following list

        Args:
            user_id: Target user ID to get following list
            limit: Number of following users to fetch (default: 100)

        Returns:
            API response containing following list

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()
        return misskey.get_following(user_id=user_id, limit=limit)

    def get_my_user_id(self) -> str:
        """Get current user's ID

        Returns:
            Current user's ID

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()
        my_info = misskey.get_my_info()
        return my_info.get("id")

    def get_my_followers(self, limit: int = 100) -> dict:
        """Get current user's followers list

        Args:
            limit: Number of followers to fetch (default: 100)

        Returns:
            API response containing followers list

        Raises:
            ValueError: If configuration is not loaded
        """
        my_user_id = self.get_my_user_id()
        return self.get_followers(user_id=my_user_id, limit=limit)

    def get_my_following(self, limit: int = 100) -> dict:
        """Get current user's following list

        Args:
            limit: Number of following users to fetch (default: 100)

        Returns:
            API response containing following list

        Raises:
            ValueError: If configuration is not loaded
        """
        my_user_id = self.get_my_user_id()
        return self.get_following(user_id=my_user_id, limit=limit)

    def follow_back(self, limit: int = 100) -> dict:
        """Follow back users who are following me but I'm not following them

        Args:
            limit: Maximum number of users to check (default: 100)

        Returns:
            Dictionary containing follow back results

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()

        # フォロワーリストを取得
        followers_response = self.get_my_followers(limit=limit)

        # フォローリストを取得
        following_response = self.get_my_following(limit=limit)

        # フォローされているけど、フォローしていないユーザを抽出
        # Misskeyのレスポンス構造: {"follower": {"id": "..."}} と {"followee": {"id": "..."}}
        followers_ids = set()
        for item in followers_response:
            if isinstance(item, dict) and "follower" in item and item["follower"]:
                followers_ids.add(item["follower"].get("id"))

        following_ids = set()
        for item in following_response:
            if isinstance(item, dict) and "followee" in item and item["followee"]:
                following_ids.add(item["followee"].get("id"))

        # フォローバックすべきユーザーID
        users_to_follow_back = followers_ids - following_ids

        # フォローバック実行
        successful_follows = []
        failed_follows = []

        for follow_id in users_to_follow_back:
            try:
                misskey.follow_user(follow_id)
                successful_follows.append(follow_id)
            except Exception as e:
                failed_follows.append({"follow_id": follow_id, "error": str(e)})

        return {
            "total_followers": len(followers_ids),
            "total_following": len(following_ids),
            "users_to_follow_back": len(users_to_follow_back),
            "successful_follows": successful_follows,
            "failed_follows": failed_follows,
            "success_count": len(successful_follows),
            "failure_count": len(failed_follows),
        }

    def checkin_roumu(self, user_id: str) -> dict:
        """Record roumu check-in for a user

        Args:
            user_id: User ID to check in

        Returns:
            Dictionary containing check-in results
        """

        return self.roumu_data.update_checkin(user_id)

    def get_roumu_leaderboard(self, limit: int = 10) -> list:
        """Get roumu leaderboard

        Args:
            limit: Maximum number of users to return (default: 10)

        Returns:
            List of users sorted by consecutive count
        """
        return self.roumu_data.get_leaderboard(limit)

    def get_roumu_user_status(self, user_id: str) -> dict:
        """Get specific user's roumu status

        Args:
            user_id: Target user ID

        Returns:
            User's roumu data or None if not found
        """
        return self.roumu_data.get_user(user_id)

    def reset_count(self) -> dict:
        """Reset all users' count based on current state

        - If last_checkin is empty: set consecutive_count = 0
        - If last_checkin is not empty: set last_checkin = ""

        Returns:
            Dictionary with reset results
        """
        return self.roumu_data.reset_count()

    def get_username_from_userid(self, user_id: str) -> str:
        """Get username from user ID using Misskey API

        Args:
            user_id: Target user ID

        Returns:
            Username in format "username@host" or "username" for local users

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()
        user_info = misskey.get_user_info(user_id)

        username = user_info.get("username", "unknown")
        host = user_info.get("host")

        # Return format: username@host or just username for local users
        if host and host != "local":
            return f"{username}@{host}"
        else:
            return username

    def get_timeline(self, limit: int = 100, until_id: str = None) -> dict:
        """Get timeline posts

        Args:
            limit: Number of posts to fetch (default: 100)
            until_id: Get posts before this ID for pagination

        Returns:
            API response containing timeline posts

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()
        return misskey.get_timeline(limit=limit, until_id=until_id)

    def add_reaction_to_note(self, note_id: str, reaction: str) -> dict:
        """Add reaction to a note

        Args:
            note_id: Target note ID to add reaction
            reaction: Reaction emoji (e.g., "👍", "❤️", "😀")

        Returns:
            API response

        Raises:
            ValueError: If configuration is not loaded or required parameters are not provided
        """
        misskey = self.get_misskey_client()
        return misskey.add_reaction(note_id=note_id, reaction=reaction)

    def get_mentions_without_reaction(
        self, limit: int = 20, following: bool = True
    ) -> list:
        """Get mentions that haven't been reacted to yet

        Args:
            limit: Number of mentions to fetch (default: 20)
            following: Only show mentions from users you follow (default: True)

        Returns:
            List of mention notes that haven't been reacted to by the current user

        Raises:
            ValueError: If configuration is not loaded
        """
        misskey = self.get_misskey_client()

        # Get current user's info to identify our own reactions
        my_info = misskey.get_my_info()
        my_user_id = my_info.get("id")

        if not my_user_id:
            raise ValueError("Could not get current user ID")

        # Get mentions from API
        mentions_response = misskey.get_mentions(limit=limit, following=following)
        mentions = mentions_response if isinstance(mentions_response, list) else []

        # Filter out mentions we've already reacted to
        unreacted_mentions = []

        for mention in mentions:
            reactions = mention.get("reactions", {})

            # Check if we've reacted to this mention
            has_my_reaction = False
            for _reaction_emoji, reaction_users in reactions.items():
                # reaction_users can be a list of user objects or user IDs
                if isinstance(reaction_users, list):
                    for reaction_user in reaction_users:
                        user_id = (
                            reaction_user.get("id")
                            if isinstance(reaction_user, dict)
                            else reaction_user
                        )
                        if user_id == my_user_id:
                            has_my_reaction = True
                            break
                elif isinstance(reaction_users, (int, str)) and str(
                    reaction_users
                ) == str(my_user_id):
                    # Single reaction count (older format)
                    has_my_reaction = True

                if has_my_reaction:
                    break

            # Only include mentions we haven't reacted to
            if not has_my_reaction:
                unreacted_mentions.append(mention)

        return unreacted_mentions

    def reply_user_info(self, note: dict) -> dict:
        """Reply to a user with their roumu information

        Args:
            note: Note object containing user information and note ID

        Returns:
            API response from creating the reply note

        Raises:
            ValueError: If configuration is not loaded or required data is missing
        """
        # Extract user ID from note
        user_id = note.get("userId") or note.get("user", {}).get("id")
        if not user_id:
            raise ValueError("Could not extract user ID from note")

        note_id = note.get("id")
        if not note_id:
            raise ValueError("Could not extract note ID from note")

        username = note.get("user", {}).get("username", "unknown")

        # Get user's roumu data from CSV
        user_data = self.roumu_data.get_user(user_id)

        # Format user information for reply
        if user_data:
            consecutive_count = int(user_data.get("consecutive_count", 0))
            total_count = int(user_data.get("total_count", 0))
            last_checkin = user_data.get("last_checkin", "")

            reply_text = f"""@{username} さんのログボ情報📊

🔥 連続ログボ: {consecutive_count}日
📈 累計ログボ: {total_count}回
📅 今日のログボ: {last_checkin if last_checkin else "まだありません"}
"""
        else:
            # User not found in database
            reply_text = f"""@{username} さんはまだログボデータがありません
"""

        # Send reply using Misskey API
        misskey = self.get_misskey_client()
        return misskey.create_note(text=reply_text, reply_id=note_id)
