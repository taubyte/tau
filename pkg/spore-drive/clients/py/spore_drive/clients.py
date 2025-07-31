"""
RPC client implementations for Spore Drive.

This module contains the low-level RPC client implementations
for communicating with the Spore Drive services.
"""

from typing import AsyncIterator
import grpc
import grpc.aio

from .proto.drive.v1 import drive_pb2
from .proto.config.v1 import config_pb2


class ConfigClient:
    """Client for configuration service RPC calls."""
    
    def __init__(self, base_url: str):
        # Strip protocol prefix for gRPC
        if base_url.startswith('http://'):
            base_url = base_url[7:]  # Remove 'http://'
        elif base_url.startswith('https://'):
            base_url = base_url[8:]  # Remove 'https://'
        
        self.base_url = base_url.rstrip('/')
        # Create gRPC channel and stub
        self.channel = grpc.aio.insecure_channel(self.base_url)
        # Note: We'll need to create the gRPC service stub manually since it's not generated
        # For now, we'll implement the RPC calls directly using the channel
    
    async def _call_unary(self, method: str, request) -> bytes:
        """Make a unary gRPC call."""
        # Create the full method name
        full_method = f"/config.v1.ConfigService/{method}"
        
        # Serialize the request
        request_bytes = request.SerializeToString()
        
        # Make the call
        call = self.channel.unary_unary(full_method)
        response_bytes = await call(request_bytes)
        
        return response_bytes
    
    async def _call_stream_unary(self, method: str, requests: AsyncIterator) -> bytes:
        """Make a client streaming gRPC call."""
        full_method = f"/config.v1.ConfigService/{method}"
        
        async def request_iterator():
            async for request in requests:
                yield request.SerializeToString()
        
        call = self.channel.stream_unary(full_method)
        response_bytes = await call(request_iterator())
        
        return response_bytes
    
    async def _call_unary_stream(self, method: str, request) -> AsyncIterator[bytes]:
        """Make a server streaming gRPC call."""
        full_method = f"/config.v1.ConfigService/{method}"
        
        request_bytes = request.SerializeToString()
        
        call = self.channel.unary_stream(full_method)
        async for response_bytes in call(request_bytes):
            yield response_bytes
    
    async def new(self) -> config_pb2.Config:
        """Create a new configuration."""
        request = config_pb2.Empty()
        response_bytes = await self._call_unary("New", request)
        return config_pb2.Config.FromString(response_bytes)
    
    async def load(self, source: config_pb2.Source) -> config_pb2.Config:
        """Load configuration from source."""
        response_bytes = await self._call_unary("Load", source)
        return config_pb2.Config.FromString(response_bytes)
    
    async def upload(self, source_uploads: AsyncIterator[config_pb2.SourceUpload]) -> config_pb2.Config:
        """Upload configuration from stream."""
        response_bytes = await self._call_stream_unary("Upload", source_uploads)
        return config_pb2.Config.FromString(response_bytes)
    
    async def download(self, bundle_config: config_pb2.BundleConfig) -> AsyncIterator[config_pb2.Bundle]:
        """Download configuration as bundle."""
        async for response_bytes in self._call_unary_stream("Download", bundle_config):
            yield config_pb2.Bundle.FromString(response_bytes)
    
    async def commit(self, config: config_pb2.Config) -> config_pb2.Empty:
        """Commit configuration changes."""
        response_bytes = await self._call_unary("Commit", config)
        return config_pb2.Empty.FromString(response_bytes)
    
    async def free(self, config: config_pb2.Config) -> config_pb2.Empty:
        """Free configuration resources."""
        response_bytes = await self._call_unary("Free", config)
        return config_pb2.Empty.FromString(response_bytes)
    
    async def do(self, op: config_pb2.Op) -> config_pb2.Return:
        """Execute configuration operation."""
        response_bytes = await self._call_unary("Do", op)
        return config_pb2.Return.FromString(response_bytes)
    
    async def close(self):
        """Close the gRPC channel."""
        await self.channel.close()


class DriveClient:
    """Client for drive service RPC calls."""
    
    def __init__(self, base_url: str):
        # Strip protocol prefix for gRPC
        if base_url.startswith('http://'):
            base_url = base_url[7:]  # Remove 'http://'
        elif base_url.startswith('https://'):
            base_url = base_url[8:]  # Remove 'https://'
        
        self.base_url = base_url.rstrip('/')
        # Create gRPC channel and stub
        self.channel = grpc.aio.insecure_channel(self.base_url)
    
    async def _call_unary(self, method: str, request) -> bytes:
        """Make a unary gRPC call."""
        # Create the full method name
        full_method = f"/drive.v1.DriveService/{method}"
        
        # Serialize the request
        request_bytes = request.SerializeToString()
        
        # Make the call
        call = self.channel.unary_unary(full_method)
        response_bytes = await call(request_bytes)
        
        return response_bytes
    
    async def _call_unary_stream(self, method: str, request) -> AsyncIterator[bytes]:
        """Make a server streaming gRPC call."""
        full_method = f"/drive.v1.DriveService/{method}"
        
        request_bytes = request.SerializeToString()
        
        call = self.channel.unary_stream(full_method)
        async for response_bytes in call(request_bytes):
            yield response_bytes
    
    async def new(self, request: drive_pb2.DriveRequest) -> drive_pb2.Drive:
        """Create a new drive."""
        response_bytes = await self._call_unary("New", request)
        return drive_pb2.Drive.FromString(response_bytes)
    
    async def plot(self, request: drive_pb2.PlotRequest) -> drive_pb2.Course:
        """Plot a course."""
        response_bytes = await self._call_unary("Plot", request)
        return drive_pb2.Course.FromString(response_bytes)
    
    async def displace(self, course: drive_pb2.Course) -> drive_pb2.Empty:
        """Start displacement."""
        response_bytes = await self._call_unary("Displace", course)
        return drive_pb2.Empty.FromString(response_bytes)
    
    async def progress(self, course: drive_pb2.Course) -> AsyncIterator[drive_pb2.DisplacementProgress]:
        """Get progress updates."""
        async for response_bytes in self._call_unary_stream("Progress", course):
            yield drive_pb2.DisplacementProgress.FromString(response_bytes)
    
    async def abort(self, course: drive_pb2.Course) -> drive_pb2.Empty:
        """Abort course."""
        response_bytes = await self._call_unary("Abort", course)
        return drive_pb2.Empty.FromString(response_bytes)
    
    async def free(self, drive: drive_pb2.Drive) -> drive_pb2.Empty:
        """Free drive resources."""
        response_bytes = await self._call_unary("Free", drive)
        return drive_pb2.Empty.FromString(response_bytes)
    
    async def close(self):
        """Close the gRPC channel."""
        await self.channel.close() 