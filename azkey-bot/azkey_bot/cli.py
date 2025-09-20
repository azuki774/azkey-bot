import click

from .commands import status_command, analyze_command, next_command, random_command


@click.group()
@click.version_option(version="0.1.0")
def cli():
    """azkey-bot - A CLI tool"""
    pass


cli.add_command(status_command)
cli.add_command(analyze_command)
cli.add_command(next_command)
cli.add_command(random_command)


if __name__ == "__main__":
    cli()
