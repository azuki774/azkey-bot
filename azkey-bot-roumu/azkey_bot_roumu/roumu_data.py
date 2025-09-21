"""Roumu data management for CSV persistence"""

import csv
import os
from datetime import datetime


class RoumuData:
    """CSV-based data storage for roumu check-in tracking"""

    def __init__(self, csv_file_path: str = "roumu.csv"):
        """Initialize RoumuData with CSV file path

        Args:
            csv_file_path: Path to the CSV file (default: "roumu.csv")
        """
        self.csv_file_path = csv_file_path
        self.fieldnames = ["user_id", "consecutive_count", "total_count", "last_checkin"]

        # Create CSV file with headers if it doesn't exist
        if not os.path.exists(self.csv_file_path):
            self._create_csv_file()

    def _create_csv_file(self):
        """Create CSV file with headers"""
        with open(self.csv_file_path, "w", newline="", encoding="utf-8") as csvfile:
            writer = csv.DictWriter(csvfile, fieldnames=self.fieldnames)
            writer.writeheader()

    def load_all_users(self) -> list[dict[str, str]]:
        """Load all user data from CSV

        Returns:
            List of user dictionaries
        """
        users = []
        try:
            with open(self.csv_file_path, newline="", encoding="utf-8") as csvfile:
                reader = csv.DictReader(csvfile)
                for row in reader:
                    if row and row.get("user_id"):  # Skip empty rows
                        # Ensure all required fields exist with default values
                        normalized_row = {
                            "user_id": row.get("user_id", ""),
                            "consecutive_count": row.get("consecutive_count", "0"),
                            "total_count": row.get("total_count", row.get("consecutive_count", "0")),
                            "last_checkin": row.get("last_checkin", ""),
                        }
                        users.append(normalized_row)
        except FileNotFoundError:
            # File doesn't exist yet, return empty list
            pass
        return users

    def get_user(self, user_id: str) -> dict[str, str] | None:
        """Get specific user data by user_id

        Args:
            user_id: Target user ID

        Returns:
            User dictionary or None if not found
        """
        users = self.load_all_users()
        for user in users:
            if user["user_id"] == user_id:
                return user
        return None

    def update_checkin(self, user_id: str) -> dict[str, any]:
        """Update user check-in data

        Args:
            user_id: User ID

        Returns:
            Dictionary with update results
        """
        # Ensure CSV file exists with proper headers
        if not os.path.exists(self.csv_file_path):
            self._create_csv_file()

        users = self.load_all_users()
        current_time = datetime.now().isoformat()

        # Check if user already checked in today
        for user in users:
            if user["user_id"] == user_id:
                if user["last_checkin"] and user["last_checkin"].strip():
                    # User already checked in, return current status without update
                    return {
                        "user_id": user_id,
                        "consecutive_count": int(user["consecutive_count"])
                        if user["consecutive_count"]
                        else 0,
                        "total_count": int(user.get("total_count", "0"))
                        if user.get("total_count")
                        else 0,
                        "last_checkin": user["last_checkin"],
                        "was_new_user": False,
                        "already_checked_in": True,
                    }
                break

        # Find existing user or create new entry
        user_found = False
        for user in users:
            if user["user_id"] == user_id:
                # Update existing user
                old_consecutive = (
                    int(user["consecutive_count"]) if user["consecutive_count"] else 0
                )
                old_total = (
                    int(user.get("total_count", "0")) if user.get("total_count") else 0
                )
                user["consecutive_count"] = str(old_consecutive + 1)
                user["total_count"] = str(old_total + 1)
                user["last_checkin"] = current_time
                user_found = True
                break

        if not user_found:
            # Add new user
            users.append(
                {
                    "user_id": user_id,
                    "consecutive_count": "1",
                    "total_count": "1",
                    "last_checkin": current_time,
                }
            )

        # Write back to CSV

        self._save_all_users(users)

        # Return result
        updated_user = self.get_user(user_id)
        return {
            "user_id": user_id,
            "consecutive_count": int(updated_user["consecutive_count"]),
            "total_count": int(updated_user.get("total_count", "0")),
            "last_checkin": updated_user["last_checkin"],
            "was_new_user": not user_found,
            "already_checked_in": False,
        }

    def reset_consecutive_count(self, user_id: str) -> bool:
        """Reset user's consecutive count to 0

        Args:
            user_id: Target user ID

        Returns:
            True if user was found and reset, False otherwise
        """
        users = self.load_all_users()

        for user in users:
            if user["user_id"] == user_id:
                user["consecutive_count"] = "0"
                user["last_checkin"] = datetime.now().isoformat()
                self._save_all_users(users)
                return True

        return False

    def reset_last_checkin(self, user_id: str = None) -> dict:
        """Reset last_checkin to empty (allows new check-in)

        Args:
            user_id: Target user ID, if None resets all users

        Returns:
            Dictionary with reset results
        """
        users = self.load_all_users()
        reset_count = 0

        if user_id is None:
            # Reset all users
            for user in users:
                if user["last_checkin"]:  # Only reset if they have a check-in
                    user["last_checkin"] = ""
                    reset_count += 1
        else:
            # Reset specific user
            for user in users:
                if user["user_id"] == user_id:
                    if user["last_checkin"]:
                        user["last_checkin"] = ""
                        reset_count = 1
                    break

        if reset_count > 0:
            self._save_all_users(users)

        return {
            "reset_count": reset_count,
            "target": "all users" if user_id is None else f"user {user_id}",
        }

    def _save_all_users(self, users: list[dict[str, str]]):
        """Save all user data to CSV

        Args:
            users: List of user dictionaries to save
        """
        with open(self.csv_file_path, "w", newline="", encoding="utf-8") as csvfile:
            writer = csv.DictWriter(csvfile, fieldnames=self.fieldnames)
            writer.writeheader()
            for user in users:
                # Ensure all required fields exist before writing
                normalized_user = {
                    "user_id": user.get("user_id", ""),
                    "consecutive_count": user.get("consecutive_count", "0"),
                    "total_count": user.get("total_count", "0"),
                    "last_checkin": user.get("last_checkin", ""),
                }
                writer.writerow(normalized_user)

    def get_leaderboard(self, limit: int = 10) -> list[dict[str, any]]:
        """Get leaderboard sorted by consecutive count

        Args:
            limit: Maximum number of users to return

        Returns:
            List of users sorted by consecutive count (descending)
        """
        users = self.load_all_users()

        # Convert consecutive_count to int for sorting
        for user in users:
            user["consecutive_count_int"] = (
                int(user["consecutive_count"]) if user["consecutive_count"] else 0
            )

        # Sort by consecutive count (descending)
        sorted_users = sorted(
            users, key=lambda x: x["consecutive_count_int"], reverse=True
        )

        return sorted_users[:limit]
