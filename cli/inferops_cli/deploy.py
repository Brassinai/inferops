"""Deploy command."""

from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path

from inferops import build_manifests

from .app_loader import load_app
from .errors import ExitCode, NotFoundError, run_with_cli_errors
from .kube import DeployRequest, StatusRequest, build_cluster_target, resolve_client
from .lifecycle import ACTIVATION_POLICIES
from .options import add_cluster_options
from .output import CommandResult, emit_result
from .state import load_state, save_state


def register(subcommands) -> None:
    """Register the deploy command."""
    parser = subcommands.add_parser(
        "deploy",
        help="Deploy an application file.",
        description="Load an InferOps application, generate ModelDeployment manifests, and apply them to Kubernetes.",
    )
    parser.add_argument("app", help="Path to the application file.")
    parser.add_argument(
        "--activate",
        action="store_true",
        help="Request activation after preparing the deployment.",
    )
    parser.add_argument(
        "--when-full",
        choices=ACTIVATION_POLICIES,
        help="Optional replacement policy to use with --activate.",
    )
    add_cluster_options(parser)
    parser.set_defaults(handler=run)


def run(args, client=None) -> int:
    """Run the deploy command."""

    def action() -> int:
        app_path = Path(args.app).expanduser().resolve()
        app = load_app(str(app_path))
        manifests = build_manifests(app)
        cluster = build_cluster_target(args)
        project_dir = app_path.parent

        activate_requested = bool(getattr(args, "activate", False))
        when_full = getattr(args, "when_full", None)

        state = load_state(project_dir)
        deployments_state = state.setdefault("deployments", {})

        k8s_client = resolve_client(args, client)

        results: list[dict] = []
        changed = False

        for manifest in manifests:
            name = manifest["metadata"]["name"]
            applied_manifest = copy.deepcopy(manifest)

            if activate_requested:
                applied_manifest["spec"]["activation"]["desiredState"] = "Active"
            if when_full:
                applied_manifest["spec"]["activation"]["whenFull"] = when_full

            spec_hash = _hash_manifest(applied_manifest)
            state_key = f"{cluster.namespace}/{name}"
            stored = deployments_state.get(state_key)
            if (
                stored
                and stored.get("last_applied_hash") == spec_hash
                and _deployment_exists(k8s_client, cluster, name)
            ):
                results.append(
                    {
                        "name": name,
                        "namespace": cluster.namespace,
                        "phase": applied_manifest["spec"]["activation"]["desiredState"],
                        "action": "unchanged",
                    }
                )
                continue

            response = k8s_client.deploy(
                DeployRequest(
                    cluster=cluster,
                    app_path=str(app_path),
                    manifests=[applied_manifest],
                    activate=False,
                    when_full=None,
                )
            )
            deployment = response["deployments"][0]

            deployments_state[state_key] = {
                "last_applied_hash": spec_hash,
                "name": name,
                "namespace": cluster.namespace,
                "app_path": str(app_path),
            }
            deployment.setdefault("action", "applied")
            results.append(deployment)
            changed = True

            save_state(state, project_dir)

        names = ", ".join(r["name"] for r in results)
        if not changed:
            summary = f"No changes for {names} in namespace {cluster.namespace}."
        else:
            summary = f"Deployed {names} in namespace {cluster.namespace}."

        emit_result(
            args.output,
            CommandResult(
                summary=summary,
                payload={"deployments": results, "cluster": cluster.to_safe_dict()},
            ),
        )
        return ExitCode.SUCCESS

    return run_with_cli_errors(action)


def _hash_manifest(manifest: dict) -> str:
    """Compute a deterministic hash of the manifest spec for idempotency."""
    spec = manifest.get("spec", {})
    canonical = json.dumps(spec, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _deployment_exists(k8s_client, cluster, name: str) -> bool:
    try:
        k8s_client.status(StatusRequest(cluster=cluster, name=name))
    except NotFoundError:
        return False
    return True
