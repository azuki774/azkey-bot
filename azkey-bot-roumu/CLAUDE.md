# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Python project called "azkey-bot-roumu" - a roumu bot for azkey.azuki.blue. The project uses modern Python tooling with pyproject.toml for configuration.

## Development Setup

- **Python Version**: 3.13 (specified in parent project)
- **Project Structure**: Package-based structure with azkey_bot_roumu/ directory
- **Configuration**: Uses pyproject.toml for project metadata

## Running the Application

```bash
# Development mode
cd azkey-bot-roumu
python -m azkey_bot_roumu.cli --help

# After installation
azkey-bot-roumu --help
```

## Installation

```bash
cd azkey-bot-roumu
pip install -e .
```

## CLI Commands

- `azkey-bot-roumu status` - Show current status

## Project Configuration

- **pyproject.toml**: Contains project metadata, requires Python >=3.13, uses Click for CLI framework
- **Virtual Environment**: Uses parent project's .venv

## Architecture Notes

This is a roumu bot for azkey.azuki.blue with:
- Package structure: azkey_bot_roumu/ with cli.py, commands.py
- Click-based CLI framework
- Entry point: azkey_bot_roumu/cli.py (cli function)
- Basic status command implemented

## 注意事項
- 常に「Flake8 の空白行警告を修正」を行うようにしてください。
