# azkey-bot Projects

This repository contains multiple bot projects for azkey.azuki.blue.

## Projects

### azkey-bot
CLI tool for managing Azure keys and analyzing Misskey posts.

**Features:**
- User post analysis with AI evaluation
- Next post generation based on posting patterns
- Random post generation
- Direct posting to Misskey

**Usage:**
```bash
cd azkey-bot
uv run python -m azkey_bot.cli analyze --total-count=500 --post
uv run python -m azkey_bot.cli next --post
```

### azkey-bot-roumu
Roumu bot for azkey.azuki.blue.

**Features:**
- (To be implemented)

**Usage:**
```bash
cd azkey-bot-roumu
uv run python -m azkey_bot_roumu.cli status
```

## Development

Each project has its own:
- `pyproject.toml` - Dependencies and configuration
- `README.md` - Project-specific documentation
- `CLAUDE.md` - Claude Code guidance

## Environment Variables

```bash
export i='YOUR_MISSKEY_ACCESS_TOKEN'
export OPENROUTER_API_KEY='YOUR_OPENROUTER_KEY'
```

## Installation

```bash
# Install specific project
cd azkey-bot
pip install -e .

cd azkey-bot-roumu
pip install -e .
```
