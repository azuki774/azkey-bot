import click

from .commands import status_command, get_command


@click.group()
@click.version_option(version="0.1.0")
def cli():
    """azkey-bot - A CLI tool"""
    pass


cli.add_command(status_command)
cli.add_command(get_command)


if __name__ == "__main__":
    cli()
