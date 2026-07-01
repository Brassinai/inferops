"""Diagnostic contracts for doctor, gpu, and cache commands."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


class CheckStatus:
    """Status values for a doctor check."""

    PASS = "PASS"
    WARN = "WARN"
    FAIL = "FAIL"


@dataclass(frozen=True)
class DoctorCheck:
    """One diagnostic check result."""

    id: str
    status: str
    message: str
    details: dict[str, Any] = field(default_factory=dict)
    remediation: str = ""

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "status": self.status,
            "message": self.message,
            "details": self.details,
            "remediation": self.remediation,
        }


@dataclass(frozen=True)
class GPUInventory:
    """GPU inventory for one node and resource name."""

    node: str
    resource_name: str
    vendor: str
    product: str
    capacity: int
    allocatable: int
    occupied: int | None
    available: int | None

    def to_dict(self) -> dict[str, Any]:
        return {
            "node": self.node,
            "resourceName": self.resource_name,
            "vendor": self.vendor,
            "product": self.product,
            "capacity": self.capacity,
            "allocatable": self.allocatable,
            "occupied": self.occupied,
            "available": self.available,
        }


@dataclass(frozen=True)
class CacheEntry:
    """One model cache entry."""

    name: str
    phase: str
    repository: str
    revision: str
    node: str
    path: str
    size: str
    last_used: str = ""
    referenced_by: list[str] = field(default_factory=list)
    references_known: bool = True
    issues: list[str] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        return {
            "name": self.name,
            "phase": self.phase,
            "repository": self.repository,
            "revision": self.revision,
            "node": self.node,
            "path": self.path,
            "size": self.size,
            "lastUsed": self.last_used,
            "referencedBy": self.referenced_by,
            "referencesKnown": self.references_known,
            "issues": self.issues,
        }
