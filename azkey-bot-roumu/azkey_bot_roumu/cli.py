import click

from .commands import dakoku_command, follow_command, status_command


@click.group()
@click.version_option(version="0.1.0")
def cli():
    """azkey-bot-roumu - A roumu bot for azkey.azuki.blue"""
    pass


cli.add_command(status_command)
cli.add_command(follow_command)
cli.add_command(dakoku_command)


if __name__ == "__main__":
    cli()
