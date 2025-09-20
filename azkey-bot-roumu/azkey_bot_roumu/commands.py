import click


@click.command("status")
def status_command():
    """Show current status"""
    click.echo("azkey-bot-roumu is running!")