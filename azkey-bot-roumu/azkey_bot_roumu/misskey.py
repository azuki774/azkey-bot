"""Misskey API client for azkey-bot-roumu"""


class Misskey:
    """Misskey API client class"""

    def __init__(self, misskey_url: str, i: str):
        """Initialize Misskey client

        Args:
            misskey_url: Misskey server endpoint (e.g., "https://azkey.azuki.blue")
            i: Access token for authentication

        Raises:
            ValueError: If required parameters are not provided
        """
        if not misskey_url:
            raise ValueError("misskey_url is required")
        if not i:
            raise ValueError("Access token 'i' is required")

        self.misskey_endpoint = misskey_url.rstrip('/')  # Remove trailing slash
        self.i = i
        self.headers = {
            "Content-Type": "application/json"
        }

    def get_api_url(self, endpoint_path: str) -> str:
        """Get full API URL

        Args:
            endpoint_path: API endpoint path (e.g., "/api/notes/create")

        Returns:
            Full API URL
        """
        if not endpoint_path.startswith('/'):
            endpoint_path = '/' + endpoint_path
        return f"{self.misskey_endpoint}{endpoint_path}"

    def post(self, endpoint_path: str, data: dict = None) -> dict:
        """Make POST request to Misskey API

        Args:
            endpoint_path: API endpoint path
            data: Request payload (will automatically add 'i' token)

        Returns:
            API response as dictionary

        Raises:
            requests.RequestException: If API request fails
        """
        import requests

        url = self.get_api_url(endpoint_path)

        # Prepare payload with authentication token
        payload = data.copy() if data else {}
        payload["i"] = self.i

        response = requests.post(url, headers=self.headers, json=payload)
        
        if not response.ok:
            # エラーレスポンスの詳細を含める
            try:
                error_detail = response.json()
                raise requests.RequestException(f"HTTP {response.status_code}: {error_detail}")
            except ValueError:
                # JSONパースできない場合
                raise requests.RequestException(f"HTTP {response.status_code}: {response.text}")
        
        return response.json()

    def get_followers(self, user_id: str, limit: int = 100) -> dict:
        """Get user's followers list
        
        Args:
            user_id: Target user ID to get followers
            limit: Number of followers to fetch (default: 100)
            
        Returns:
            API response containing followers list
        """
        payload = {
            "userId": user_id,
            "limit": limit
        }
        
        return self.post("/api/users/followers", payload)

    def get_following(self, user_id: str, limit: int = 100) -> dict:
        """Get user's following list
        
        Args:
            user_id: Target user ID to get following list
            limit: Number of following users to fetch (default: 100)
            
        Returns:
            API response containing following list
        """
        payload = {
            "userId": user_id,
            "limit": limit
        }
        
        return self.post("/api/users/following", payload)

    def get_my_info(self) -> dict:
        """Get current user's information
        
        Returns:
            API response containing current user's information
        """
        return self.post("/api/i")

    def follow_user(self, user_id: str) -> dict:
        """Follow a user
        
        Args:
            user_id: Target user ID to follow
            
        Returns:
            API response
        """
        payload = {
            "userId": user_id
        }
        
        return self.post("/api/following/create", payload)
