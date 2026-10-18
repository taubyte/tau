#!/usr/bin/env python3
"""
Comprehensive tests for the Python Spore Drive Drive class.

This module provides comprehensive tests for the Drive and Course classes,
including integration tests with mock server and unit tests with mocked dependencies.
"""

import asyncio
import os
import sys
import tempfile
import subprocess
import time
import pytest
from unittest.mock import AsyncMock, MagicMock, patch
from typing import Optional, Dict, Any, List, Set
from pathlib import Path

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.drive import Drive, Course
from spore_drive.config import Config
from spore_drive.types import TauBinarySource, CourseConfig
from spore_drive.proto.drive.v1 import drive_pb2


class MockAsyncIterator:
    """Mock async iterator for testing."""
    def __init__(self, items):
        self.items = items
        self.index = 0
    
    def __aiter__(self):
        return self
    
    async def __anext__(self):
        if self.index < len(self.items):
            item = self.items[self.index]
            self.index += 1
            return item
        raise StopAsyncIteration


async def create_test_config(config: Config) -> None:
    """Create a test configuration with all major components."""
    # Set Cloud Domain
    await config.cloud.domain.root.set("test.com")
    await config.cloud.domain.generated.set("gtest.com")
    await config.cloud.domain.validation.generate()

    # Generate P2P Swarm keys
    await config.cloud.p2p.swarm.generate()

    # Set Auth configurations
    main_auth = config.auth.signer("main")
    await main_auth.username.set("tau1")
    await main_auth.password.set("testtest")

    # Set Shapes configurations
    shape1 = config.shapes.get("shape1")
    await shape1.services.set(["auth", "seer"])
    await shape1.ports.port("main").set(4242)
    await shape1.ports.port("lite").set(4262)

    shape2 = config.shapes.get("shape2")
    await shape2.services.set(["gateway", "patrick", "monkey"])
    await shape2.ports.port("main").set(6242)
    await shape2.ports.port("lite").set(6262)
    await shape2.plugins.set(["plugin1@v0.1"])

    # Set Hosts
    host1 = config.hosts.get("host1")
    await host1.addresses.add(["127.0.0.1/32"])
    await host1.ssh.address.set("127.0.0.1:4242")
    await host1.ssh.auth.add(["main"])
    await host1.location.set("1.25, 25.1")
    await host1.shapes.get("shape1").generate()
    await host1.shapes.get("shape2").generate()

    host2 = config.hosts.get("host2")
    await host2.addresses.add(["127.0.0.1/32"])
    await host2.ssh.address.set("127.0.0.1:6242")
    await host2.ssh.auth.add(["main"])
    await host2.location.set("1.25, 25.1")
    await host2.shapes.get("shape1").generate()
    await host2.shapes.get("shape2").generate()

    # Set P2P Bootstrap
    await config.cloud.p2p.bootstrap.shape("shape1").nodes.add(["host2", "host1"])
    await config.cloud.p2p.bootstrap.shape("shape2").nodes.add(["host2", "host1"])

    await config.commit()


class TestDrive:
    """Test suite for the Drive class."""

    def setup_method(self):
        """Set up test environment."""
        self.temp_dir = None
        self.config = None
        self.drive = None
        self.mock_server_process = None
        self.rpc_url = None

    def teardown_method(self):
        """Clean up test environment."""
        # Clean up drive and config synchronously using internal cleanup
        if self.drive:
            try:
                if hasattr(self.drive, '_client') and self.drive._client:
                    self.drive._client._channel.close()
            except:
                pass
            self.drive = None
        
        if self.config:
            try:
                if hasattr(self.config, '_client') and self.config._client:
                    self.config._client._channel.close()
            except:
                pass
            self.config = None

        if self.mock_server_process:
            try:
                self.mock_server_process.terminate()
                self.mock_server_process.wait(timeout=5)
            except Exception:
                if self.mock_server_process.poll() is None:
                    self.mock_server_process.kill()
                    self.mock_server_process.wait()

        if self.temp_dir and os.path.exists(self.temp_dir):
            import shutil
            shutil.rmtree(self.temp_dir, ignore_errors=True)

    def start_mock_server(self) -> str:
        """Start the mock server and return the RPC URL."""
        # Get the path to the mock server
        mock_server_path = os.path.join(
            os.path.dirname(__file__), 
            "..", "..", "mock"
        )
        
        if not os.path.exists(mock_server_path):
            pytest.skip(f"Mock server directory not found: {mock_server_path}")
        
        # Start the mock server
        try:
            self.mock_server_process = subprocess.Popen(
                ["go", "run", "."],
                cwd=mock_server_path,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True
            )
            
            # Wait for the server to start
            time.sleep(3)
            
            # Read the URL from stdout
            if self.mock_server_process.stdout:
                line = self.mock_server_process.stdout.readline()
                if line:
                    url = line.strip()
                    return url
                else:
                    pytest.skip("Mock server didn't output URL")
            else:
                pytest.skip("Mock server stdout not available")
                
        except Exception as e:
            pytest.skip(f"Failed to start mock server: {e}")

    async def setup_drive(self, tau_source: Optional[TauBinarySource] = None):
        """Set up drive with configuration."""
        self.temp_dir = tempfile.mkdtemp()
        self.rpc_url = self.start_mock_server()
        
        self.config = Config(self.temp_dir)
        await self.config.init(self.rpc_url)
        await create_test_config(self.config)
        
        # Create a fake tau binary for testing
        fake_tau_path = os.path.join(self.temp_dir, "faketau")
        with open(fake_tau_path, "w") as f:
            f.write("#!/bin/bash\necho 'fake tau binary'")
        os.chmod(fake_tau_path, 0o755)
        
        self.drive = Drive(self.config, tau_source or fake_tau_path)
        await self.drive.init(self.rpc_url)

    @pytest.mark.asyncio
    async def test_plot_course(self):
        """Test plotting a course."""
        await self.setup_drive()
        
        course_config: CourseConfig = {
            "shapes": ["shape1"],
            "concurrency": 1
        }
        
        course = await self.drive.plot(course_config)
        assert isinstance(course, Course)
        assert course._course is not None
        assert course._client is not None
        assert course._drive is not None

    @pytest.mark.asyncio
    async def test_course_displace(self):
        """Test course displacement."""
        await self.setup_drive()
        
        course_config: CourseConfig = {
            "shapes": ["shape1"],
            "concurrency": 1
        }
        
        course = await self.drive.plot(course_config)
        await course.displace()
        # Should not raise any exceptions

    @pytest.mark.asyncio
    async def test_course_progress(self):
        """Test course progress monitoring."""
        await self.setup_drive()
        
        course_config: CourseConfig = {
            "shapes": ["shape1"],
            "concurrency": 1
        }
        
        course = await self.drive.plot(course_config)
        await course.displace()
        
        progress_count = 0
        async for progress in course.progress():
            assert isinstance(progress, drive_pb2.DisplacementProgress)
            progress_count += 1
            if progress_count >= 5:  # Limit to avoid infinite loop
                break
        
        assert progress_count > 0


class TestDriveUnit:
    """Unit test suite for the Drive class."""

    def setup_method(self):
        """Set up test environment."""
        self.temp_dir = None
        self.config = None
        self.drive = None

    def teardown_method(self):
        """Clean up test environment."""
        if self.temp_dir and os.path.exists(self.temp_dir):
            import shutil
            shutil.rmtree(self.temp_dir, ignore_errors=True)

    @pytest.mark.asyncio
    async def test_drive_constructor(self):
        """Test Drive constructor with different parameters."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        # Test with config only
        config = MagicMock(spec=Config)
        drive = Drive(config)
        assert drive._config == config
        assert drive._tau is None
        assert drive._client is None
        assert drive._drive is None

        # Test with config and tau source (path)
        drive = Drive(config, "/path/to/tau")
        assert drive._config == config
        assert isinstance(drive._tau, TauBinarySource)
        assert drive._tau.source_type == TauSourceType.PATH
        assert drive._tau.value == "/path/to/tau"

        # Test with config and latest tau
        drive = Drive(config, True)
        assert drive._config == config
        assert isinstance(drive._tau, TauBinarySource)
        assert drive._tau.source_type == TauSourceType.LATEST
        assert drive._tau.value is None

        # Test with config and version
        drive = Drive(config, "1.0.0")
        assert drive._config == config
        assert isinstance(drive._tau, TauBinarySource)
        assert drive._tau.source_type == TauSourceType.VERSION
        assert drive._tau.value == "1.0.0"

        # Test with config and URL
        drive = Drive(config, "https://example.com/tau.tar.gz")
        assert drive._config == config
        assert isinstance(drive._tau, TauBinarySource)
        assert drive._tau.source_type == TauSourceType.URL
        assert drive._tau.value == "https://example.com/tau.tar.gz"

    @pytest.mark.asyncio
    async def test_drive_init_with_url(self):
        """Test drive initialization with explicit URL."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        drive = Drive(config)
        
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.DriveClient', return_value=mock_client):
            with patch('spore_drive.drive.drive_pb2.DriveRequest', return_value=mock_request):
                with patch.object(mock_client, 'new', return_value=mock_drive):
                    await drive.init("http://localhost:8080/")
                    
                    assert drive._client == mock_client
                    assert drive._drive == mock_drive
                    mock_client.new.assert_called_once_with(mock_request)

    @pytest.mark.asyncio
    async def test_drive_init_without_url(self):
        """Test drive initialization without URL (uses service manager)."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        drive = Drive(config)
        
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest', return_value=mock_request):
                    with patch.object(mock_client, 'new', return_value=mock_drive):
                        await drive.init()
                        
                        assert drive._client == mock_client
                        assert drive._drive == mock_drive
                        mock_client.new.assert_called_once_with(mock_request)

    @pytest.mark.asyncio
    async def test_drive_init_with_tau_latest(self):
        """Test drive initialization with latest tau."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        drive = Drive(config, True)
        
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest', return_value=mock_request):
                    with patch.object(mock_client, 'new', return_value=mock_drive):
                        await drive.init()
                        
                        assert mock_request.latest is True
                        mock_client.new.assert_called_once_with(mock_request)

    @pytest.mark.asyncio
    async def test_drive_init_with_tau_version(self):
        """Test drive initialization with tau version."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        drive = Drive(config, "1.0.0")
        
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest', return_value=mock_request):
                    with patch.object(mock_client, 'new', return_value=mock_drive):
                        await drive.init()
                        
                        assert mock_request.version == "1.0.0"
                        mock_client.new.assert_called_once_with(mock_request)

    @pytest.mark.asyncio
    async def test_drive_init_with_tau_url(self):
        """Test drive initialization with tau URL."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        drive = Drive(config, "https://example.com/tau.tar.gz")
        
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest', return_value=mock_request):
                    with patch.object(mock_client, 'new', return_value=mock_drive):
                        await drive.init()
                        
                        assert mock_request.url == "https://example.com/tau.tar.gz"
                        mock_client.new.assert_called_once_with(mock_request)

    @pytest.mark.asyncio
    async def test_drive_init_with_tau_path(self):
        """Test drive initialization with tau path."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        drive = Drive(config, "/path/to/tau.tar.gz")
        
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest', return_value=mock_request):
                    with patch.object(mock_client, 'new', return_value=mock_drive):
                        await drive.init()
                        
                        assert mock_request.path == "/path/to/tau.tar.gz"
                        mock_client.new.assert_called_once_with(mock_request)

    @pytest.mark.asyncio
    async def test_drive_free(self):
        """Test drive resource cleanup."""
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = MagicMock()
        
        await drive.free()
        drive._client.free.assert_called_once_with(drive._drive)

    @pytest.mark.asyncio
    async def test_drive_free_no_client(self):
        """Test drive free when client is None."""
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = None
        drive._drive = MagicMock()
        
        # Should not raise any exception
        await drive.free()

    @pytest.mark.asyncio
    async def test_drive_free_no_drive(self):
        """Test drive free when drive is None."""
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = None
        
        # Should not raise any exception
        await drive.free()
        drive._client.free.assert_not_called()

    @pytest.mark.asyncio
    async def test_plot_course_success(self):
        """Test successful course plotting."""
        from spore_drive.types import CourseConfig
        
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = MagicMock()

        mock_course = MagicMock()
        mock_request = MagicMock()

        with patch('spore_drive.drive.drive_pb2.PlotRequest', return_value=mock_request):
            with patch.object(drive._client, 'plot', return_value=mock_course):
                course_config = CourseConfig(
                    shapes=["shape1"],
                    concurrency=1
                )

                course = await drive.plot(course_config)

                assert isinstance(course, Course)
                assert course._client == drive._client
                assert course._drive == drive._drive
                assert course._course == mock_course
                assert isinstance(course._config, CourseConfig)
                assert course._config.shapes == ["shape1"]
                assert course._config.concurrency == 1

    @pytest.mark.asyncio
    async def test_plot_course_not_initialized(self):
        """Test plotting course when drive is not initialized."""
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = None
        drive._drive = None
        
        course_config: CourseConfig = {
            "shapes": ["shape1"],
            "concurrency": 1
        }
        
        with pytest.raises(RuntimeError, match="Drive not initialized"):
            await drive.plot(course_config)

    @pytest.mark.asyncio
    async def test_plot_course_with_shapes_and_concurrency(self):
        """Test plotting course with shapes and concurrency."""
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = MagicMock()
        
        mock_course = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.drive_pb2.PlotRequest', return_value=mock_request):
            with patch.object(drive._client, 'plot', return_value=mock_course):
                course_config: CourseConfig = {
                    "shapes": ["shape1", "shape2"],
                    "concurrency": 5
                }
                
                course = await drive.plot(course_config)
                
                # Verify request was configured correctly
                # Note: In unit tests, we're mocking the request object, so we verify the method calls
                # rather than the final state of the object
                mock_request.shapes.extend.assert_called_once_with(["shape1", "shape2"])
                assert mock_request.concurrency == 5

    @pytest.mark.asyncio
    async def test_plot_course_with_defaults(self):
        """Test plotting course with default values."""
        from spore_drive.types import CourseConfig
        
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = MagicMock()

        mock_course = MagicMock()
        mock_request = MagicMock()

        with patch('spore_drive.drive.drive_pb2.PlotRequest', return_value=mock_request):
            with patch.object(drive._client, 'plot', return_value=mock_course):
                course_config = CourseConfig()  # Use defaults

                course = await drive.plot(course_config)

                # Verify default values
                mock_request.shapes.extend.assert_called_once_with([])
                assert mock_request.concurrency == 1  # Default concurrency is 1

    @pytest.mark.asyncio
    async def test_plot_course_with_shapes_only(self):
        """Test plotting course with shapes only."""
        from spore_drive.types import CourseConfig
        
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = MagicMock()

        mock_course = MagicMock()
        mock_request = MagicMock()

        with patch('spore_drive.drive.drive_pb2.PlotRequest', return_value=mock_request):
            with patch.object(drive._client, 'plot', return_value=mock_course):
                course_config = CourseConfig(
                    shapes=["shape1"]
                )

                course = await drive.plot(course_config)

                mock_request.shapes.extend.assert_called_once_with(["shape1"])
                assert mock_request.concurrency == 1  # Default concurrency is 1

    @pytest.mark.asyncio
    async def test_plot_course_with_concurrency_only(self):
        """Test plotting course with concurrency only."""
        config = MagicMock(spec=Config)
        drive = Drive(config)
        drive._client = AsyncMock()
        drive._drive = MagicMock()
        
        mock_course = MagicMock()
        mock_request = MagicMock()
        
        with patch('spore_drive.drive.drive_pb2.PlotRequest', return_value=mock_request):
            with patch.object(drive._client, 'plot', return_value=mock_course):
                course_config: CourseConfig = {
                    "concurrency": 3
                }
                
                course = await drive.plot(course_config)
                
                mock_request.shapes.extend.assert_called_once_with([])
                assert mock_request.concurrency == 3


class TestCourseUnit:
    """Unit test suite for the Course class."""

    @pytest.mark.asyncio
    async def test_course_constructor(self):
        """Test Course constructor."""
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        course_config: CourseConfig = {"shapes": ["shape1"]}
        
        course = Course(mock_client, mock_drive, mock_course, course_config)
        
        assert course._client == mock_client
        assert course._drive == mock_drive
        assert course._course == mock_course
        assert course._config == course_config

    @pytest.mark.asyncio
    async def test_course_displace(self):
        """Test course displacement."""
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        course_config: CourseConfig = {"shapes": ["shape1"]}
        
        course = Course(mock_client, mock_drive, mock_course, course_config)
        
        await course.displace()
        mock_client.displace.assert_called_once_with(mock_course)

    @pytest.mark.asyncio
    async def test_course_progress(self):
        """Test course progress monitoring."""
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        course_config: CourseConfig = {"shapes": ["shape1"]}
        
        # Mock progress events
        mock_progress1 = MagicMock()
        mock_progress2 = MagicMock()
        # Make progress method return an async iterator directly, not a coroutine
        mock_client.progress = MagicMock(return_value=MockAsyncIterator([
            mock_progress1, mock_progress2
        ]))
        
        course = Course(mock_client, mock_drive, mock_course, course_config)
        
        progress_count = 0
        async for progress in course.progress():
            assert progress in [mock_progress1, mock_progress2]
            progress_count += 1
        
        assert progress_count == 2
        mock_client.progress.assert_called_once_with(mock_course)

    @pytest.mark.asyncio
    async def test_course_abort(self):
        """Test course abortion."""
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        course_config: CourseConfig = {"shapes": ["shape1"]}
        
        course = Course(mock_client, mock_drive, mock_course, course_config)
        
        await course.abort()
        mock_client.abort.assert_called_once_with(mock_course)

    @pytest.mark.asyncio
    async def test_course_progress_empty(self):
        """Test course progress with no events."""
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        course_config: CourseConfig = {"shapes": ["shape1"]}
        
        # Mock empty progress
        mock_client.progress = MagicMock(return_value=MockAsyncIterator([]))
        
        course = Course(mock_client, mock_drive, mock_course, course_config)
        
        progress_count = 0
        async for progress in course.progress():
            progress_count += 1
        
        assert progress_count == 0
        mock_client.progress.assert_called_once_with(mock_course)


class TestDriveIntegration:
    """Integration tests for Drive class with mocked dependencies."""

    @pytest.mark.asyncio
    async def test_full_workflow_mocked(self):
        """Test full drive workflow with mocked dependencies."""
        # Setup mocks
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        mock_progress = MagicMock()
        
        # Mock the drive initialization
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest'):
                    with patch.object(mock_client, 'new', return_value=mock_drive):
                        drive = Drive(config)
                        await drive.init()
                        
                        # Mock course plotting
                        with patch('spore_drive.drive.drive_pb2.PlotRequest'):
                            with patch.object(mock_client, 'plot', return_value=mock_course):
                                course_config: CourseConfig = {
                                    "shapes": ["shape1"],
                                    "concurrency": 1
                                }
                                
                                course = await drive.plot(course_config)
                                
                                # Mock displacement
                                await course.displace()
                                mock_client.displace.assert_called_once_with(mock_course)
                                
                                # Mock progress
                                mock_client.progress = MagicMock(return_value=MockAsyncIterator([mock_progress]))
                                
                                progress_count = 0
                                async for progress in course.progress():
                                    assert progress == mock_progress
                                    progress_count += 1
                                
                                assert progress_count == 1
                                
                                # Mock abort
                                await course.abort()
                                mock_client.abort.assert_called_once_with(mock_course)
                                
                                # Cleanup
                                await drive.free()
                                mock_client.free.assert_called_once_with(mock_drive)

    @pytest.mark.asyncio
    async def test_error_handling_mocked(self):
        """Test error handling with mocked dependencies."""
        config = MagicMock(spec=Config)
        config._config = MagicMock()  # Mock the _config attribute
        mock_client = AsyncMock()
        
        # Mock client error
        mock_client.new.side_effect = Exception("Connection failed")
        
        with patch('spore_drive.drive.start_service', return_value=8080):
            with patch('spore_drive.drive.DriveClient', return_value=mock_client):
                with patch('spore_drive.drive.drive_pb2.DriveRequest'):
                    drive = Drive(config)
                    
                    with pytest.raises(Exception, match="Connection failed"):
                        await drive.init()

    @pytest.mark.asyncio
    async def test_course_error_handling_mocked(self):
        """Test course error handling with mocked dependencies."""
        mock_client = AsyncMock()
        mock_drive = MagicMock()
        mock_course = MagicMock()
        course_config: CourseConfig = {"shapes": ["shape1"]}
        
        # Mock client errors
        mock_client.displace.side_effect = Exception("Displacement failed")
        mock_client.abort.side_effect = Exception("Abort failed")
        
        course = Course(mock_client, mock_drive, mock_course, course_config)
        
        with pytest.raises(Exception, match="Displacement failed"):
            await course.displace()
        
        with pytest.raises(Exception, match="Abort failed"):
            await course.abort()


if __name__ == "__main__":
    pytest.main([__file__])