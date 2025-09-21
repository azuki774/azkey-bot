"""Structured logging configuration for azkey-bot-roumu"""

import logging
import sys
from typing import Any


class StructuredFormatter(logging.Formatter):
    """Custom formatter for structured logging"""

    def format(self, record: logging.LogRecord) -> str:
        # Base log entry
        log_entry = {
            "timestamp": self.formatTime(record),
            "level": record.levelname,
            "component": record.name,
            "message": record.getMessage(),
        }

        # Add extra fields if available
        if hasattr(record, "extra_data") and record.extra_data:
            log_entry.update(record.extra_data)

        # Format as key=value pairs for easy parsing
        formatted_parts = []
        for key, value in log_entry.items():
            if isinstance(value, str) and " " in value:
                formatted_parts.append(f'{key}="{value}"')
            else:
                formatted_parts.append(f"{key}={value}")

        return " ".join(formatted_parts)


def setup_logger(name: str) -> logging.Logger:
    """Setup structured logger

    Args:
        name: Logger name (typically module name)

    Returns:
        Configured logger instance
    """
    logger = logging.getLogger(name)

    if not logger.handlers:  # Avoid duplicate handlers
        handler = logging.StreamHandler(sys.stdout)
        handler.setFormatter(StructuredFormatter())
        logger.addHandler(handler)
        logger.setLevel(logging.INFO)

    return logger


def log_with_data(
    logger: logging.Logger, level: str, message: str, **kwargs: Any
) -> None:
    """Log message with structured data

    Args:
        logger: Logger instance
        level: Log level (info, warning, error, etc.)
        message: Log message
        **kwargs: Additional structured data
    """
    # Create a LogRecord with extra data
    record = logger.makeRecord(
        logger.name, getattr(logging, level.upper()), __file__, 0, message, (), None
    )
    record.extra_data = kwargs

    logger.handle(record)
