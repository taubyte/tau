"""
Type definitions for Spore Drive Python client.

This module contains type aliases and common types used throughout
the Spore Drive client implementation.
"""

from typing import Union, Dict, Any, List, Optional
from dataclasses import dataclass, field
from enum import Enum


class TauSourceType(Enum):
    """Enumeration for tau binary source types."""
    LATEST = "latest"
    VERSION = "version"
    URL = "url"
    PATH = "path"


@dataclass
class TauBinarySource:
    """
    Configuration for tau binary source.
    
    Attributes:
        source_type: Type of source (latest, version, url, path)
        value: The actual value (version string, URL, or path)
    """
    source_type: TauSourceType
    value: Optional[str] = None
    
    @classmethod
    def latest(cls) -> 'TauBinarySource':
        """Create a latest tau binary source."""
        return cls(TauSourceType.LATEST)
    
    @classmethod
    def version(cls, version: str) -> 'TauBinarySource':
        """Create a version-specific tau binary source."""
        return cls(TauSourceType.VERSION, version)
    
    @classmethod
    def url(cls, url: str) -> 'TauBinarySource':
        """Create a URL-based tau binary source."""
        return cls(TauSourceType.URL, url)
    
    @classmethod
    def path(cls, path: str) -> 'TauBinarySource':
        """Create a path-based tau binary source."""
        return cls(TauSourceType.PATH, path)


@dataclass
class CourseConfig:
    """
    Configuration for course operations.
    
    Attributes:
        shapes: List of shape names to include in the course
        concurrency: Number of concurrent operations (default: 1)
        timeout: Optional timeout in seconds
        retries: Number of retry attempts (default: 3)
    """
    shapes: List[str] = field(default_factory=list)
    concurrency: int = 1
    timeout: Optional[int] = None
    retries: int = 3
    
    def __post_init__(self):
        """Validate configuration after initialization."""
        if self.concurrency < 1:
            raise ValueError("Concurrency must be at least 1")
        if self.retries < 0:
            raise ValueError("Retries must be non-negative")
        if self.timeout is not None and self.timeout <= 0:
            raise ValueError("Timeout must be positive if specified")
    
    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> 'CourseConfig':
        """Create CourseConfig from dictionary."""
        return cls(
            shapes=data.get('shapes', []),
            concurrency=data.get('concurrency', 1),
            timeout=data.get('timeout'),
            retries=data.get('retries', 3)
        )
    
    def to_dict(self) -> Dict[str, Any]:
        """Convert to dictionary."""
        result = {
            'shapes': self.shapes,
            'concurrency': self.concurrency,
            'retries': self.retries
        }
        if self.timeout is not None:
            result['timeout'] = self.timeout
        return result


@dataclass
class ServiceConfig:
    """
    Configuration for service management.
    
    Attributes:
        host: Service host (default: localhost)
        port: Service port (default: auto-assigned)
        timeout: Connection timeout in seconds (default: 30)
        retries: Number of retry attempts (default: 3)
    """
    host: str = "localhost"
    port: Optional[int] = None
    timeout: int = 30
    retries: int = 3
    
    def __post_init__(self):
        """Validate configuration after initialization."""
        if self.timeout <= 0:
            raise ValueError("Timeout must be positive")
        if self.retries < 0:
            raise ValueError("Retries must be non-negative")
        if self.port is not None and (self.port < 1 or self.port > 65535):
            raise ValueError("Port must be between 1 and 65535")


# Backward compatibility aliases
TauBinarySourceLegacy = Union[bool, str]  # For backward compatibility
CourseConfigLegacy = Dict[str, Any]  # For backward compatibility 