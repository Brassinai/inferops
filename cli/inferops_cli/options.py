"""Shared parser options for CLI commands."""

from __future__ import annotations

import argparse

from .kube import DEFAULT_NAMESPACE


def add_cluster_options(parser: argparse.ArgumentParser) -> None:
    """Add shared Kubernetes target options to one parser."""
    parser.add_argument(
        "--namespace",
        default=DEFAULT_NAMESPACE,
        help=f"Kubernetes namespace. Defaults to {DEFAULT_NAMESPACE}.",
    )
    parser.add_argument(
        "--context",
        help="Kubeconfig context to use for this command.",
    )
    parser.add_argument(
        "--kubeconfig",
        help="Path to the kubeconfig file to use.",
    )
    parser.add_argument(
        "--output",
        "-o",
        choices=("text", "json", "yaml"),
        default="text",
        help="Output format. Defaults to text.",
    )
