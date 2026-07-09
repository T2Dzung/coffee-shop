#!/usr/bin/env python3
"""Vendor the pinned RabbitMQ Cluster Operator manifest into the GitOps tree.

The operator manifest is vendored so ArgoCD can sync without reaching GitHub.
The version defaults to infrastructure/ansible/playbooks/group_vars/all/versions.yml
but can be overridden with --version or RABBITMQ_CLUSTER_OPERATOR_VERSION.
"""

from __future__ import annotations

import argparse
import os
import re
import sys
import urllib.request
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
VERSIONS_FILE = PROJECT_ROOT / "infrastructure/ansible/playbooks/group_vars/all/versions.yml"
DESTINATION = PROJECT_ROOT / "infrastructure/k8s/gitops/addons/rabbitmq-operator/cluster-operator.yaml"


def read_pinned_version() -> str:
    content = VERSIONS_FILE.read_text(encoding="utf-8")
    match = re.search(
        r'^rabbitmq_cluster_operator_version:\s*["\']?v?([^"\'\s]+)',
        content,
        re.MULTILINE,
    )
    if not match:
        raise RuntimeError(f"rabbitmq_cluster_operator_version not found in {VERSIONS_FILE}")
    return match.group(1)


def download_manifest(version: str) -> bytes:
    normalized = version.removeprefix("v")
    url = (
        "https://github.com/rabbitmq/cluster-operator/releases/download/"
        f"v{normalized}/cluster-operator.yml"
    )
    print(f"Downloading RabbitMQ Cluster Operator manifest: {url}")
    with urllib.request.urlopen(url, timeout=30) as response:
        return response.read()


def validate_manifest(content: bytes, version: str) -> None:
    required_markers = [
        b"kind: CustomResourceDefinition",
        b"rabbitmqclusters.rabbitmq.com",
        f"cluster-operator:{version.removeprefix('v')}".encode(),
    ]
    missing = [marker.decode(errors="replace") for marker in required_markers if marker not in content]
    if missing:
        raise RuntimeError(f"Downloaded manifest is missing expected markers: {', '.join(missing)}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", help="RabbitMQ Cluster Operator version, with or without leading v")
    args = parser.parse_args()

    version = args.version or os.environ.get("RABBITMQ_CLUSTER_OPERATOR_VERSION") or read_pinned_version()
    version = version.removeprefix("v")

    content = download_manifest(version)
    validate_manifest(content, version)

    DESTINATION.parent.mkdir(parents=True, exist_ok=True)
    temp_path = DESTINATION.with_suffix(".yaml.tmp")
    temp_path.write_bytes(content)
    temp_path.replace(DESTINATION)
    print(f"Wrote {DESTINATION}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # noqa: BLE001 - script should print actionable CLI errors.
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
