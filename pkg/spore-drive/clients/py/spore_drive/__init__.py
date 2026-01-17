"""
Spore Drive Python client library.

This package provides a pythonic interface to the Spore Drive service,
implementing all RPC calls available in the TypeScript client.
"""

from .types import TauBinarySource, CourseConfig, ServiceConfig, TauSourceType
from .config import Config
from .drive import Drive, Course
from .utils import (
    tau_latest,
    tau_version,
    tau_url,
    tau_path,
    start_spore_drive_service,
    stop_spore_drive_service,
    get_spore_drive_service_port
)

__all__ = [
    'Config',
    'Drive',
    'Course',
    'TauBinarySource',
    'CourseConfig',
    'ServiceConfig',
    'TauSourceType',
    'tau_latest',
    'tau_version',
    'tau_url',
    'tau_path',
    'start_spore_drive_service',
    'stop_spore_drive_service',
    'get_spore_drive_service_port',
]

# Version information
__version__ = "0.1.0" 