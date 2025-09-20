"""Misskey API client functions"""

import requests
import os


def get_user_notes(user_id="acfu9psygqdo02op", limit=10, with_replies=True):
    """Get user notes from azkey.azuki.blue API
    
    Args:
        user_id: User ID for the request
        limit: Number of notes to fetch
        with_replies: Include replies
        
    Returns:
        JSON response data
        
    Raises:
        ValueError: If access token is not set
        requests.RequestException: If API request fails
    """
    access_token = os.getenv("i")
    if not access_token:
        raise ValueError("Environment variable 'i' is not set")

    url = "https://azkey.azuki.blue/api/users/notes"

    payload = {
        "userId": user_id,
        "limit": limit,
        "withReplies": with_replies,
        "i": access_token
    }

    headers = {
        "Content-Type": "application/json"
    }

    response = requests.post(url, headers=headers, json=payload)
    response.raise_for_status()
    
    return response.json()