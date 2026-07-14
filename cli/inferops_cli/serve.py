"""Serve SDK web endpoints from an InferOps application file."""

from __future__ import annotations

import os

from inferops.server import EndpointApplication, GatewayRuntime, load_app, make_handler
from http.server import ThreadingHTTPServer

from .errors import ExitCode, run_with_cli_errors


def register(subcommands) -> None:
    """Register the serve command."""
    parser = subcommands.add_parser(
        "serve",
        help="Serve SDK web endpoints from an app file.",
        description=(
            "Load an InferOps application and expose @inferops.web_endpoint "
            "handlers over HTTP. The handlers call an active InferOps gateway "
            "through self.generate() and self.generate_stream()."
        ),
    )
    parser.add_argument("app", help="Path to the application file.")
    parser.add_argument(
        "--gateway-url",
        default=os.getenv("INFEROPS_GATEWAY_URL", "http://127.0.0.1:8080"),
        help="InferOps gateway base URL. Defaults to http://127.0.0.1:8080.",
    )
    parser.add_argument("--host", default="127.0.0.1", help="Host address to bind. Defaults to 127.0.0.1.")
    parser.add_argument("--port", type=int, default=9000, help="HTTP port to bind. Defaults to 9000.")
    parser.add_argument("--api-key", default=os.getenv("INFEROPS_API_KEY"), help="Bearer token for gateway auth.")
    parser.set_defaults(handler=run)


def run(args) -> int:
    """Run the serve command."""

    def action() -> int:
        if args.port < 1 or args.port > 65535:
            raise ValueError("--port must be between 1 and 65535")
        app = load_app(args.app)
        endpoint_app = EndpointApplication(
            app,
            runtime_factory=lambda _model: GatewayRuntime(gateway_url=args.gateway_url, api_key=args.api_key),
        )
        handler = make_handler(endpoint_app)
        server = ThreadingHTTPServer((args.host, args.port), handler)
        routes = ", ".join(f"{method} {path}" for method, path in endpoint_app.routes)
        print(f"Serving SDK endpoints on http://{args.host}:{args.port} ({routes}).")
        print("Press Ctrl-C to stop.")
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            return ExitCode.SUCCESS
        finally:
            server.server_close()
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)
