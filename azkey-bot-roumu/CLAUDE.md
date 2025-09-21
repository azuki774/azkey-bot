# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

azkey-bot-roumu is a "roumu" (login bonus) bot for azkey.azuki.blue Misskey server. It functions as an SNS login bonus bot that tracks daily check-ins and manages follow-back automation.

## Development Setup

- **Python Version**: 3.13+ (specified in pyproject.toml)
- **Package Manager**: uv (preferred) or pip
- **Package Structure**: azkey_bot_roumu/ with modular architecture
- **Configuration**: Uses pyproject.toml, no build/test/lint scripts defined

## Running the Application

```bash
# Development mode with uv
uv run python -m azkey_bot_roumu.cli <command>

# After installation
pip install -e .
azkey-bot-roumu <command>
```

## CLI Commands

- `azkey-bot-roumu status` - Show current status
- `azkey-bot-roumu follow` - Execute follow-back automation for Misskey followers
- `azkey-bot-roumu check` - Monitor timeline for keywords and perform automatic check-ins
- `azkey-bot-roumu reset` - Reset all users' count based on current state
- `azkey-bot-roumu dakoku --user-id <id>` - Debug command for manual user check-in
- `azkey-bot-roumu timeline --limit <n>` - Display recent timeline posts

## Architecture

### Core Components

**Usecases Class** (`usecases.py`): Central business logic coordinator that manages configuration, integrates all services, and provides high-level operations. Acts as the main entry point for commands.

**Misskey API Client** (`misskey.py`): Handles all Misskey server communication including follow/unfollow operations, user information retrieval, and notification management.

**RoumuData** (`roumu_data.py`): CSV-based persistence layer for check-in tracking with schema: `user_id`, `consecutive_count`, `total_count`, `last_checkin`. Implements duplicate check-in prevention, leaderboard functionality, and count reset operations.

### Data Flow Architecture

1. **Commands** → **Usecases** → **Misskey API / RoumuData**
2. Configuration loaded from environment variables (`i`, `OPENROUTER_API_KEY`)
3. CSV file (`roumu.csv`) automatically created and managed for persistence
4. Follow-back logic: fetch followers/following → calculate diff → execute follows

### Key Features

- **Follow-back Automation**: Automatically follows users who follow you but aren't followed back
- **Check-in System**: Daily login bonus tracking with consecutive count, total count and duplicate prevention
- **Timeline Monitoring**: Automatic keyword detection and check-in processing
- **Count Reset System**: Conditional reset logic based on last check-in status
- **Automatic Reactions**: Adds 👍 reactions to posts that trigger successful check-ins
- **Username Resolution**: Automatic username lookup from user IDs via Misskey API
- **CSV Persistence**: Lightweight data storage for user check-in records
- **Structured Logging**: Server-friendly logging for production deployment

## Environment Variables

Required for API operations:
- `i`: Misskey access token
- `OPENROUTER_API_KEY`: OpenRouter API key (for future AI features)

Optional:
- `ROUMU_DATA_DIR`: Directory path for CSV file storage (default: current directory)
- `MISSKEY_ENDPOINT`: Misskey server endpoint (default: https://azkey.azuki.blue)

## Code Quality

This project uses Ruff for linting and formatting. Install and run:

```bash
# Install dev dependencies
uv add --dev ruff

# Check code quality
uv run ruff check

# Auto-fix issues
uv run ruff check --fix

# Format code
uv run ruff format

# Check and fix everything
uv run ruff check --fix && uv run ruff format
```

## CI/CD

GitHub Actions automatically runs code quality checks on every push and pull request:

- **Ruff Linter**: Checks code style, imports, type annotations, and best practices
- **Ruff Formatter**: Ensures consistent code formatting
- **Python 3.13**: Tests against the target Python version

The workflow fails if any linting errors or formatting issues are detected. Run `uv run ruff check --fix && uv run ruff format` locally before pushing to ensure CI passes.

## Data Schema

CSV file structure in `roumu.csv`:
- `user_id`: Misskey user identifier
- `consecutive_count`: Number of consecutive check-ins
- `total_count`: Cumulative total number of check-ins
- `last_checkin`: ISO timestamp of last check-in (empty = can check-in)

## Docker Deployment

The application supports Docker deployment for server environments:

```bash
# Quick start with Makefile
make quick-start          # Setup data directory, build image
make status               # Check status
make follow               # Execute follow-back
make check                # Execute timeline monitoring
make reset                # Reset all users' counts

# Or manual Docker commands
docker build --no-cache -t azkey-bot-roumu .
docker run --env-file .env -e ROUMU_DATA_DIR=/app/data -v $(pwd)/data:/app/data azkey-bot-roumu check

# Docker Compose for services
make compose-up-nocache   # Build and start services
make compose-down         # Stop services

# Automated scheduling with cron
docker build -f Dockerfile.scheduler -t azkey-bot-roumu:scheduler .
docker run -d --env-file .env -v $(pwd)/data:/app/data azkey-bot-roumu:scheduler
```

See `DOCKER.md` for detailed deployment instructions.

**Production scheduling:**
- **follow**: Every 1 hour via cron (`0 * * * *`)
- **check**: Every 5 minutes via cron (`*/5 * * * *`)
- **reset**: Manual execution (e.g., monthly reset)
- Structured logs output to stdout for log aggregation systems