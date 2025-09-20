# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Python project called "azkey-bot" - currently a minimal Python application with a simple main.py entry point. The project uses modern Python tooling with pyproject.toml for configuration.

## Development Setup

- **Python Version**: 3.13 (specified in .python-version)
- **Project Structure**: Package-based structure with azkey_bot/ directory
- **Configuration**: Uses pyproject.toml for project metadata

## Running the Application

```bash
# Development mode
python -m azkey_bot.cli --help

# After installation
azkey-bot --help
```

## Installation

```bash
pip install -e .
```

## CLI Commands

- `azkey-bot status` - Show current status
- `azkey-bot get <key_name>` - Get a specific key
- `azkey-bot set <key_name> <key_value>` - Set a key value
- `azkey-bot list` - List all keys

## Project Configuration

- **pyproject.toml**: Contains project metadata, requires Python >=3.13, uses Click for CLI framework
- **No package manager scripts**: No build, test, or lint commands are currently defined
- **Virtual Environment**: .venv directory is gitignored, suggesting use of virtual environments

## Architecture Notes

This is a CLI tool for managing Azure keys with:
- Package structure: azkey_bot/ with cli.py, commands.py, azure.py
- Click-based CLI framework with command groups
- Entry point: azkey_bot/cli.py:7 (cli function)
- Commands: status, get, set, list with detailed docstrings and use cases
- Mock Azure client implementation for development
- Test suite using pytest and Click's testing utilities