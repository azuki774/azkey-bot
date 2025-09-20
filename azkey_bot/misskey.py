"""Misskey API client functions"""

import requests
import os


def get_user_notes(user_id="acfu9psygqdo02op", limit=10, with_replies=True, 
                   until_id=None, since_id=None, until_date=None):
    """Get user notes from azkey.azuki.blue API with pagination support

    Args:
        user_id: User ID for the request
        limit: Number of notes to fetch (max 100)
        with_replies: Include replies
        until_id: Get notes older than this ID
        since_id: Get notes newer than this ID  
        until_date: Get notes before this date (Unix timestamp in milliseconds)

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

    # Add pagination parameters if provided
    if until_id:
        payload["untilId"] = until_id
    if since_id:
        payload["sinceId"] = since_id
    if until_date:
        payload["untilDate"] = until_date

    headers = {
        "Content-Type": "application/json"
    }

    response = requests.post(url, headers=headers, json=payload)
    response.raise_for_status()

    return response.json()


def get_all_notes_paginated(user_id="acfu9psygqdo02op", total_count=500, page_size=100):
    """Get specified number of notes using pagination

    Args:
        user_id: User ID for the request
        total_count: Total number of notes to fetch
        page_size: Notes per page (max 100)

    Returns:
        List of notes collected (up to total_count)
    """
    all_notes = []
    until_id = None
    remaining = total_count

    page = 1
    while remaining > 0:
        # Calculate how many notes to fetch in this request
        current_limit = min(remaining, page_size)

        notes = get_user_notes(
            user_id=user_id,
            limit=current_limit,
            until_id=until_id
        )

        if not notes:  # Empty response, no more notes
            break

        all_notes.extend(notes)
        until_id = notes[-1]["id"]  # Get last note ID for next page
        remaining -= len(notes)

        print(f"Page {page}: Fetched {len(notes)} notes (Total: {len(all_notes)}/{total_count})")
        page += 1

        # If we got fewer notes than requested, we've reached the end
        if len(notes) < current_limit:
            break

    return all_notes


def get_latest_notes_since(user_id="acfu9psygqdo02op", since_id=None, limit=100):
    """Get latest notes since a specific ID

    Args:
        user_id: User ID for the request
        since_id: Get notes newer than this ID
        limit: Number of notes to fetch

    Returns:
        List of new notes
    """
    return get_user_notes(
        user_id=user_id,
        limit=limit,
        since_id=since_id
    )
