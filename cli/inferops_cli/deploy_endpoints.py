"""Deploy SDK web endpoints as a normal Kubernetes app."""

from __future__ import annotations

from collections.abc import Sequence
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

from .app_loader import load_app
from .errors import CLIError, ExitCode, run_with_cli_errors
from .kube import EndpointAppDeployRequest, build_cluster_target, resolve_client
from .options import add_cluster_options
from .output import CommandResult, emit_result


_DNS_LABEL_RE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
_ENV_NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def register(subcommands) -> None:
    """Register the deploy-endpoints command."""
    parser = subcommands.add_parser(
        "deploy-endpoints",
        help="Deploy SDK web endpoints as a Kubernetes Deployment and Service.",
        description=(
            "Load an InferOps app, verify it declares @inferops.web_endpoint "
            "handlers, and deploy an endpoint app image that runs `inferops serve`."
        ),
    )
    parser.add_argument("app", help="Path to the local application file.")
    parser.add_argument(
        "--image",
        required=True,
        help="Endpoint app container image containing the app file and inferops CLI.",
    )
    parser.add_argument(
        "--name",
        help=(
            "Kubernetes Deployment/Service name. Defaults to <app-name>-endpoints, "
            "or <app-name> when it already ends with -endpoints."
        ),
    )
    parser.add_argument(
        "--container-app-path",
        default="/app/app.py",
        help="Path to the app file inside the endpoint image. Defaults to /app/app.py.",
    )
    parser.add_argument(
        "--gateway-url",
        help="In-cluster InferOps gateway URL. Defaults to http://inferops-gateway.<namespace>.svc.",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=8080,
        help="Endpoint app HTTP port. Defaults to 8080.",
    )
    parser.add_argument(
        "--replicas",
        type=int,
        default=1,
        help="Endpoint app replica count. Defaults to 1.",
    )
    parser.add_argument(
        "--env",
        action="append",
        default=[],
        metavar="KEY=VALUE",
        help="Additional environment variable for the endpoint app. Repeatable.",
    )
    parser.add_argument(
        "--build",
        action="store_true",
        help="Build and push the endpoint image before deploying.",
    )
    parser.add_argument(
        "--no-push",
        action="store_true",
        help="With --build, build the image but skip docker push.",
    )
    parser.add_argument(
        "--dockerfile",
        help="Dockerfile to use with --build. Defaults to examples/sdk-endpoints/Dockerfile.",
    )
    parser.add_argument(
        "--build-context",
        help="Docker build context to use with --build. Defaults to the repository root.",
    )
    parser.add_argument(
        "--app-source",
        help=(
            "App file path inside the Docker build context. Defaults to the app "
            "path relative to --build-context."
        ),
    )
    parser.add_argument(
        "--build-platform",
        help="Optional Docker build platform, for example linux/amd64 or linux/arm64.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None, runner=None) -> int:
    """Run the deploy-endpoints command."""

    def action() -> int:
        app_path = Path(args.app).expanduser().resolve()
        app = load_app(str(app_path))
        routes = _endpoint_routes(app)
        if not routes:
            raise ValueError("app does not declare any @inferops.web_endpoint handlers")
        cluster = build_cluster_target(args)
        name = _endpoint_app_name(args.name or _default_endpoint_app_name(app.name))
        gateway_url = args.gateway_url or f"http://inferops-gateway.{cluster.namespace}.svc"
        image = _required_non_empty(args.image, "--image")
        request = EndpointAppDeployRequest(
            cluster=cluster,
            name=name,
            app_path=str(app_path),
            image=image,
            container_app_path=_absolute_path(
                args.container_app_path,
                "--container-app-path",
            ),
            gateway_url=_required_non_empty(gateway_url, "--gateway-url"),
            port=_valid_port(args.port),
            replicas=_valid_replicas(args.replicas),
            env=_parse_env(args.env),
        )
        kubernetes_client = resolve_client(args, client)
        image_build = _maybe_build_endpoint_image(
            args,
            app_path,
            image,
            request.container_app_path,
            runner,
        )
        response = kubernetes_client.deploy_endpoint_app(request)
        if image_build is not None:
            response["imageBuild"] = image_build
        endpoint_app = response["endpointApp"]
        endpoint_app["routes"] = routes
        details = []
        if image_build is not None:
            details.append(f"Built image {image_build['image']}.")
            if image_build["pushed"]:
                details.append(f"Pushed image {image_build['image']}.")
        emit_result(
            args.output,
            CommandResult(
                summary=(
                    f"Deployed endpoint app {endpoint_app['name']} in namespace "
                    f"{endpoint_app['namespace']}."
                ),
                payload=response,
                details=details,
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def _endpoint_routes(app: Any) -> list[dict[str, Any]]:
    routes = []
    for model in app.models:
        for endpoint in model.endpoints:
            routes.append(
                {
                    "model": model.name,
                    "method": endpoint.method,
                    "path": endpoint.path,
                    "streaming": endpoint.streaming,
                }
            )
    return sorted(routes, key=lambda item: (item["path"], item["method"], item["model"]))


def _endpoint_app_name(value: str) -> str:
    name = value.strip()
    if len(name) > 63 or _DNS_LABEL_RE.fullmatch(name) is None:
        raise ValueError("endpoint app name must be a valid Kubernetes DNS-1123 label")
    return name


def _default_endpoint_app_name(app_name: str) -> str:
    name = app_name.strip()
    if name.endswith("-endpoints"):
        return name
    return f"{name}-endpoints"


def _required_non_empty(value: str, flag: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{flag} is required")
    return value.strip()


def _absolute_path(value: str, flag: str) -> str:
    path = _required_non_empty(value, flag)
    if not path.startswith("/"):
        raise ValueError(f"{flag} must be an absolute path inside the image")
    return path


def _valid_port(value: int) -> int:
    if value < 1 or value > 65535:
        raise ValueError("--port must be between 1 and 65535")
    return value


def _valid_replicas(value: int) -> int:
    if value < 1:
        raise ValueError("--replicas must be at least 1")
    return value


def _parse_env(values: list[str]) -> dict[str, str]:
    env: dict[str, str] = {}
    for item in values:
        key, separator, value = item.partition("=")
        if not separator:
            raise ValueError("--env values must use KEY=VALUE")
        key = key.strip()
        if _ENV_NAME_RE.fullmatch(key) is None:
            raise ValueError(f"invalid environment variable name: {key}")
        if key in {"INFEROPS_GATEWAY_URL", "PORT"}:
            raise ValueError(f"{key} is managed by inferops deploy-endpoints")
        env[key] = value
    return env


def _maybe_build_endpoint_image(
    args: Any,
    app_path: Path,
    image: str,
    container_app_path: str,
    runner,
) -> dict[str, Any] | None:
    if not args.build:
        if args.no_push:
            raise ValueError("--no-push can only be used with --build")
        return None

    build_context = _build_context_path(args.build_context)
    dockerfile = _dockerfile_path(args.dockerfile, build_context)
    app_source = _app_source(args.app_source, app_path, build_context)
    build_command = [
        "docker",
        "build",
        "-f",
        str(dockerfile),
        "--build-arg",
        f"APP_SOURCE={app_source}",
        "--build-arg",
        f"CONTAINER_APP_PATH={container_app_path}",
        "-t",
        image,
    ]
    if args.build_platform:
        build_command.extend(
            [
                "--platform",
                _required_non_empty(args.build_platform, "--build-platform"),
            ]
        )
    build_command.append(str(build_context))

    _run_docker_command(build_command, runner)
    push_command = None
    if not args.no_push:
        push_command = ["docker", "push", image]
        _run_docker_command(push_command, runner)

    return {
        "image": image,
        "built": True,
        "pushed": not args.no_push,
        "dockerfile": str(dockerfile),
        "buildContext": str(build_context),
        "appSource": app_source,
        "commands": {
            "build": build_command,
            "push": push_command,
        },
    }


def _build_context_path(value: str | None) -> Path:
    if value:
        path = Path(value).expanduser().resolve()
    else:
        path = _repo_root()
    if not path.exists() or not path.is_dir():
        raise ValueError(f"--build-context must be an existing directory: {path}")
    return path


def _dockerfile_path(value: str | None, build_context: Path) -> Path:
    if value:
        path = Path(value).expanduser().resolve()
    else:
        path = build_context / "examples" / "sdk-endpoints" / "Dockerfile"
    if not path.exists() or not path.is_file():
        raise ValueError(f"--dockerfile must be an existing file: {path}")
    return path


def _app_source(value: str | None, app_path: Path, build_context: Path) -> str:
    if value:
        source = value.strip()
        if not source:
            raise ValueError("--app-source cannot be empty")
        if source.startswith("/") or ".." in Path(source).parts:
            raise ValueError("--app-source must be a relative path inside --build-context")
        if not (build_context / source).exists():
            raise ValueError(f"--app-source does not exist inside --build-context: {source}")
        return source
    try:
        return app_path.relative_to(build_context).as_posix()
    except ValueError as exc:
        raise ValueError(
            "app must be inside --build-context when using --build; pass "
            "--build-context or --app-source to choose the file copied into the image"
        ) from exc


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def _run_docker_command(command: Sequence[str], runner) -> None:
    try:
        completed = (runner or _run_command)(command)
    except FileNotFoundError as exc:
        raise CLIError("docker is required for --build but was not found on PATH") from exc
    except subprocess.CalledProcessError as exc:
        executable = command[0] if command else "command"
        raise CLIError(f"{executable} command failed with exit code {exc.returncode}") from exc
    if getattr(completed, "returncode", 0) != 0:
        executable = command[0] if command else "command"
        raise CLIError(f"{executable} command failed with exit code {completed.returncode}")


def _run_command(command: Sequence[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        list(command),
        text=True,
        stdout=sys.stderr,
        stderr=sys.stderr,
        check=True,
    )
