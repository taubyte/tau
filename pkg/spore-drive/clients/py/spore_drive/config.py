"""
Configuration management for Spore Drive.

This module contains the Config class that provides a pythonic interface
to manage configuration including cloud settings, hosts, authentication, and shapes.
"""

from typing import Optional, Union, AsyncIterator
from .proto.config.v1 import config_pb2
from .clients import ConfigClient
from .operations import Cloud, Hosts, Auth, Shapes
from .service_manager import get_service_manager, start_service


class Config:
    """
    Configuration management for Spore Drive.
    
    This class provides a pythonic interface to manage configuration
    including cloud settings, hosts, authentication, and shapes.
    """
    
    def __init__(self, source: Optional[Union[str, bytes]] = None):
        """
        Initialize configuration.
        
        Args:
            source: Optional source path (directory) or archive data for configuration
        """
        self._source = source
        self._client: Optional[ConfigClient] = None
        self._config: Optional[config_pb2.Config] = None
        self._service_manager = get_service_manager()
    
    @classmethod
    async def create(cls, source: Optional[Union[str, bytes]] = None, url: Optional[str] = None) -> 'Config':
        """
        Factory method to create and initialize a configuration.
        
        Args:
            source: Optional source path (directory) or archive data for configuration
            url: Optional service URL
            
        Returns:
            Initialized Config instance
        """
        config = cls(source)
        await config.init(url)
        return config
    
    @classmethod
    async def from_directory(cls, directory_path: str, url: Optional[str] = None) -> 'Config':
        """
        Factory method to create configuration from a directory.
        
        Args:
            directory_path: Path to configuration directory
            url: Optional service URL
            
        Returns:
            Initialized Config instance
        """
        import os
        if not os.path.exists(directory_path):
            raise FileNotFoundError(f"Configuration directory does not exist: {directory_path}")
        if not os.path.isdir(directory_path):
            raise NotADirectoryError(f"Path is not a directory: {directory_path}")
        
        return await cls.create(source=directory_path, url=url)
    
    @classmethod
    async def from_archive(cls, archive_data: bytes, url: Optional[str] = None) -> 'Config':
        """
        Factory method to create configuration from archive data.
        
        Args:
            archive_data: Configuration archive data as bytes
            url: Optional service URL
            
        Returns:
            Initialized Config instance
        """
        return await cls.create(source=archive_data, url=url)
    
    @classmethod
    async def new(cls, url: Optional[str] = None) -> 'Config':
        """
        Factory method to create a new empty configuration.
        
        Args:
            url: Optional service URL
            
        Returns:
            Initialized Config instance
        """
        return await cls.create(url=url)
    
    async def __aenter__(self):
        """Async context manager entry."""
        if not self._config:
            await self.init()
        return self
    
    async def __aexit__(self, exc_type, exc_val, exc_tb):
        """Async context manager exit with automatic cleanup."""
        await self.free()
    
    async def init(self, url: Optional[str] = None) -> None:
        """
        Initialize the configuration.
        
        Args:
            url: Optional service URL, defaults to localhost with service port
        """
        if url is None:
            # Start service and get port
            port = start_service()
            url = f"http://localhost:{port}/"
        
        self._client = ConfigClient(url)
        
        if isinstance(self._source, str):
            # Load from directory path (root is the directory, path is "/")
            source = config_pb2.Source(root=self._source, path="/")
            self._config = await self._client.load(source)
        elif isinstance(self._source, bytes):
            # Load from archive data using upload method
            async def upload_stream():
                yield config_pb2.SourceUpload(chunk=self._source)
            
            self._config = await self._client.upload(upload_stream())
        else:
            # Create new config
            self._config = await self._client.new()
    
    async def free(self) -> None:
        """Free the configuration resources."""
        if self._config and self._client:
            await self._client.free(self._config)
    
    @property
    def id(self) -> Optional[str]:
        """Get the configuration ID."""
        return self._config.id if self._config else None
    
    @property
    def cloud(self) -> Cloud:
        """Get cloud configuration interface."""
        if not self._config or not self._client:
            raise RuntimeError("Config not initialized")
        return Cloud(self._client, self._config)
    
    @property
    def hosts(self) -> Hosts:
        """Get hosts configuration interface."""
        if not self._config or not self._client:
            raise RuntimeError("Config not initialized")
        return Hosts(self._client, self._config)
    
    @property
    def auth(self) -> Auth:
        """Get authentication configuration interface."""
        if not self._config or not self._client:
            raise RuntimeError("Config not initialized")
        return Auth(self._client, self._config)
    
    @property
    def shapes(self) -> Shapes:
        """Get shapes configuration interface."""
        if not self._config or not self._client:
            raise RuntimeError("Config not initialized")
        return Shapes(self._client, self._config)
    
    async def commit(self) -> config_pb2.Empty:
        """Commit configuration changes."""
        if not self._config or not self._client:
            raise RuntimeError("Config not initialized")
        return await self._client.commit(self._config)
    
    async def save(self) -> None:
        """Alias for commit."""
        await self.commit()
    
    async def download(self, bundle_type: str = "zip") -> AsyncIterator[config_pb2.Bundle]:
        """Download configuration as a bundle."""
        if not self._config or not self._client:
            raise RuntimeError("Config not initialized")
        
        bundle_config = config_pb2.BundleConfig(
            id=self._config,
            type=config_pb2.BundleType.BUNDLE_ZIP if bundle_type == "zip" else config_pb2.BundleType.BUNDLE_TAR
        )
        
        async for bundle in self._client.download(bundle_config):
            yield bundle 