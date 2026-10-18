"""
Drive management for Spore Drive.

This module contains the Drive and Course classes that provide
a pythonic interface to manage drives and their associated operations.
"""

from typing import Optional, AsyncIterator, Union
from .proto.drive.v1 import drive_pb2
from .clients import DriveClient
from .types import TauBinarySource, CourseConfig, TauBinarySourceLegacy, CourseConfigLegacy
from .config import Config
from .service_manager import get_service_manager, start_service


class Drive:
    """
    Drive management for Spore Drive.
    
    This class provides a pythonic interface to manage drives
    and their associated operations.
    """
    
    def __init__(self, config: Config, tau: Optional[Union[TauBinarySource, TauBinarySourceLegacy]] = None):
        """
        Initialize drive.
        
        Args:
            config: Configuration for the drive
            tau: Optional tau binary source specification
        """
        self._config = config
        self._tau = self._normalize_tau_source(tau)
        self._client: Optional[DriveClient] = None
        self._drive: Optional[drive_pb2.Drive] = None
        self._service_manager = get_service_manager()
    
    def _normalize_tau_source(self, tau: Optional[Union[TauBinarySource, TauBinarySourceLegacy]]) -> Optional[TauBinarySource]:
        """Normalize tau source to use the new dataclass format."""
        if tau is None:
            return None
        elif isinstance(tau, TauBinarySource):
            return tau
        elif tau is True:
            return TauBinarySource.latest()
        elif isinstance(tau, str):
            # Determine if it's a version, URL, or path
            if tau.startswith(('http://', 'https://')):
                return TauBinarySource.url(tau)
            elif '/' in tau or tau.endswith(('.tar', '.zip', '.gz')):
                return TauBinarySource.path(tau)
            else:
                return TauBinarySource.version(tau)
        else:
            raise ValueError(f"Invalid tau source type: {type(tau)}")
    
    @classmethod
    async def create(cls, config: Config, tau: Optional[Union[TauBinarySource, TauBinarySourceLegacy]] = None, url: Optional[str] = None) -> 'Drive':
        """
        Factory method to create and initialize a drive.
        
        Args:
            config: Configuration for the drive
            tau: Optional tau binary source specification
            url: Optional service URL
            
        Returns:
            Initialized Drive instance
        """
        drive = cls(config, tau)
        await drive.init(url)
        return drive
    
    @classmethod
    async def with_latest_tau(cls, config: Config, url: Optional[str] = None) -> 'Drive':
        """
        Factory method to create a drive with the latest tau binary.
        
        Args:
            config: Configuration for the drive
            url: Optional service URL
            
        Returns:
            Initialized Drive instance
        """
        return await cls.create(config, tau=TauBinarySource.latest(), url=url)
    
    @classmethod
    async def with_version(cls, config: Config, version: str, url: Optional[str] = None) -> 'Drive':
        """
        Factory method to create a drive with a specific tau version.
        
        Args:
            config: Configuration for the drive
            version: Tau version string
            url: Optional service URL
            
        Returns:
            Initialized Drive instance
        """
        return await cls.create(config, tau=TauBinarySource.version(version), url=url)
    
    @classmethod
    async def with_url(cls, config: Config, tau_url: str, url: Optional[str] = None) -> 'Drive':
        """
        Factory method to create a drive with tau from URL.
        
        Args:
            config: Configuration for the drive
            tau_url: URL to tau binary
            url: Optional service URL
            
        Returns:
            Initialized Drive instance
        """
        return await cls.create(config, tau=TauBinarySource.url(tau_url), url=url)
    
    @classmethod
    async def with_path(cls, config: Config, tau_path: str, url: Optional[str] = None) -> 'Drive':
        """
        Factory method to create a drive with tau from local path.
        
        Args:
            config: Configuration for the drive
            tau_path: Local path to tau binary
            url: Optional service URL
            
        Returns:
            Initialized Drive instance
        """
        return await cls.create(config, tau=TauBinarySource.path(tau_path), url=url)
    
    async def __aenter__(self):
        """Async context manager entry."""
        await self.init()
        return self
    
    async def __aexit__(self, exc_type, exc_val, exc_tb):
        """Async context manager exit with automatic cleanup."""
        await self.free()
    
    async def init(self, url: Optional[str] = None) -> None:
        """
        Initialize the drive.
        
        Args:
            url: Optional service URL, defaults to localhost with service port
        """
        if url is None:
            # Start service and get port
            port = start_service()
            url = f"http://localhost:{port}/"
        
        self._client = DriveClient(url)
        
        # Create drive request
        request = drive_pb2.DriveRequest()
        request.config.CopyFrom(self._config._config)
        
        # Set tau binary source
        if self._tau is not None:
            if self._tau.source_type.value == "latest":
                request.latest = True
            elif self._tau.source_type.value == "version":
                request.version = self._tau.value
            elif self._tau.source_type.value == "url":
                request.url = self._tau.value
            elif self._tau.source_type.value == "path":
                request.path = self._tau.value
        
        self._drive = await self._client.new(request)
    
    async def free(self) -> None:
        """Free the drive resources."""
        if self._drive and self._client:
            await self._client.free(self._drive)
    
    async def plot(self, config: Union[CourseConfig, CourseConfigLegacy]) -> 'Course':
        """
        Plot a course for displacement.
        
        Args:
            config: Course configuration with shapes and concurrency
            
        Returns:
            Course object
        """
        if not self._drive or not self._client:
            raise RuntimeError("Drive not initialized")
        
        # Normalize config to CourseConfig dataclass
        if isinstance(config, dict):
            course_config = CourseConfig.from_dict(config)
        else:
            course_config = config
        
        request = drive_pb2.PlotRequest()
        request.drive.CopyFrom(self._drive)
        request.shapes.extend(course_config.shapes)
        request.concurrency = course_config.concurrency
        
        course = await self._client.plot(request)
        return Course(self._client, self._drive, course, course_config)


class Course:
    """
    Course management for Spore Drive.
    
    This class provides a pythonic interface to manage courses
    and their displacement operations.
    """
    
    def __init__(self, client: DriveClient, drive: drive_pb2.Drive, 
                 course: drive_pb2.Course, config: CourseConfig):
        """
        Initialize course.
        
        Args:
            client: Drive client
            drive: Drive object
            course: Course object
            config: Course configuration
        """
        self._client = client
        self._drive = drive
        self._course = course
        self._config = config
    
    async def __aenter__(self):
        """Async context manager entry."""
        return self
    
    async def __aexit__(self, exc_type, exc_val, exc_tb):
        """Async context manager exit with automatic cleanup."""
        await self.abort()
    
    async def displace(self) -> None:
        """Start displacement of the course."""
        await self._client.displace(self._course)
    
    async def progress(self) -> AsyncIterator[drive_pb2.DisplacementProgress]:
        """
        Get progress updates for the course.
        
        Yields:
            Progress updates
        """
        async for progress in self._client.progress(self._course):
            yield progress
    
    async def abort(self) -> None:
        """Abort the course."""
        await self._client.abort(self._course) 