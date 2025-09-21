import click

from .commands import (
    check_command,
    dakoku_command,
    follow_command,
    reset_command,
    status_command,
    timeline_command,
)


@click.group()
@click.version_option(version="0.1.0")
def cli():
    """azkey-bot-roumu - A roumu bot for azkey.azuki.blue"""
    pass


cli.add_command(status_command)
cli.add_command(follow_command)
cli.add_command(dakoku_command)
cli.add_command(timeline_command)
cli.add_command(check_command)
cli.add_command(reset_command)


if __name__ == "__main__":
    cli()
