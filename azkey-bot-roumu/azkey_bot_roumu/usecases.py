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
        self.misskey_endpoint = "https://azkey.azuki.blue"

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

    def reset_roumu_count(self, user_id: str) -> bool:
        """Reset user's consecutive roumu count

        Args:
            user_id: Target user ID

        Returns:
            True if reset successful, False if user not found
        """
        return self.roumu_data.reset_consecutive_count(user_id)

    def reset_roumu_checkin(self, user_id: str = None) -> dict:
        """Reset user's last check-in (allows new check-in)

        Args:
            user_id: Target user ID, if None resets all users

        Returns:
            Dictionary with reset results
        """
        return self.roumu_data.reset_last_checkin(user_id)

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
