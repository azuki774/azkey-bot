from http.server import BaseHTTPRequestHandler

from .usecases import Usecases


def create_http_handler(csv_dir, logger):
    """Factory function to create HTTP request handler with injected dependencies"""

    class HTTPRequestHandler(BaseHTTPRequestHandler):
        def do_GET(self):
            """Handle GET requests for various endpoints"""
            if self.path == "/reset":
                self._handle_reset()
            else:
                self.send_response(404)
                self.end_headers()

        def _handle_reset(self):
            """Handle /reset endpoint - execute reset_command logic"""
            try:
                logger.info(
                    f'action=http_reset_start remote_addr={self.client_address[0]} message="HTTP reset request received"'
                )

                # Execute reset logic (same as reset_command)
                usecases = Usecases(csv_dir=csv_dir)
                result = usecases.reset_count()

                # Log results
                logger.info(
                    f"action=http_reset_complete total_users={result['total_users']} "
                    f"consecutive_count_reset={result['consecutive_count_reset']} "
                    f"last_checkin_reset={result['last_checkin_reset']} "
                    f'message="{result["message"]}"'
                )

                # Send 200 OK response with empty body
                self.send_response(200)
                self.end_headers()

            except Exception as e:
                logger.error(f'action=http_reset_error error="{e}"')
                self.send_response(500)
                self.end_headers()

        def log_message(self, _format, *_args):
            """Suppress default HTTP server logs"""
            pass

    return HTTPRequestHandler
