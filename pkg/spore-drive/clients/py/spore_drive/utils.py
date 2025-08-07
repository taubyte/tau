"""
Utility functions for Spore Drive.

This module contains utility functions for tau binary sources
and service management convenience functions.
"""

from typing import Optional
from .service_manager import start_service, stop_service, get_existing_service_port
from .types import TauBinarySource


# Convenience functions for tau binary sources
def tau_latest() -> TauBinarySource:
    """Get latest tau binary source."""
    return TauBinarySource.latest()


def tau_version(version: str) -> TauBinarySource:
    """Get tau binary source by version."""
    return TauBinarySource.version(version)


def tau_url(url: str) -> TauBinarySource:
    """Get tau binary source by URL."""
    return TauBinarySource.url(url)


def tau_path(path: str) -> TauBinarySource:
    """Get tau binary source by path."""
    return TauBinarySource.path(path)


# Service management convenience functions
def start_spore_drive_service() -> int:
    """Start the Spore Drive service and return the port."""
    return start_service()


def stop_spore_drive_service() -> None:
    """Stop the Spore Drive service."""
    stop_service()


def get_spore_drive_service_port() -> Optional[int]:
    """Get the port of an existing Spore Drive service if one is running."""
    return get_existing_service_port() 