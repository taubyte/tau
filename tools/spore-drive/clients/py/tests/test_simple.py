#!/usr/bin/env python3
"""
Basic functionality tests to verify core Config operations.
"""

import asyncio
import os
import sys
import tempfile
import subprocess
import time
import pytest

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.config import Config
from spore_drive.service_manager import check_service_health


class TestBasicConfig:
    """Test suite for basic Config functionality."""
    
    def setup_method(self):
        """Set up test environment."""
        self.mock_server_process = None
        self.temp_dir = None
    
    def teardown_method(self):
        """Clean up test environment."""
        if self.mock_server_process:
            try:
                self.mock_server_process.terminate()
                self.mock_server_process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.mock_server_process.kill()
                self.mock_server_process.wait()
        
        if self.temp_dir and os.path.exists(self.temp_dir):
            import shutil
            shutil.rmtree(self.temp_dir, ignore_errors=True)
    
    def start_mock_server(self):
        """Start the mock server and return the URL."""
        mock_server_path = os.path.join(os.path.dirname(__file__), "..", "..", "mock")
        
        if not os.path.exists(mock_server_path):
            pytest.skip(f"Mock server directory not found: {mock_server_path}")
        
        self.mock_server_process = subprocess.Popen(
            ["go", "run", "."],
            cwd=mock_server_path,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        
        # Wait for server to start
        time.sleep(3)
        
        # Get URL
        if self.mock_server_process.stdout:
            line = self.mock_server_process.stdout.readline()
            if line:
                return line.strip()
        
        pytest.skip("Mock server didn't output URL")
    
    @pytest.mark.asyncio
    async def test_basic_config_new(self):
        """Test basic config functionality with new config."""
        url = self.start_mock_server()
        port = int(url.split(":")[-1].rstrip("/"))
        
        # Test health check
        assert check_service_health(port), "Health check failed"
        
        # Test basic config operations
        config = Config()  # Don't pass a source, so it calls new()
        await config.init(url)
        
        # Test basic set/get
        await config.cloud.domain.root.set("test.cloud")
        result = await config.cloud.domain.root.get()
        assert result == "test.cloud"
        
        # Clean up
        await config.free()
    
    @pytest.mark.asyncio
    async def test_basic_config_with_tempdir(self):
        """Test basic config functionality with temporary directory."""
        url = self.start_mock_server()
        port = int(url.split(":")[-1].rstrip("/"))
        
        # Test health check
        assert check_service_health(port), "Health check failed"
        
        # Test with temporary directory
        self.temp_dir = tempfile.mkdtemp(prefix="cloud-")
        config = Config(self.temp_dir)
        await config.init(url)
        
        # Test basic operations that should work
        await config.commit()
        
        # Test list operations (should return empty lists)
        hosts = await config.hosts.list()
        assert isinstance(hosts, list)
        
        auth = await config.auth.list()
        assert isinstance(auth, list)
        
        shapes = await config.shapes.list()
        assert isinstance(shapes, list)
        
        # Clean up
        await config.free()
    
    @pytest.mark.asyncio
    async def test_minimal_operations(self):
        """Test minimal config operations."""
        url = self.start_mock_server()
        port = int(url.split(":")[-1].rstrip("/"))
        
        # Test health check
        assert check_service_health(port), "Health check failed"
        
        # Test minimal operations
        self.temp_dir = tempfile.mkdtemp(prefix="cloud-")
        config = Config(self.temp_dir)
        await config.init(url)
        
        # Test commit (should not fail)
        await config.commit()
        
        # Test basic domain operations
        await config.cloud.domain.root.set("minimal.test")
        result = await config.cloud.domain.root.get()
        assert result == "minimal.test"
        
        # Clean up
        await config.free()


@pytest.mark.asyncio
async def test_basic_config():
    """Legacy test function for basic config functionality."""
    # Start mock server
    mock_server_path = os.path.join(os.path.dirname(__file__), "..", "..", "mock")
    
    if not os.path.exists(mock_server_path):
        pytest.skip(f"Mock server directory not found: {mock_server_path}")
    
    process = subprocess.Popen(
        ["go", "run", "."],
        cwd=mock_server_path,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True
    )
    
    try:
        # Wait for server to start
        time.sleep(3)
        
        # Get URL
        if process.stdout:
            line = process.stdout.readline()
            if line:
                url = line.strip()
                
                # Extract port
                port = int(url.split(":")[-1].rstrip("/"))
                
                # Test health check
                assert check_service_health(port), "Health check failed"
                
                # Test basic config operations
                config = Config()  # Don't pass a source, so it calls new()
                await config.init(url)
                
                # Test basic set/get
                await config.cloud.domain.root.set("test.cloud")
                result = await config.cloud.domain.root.get()
                assert result == "test.cloud"
                
                # Clean up
                await config.free()
            else:
                pytest.skip("Mock server didn't output URL")
        else:
            pytest.skip("Mock server stdout not available")
    
    finally:
        # Clean up
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


@pytest.mark.asyncio
async def test_minimal_config():
    """Legacy test function for minimal config functionality."""
    # Start mock server
    mock_server_path = os.path.join(os.path.dirname(__file__), "..", "..", "mock")
    
    if not os.path.exists(mock_server_path):
        pytest.skip(f"Mock server directory not found: {mock_server_path}")
    
    process = subprocess.Popen(
        ["go", "run", "."],
        cwd=mock_server_path,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True
    )
    
    temp_dir = None
    
    try:
        # Wait for server to start
        time.sleep(3)
        
        # Get URL
        if process.stdout:
            line = process.stdout.readline()
            if line:
                url = line.strip()
                
                # Extract port
                port = int(url.split(":")[-1].rstrip("/"))
                
                # Test health check
                assert check_service_health(port), "Health check failed"
                
                # Test basic config operations
                temp_dir = tempfile.mkdtemp(prefix="cloud-")
                config = Config(temp_dir)
                await config.init(url)
                
                # Test commit (should not fail)
                await config.commit()
                
                # Test list operations (should return empty lists)
                hosts = await config.hosts.list()
                assert isinstance(hosts, list)
                
                auth = await config.auth.list()
                assert isinstance(auth, list)
                
                shapes = await config.shapes.list()
                assert isinstance(shapes, list)
                
                # Clean up
                await config.free()
            else:
                pytest.skip("Mock server didn't output URL")
        else:
            pytest.skip("Mock server stdout not available")
    
    finally:
        # Clean up
        if temp_dir and os.path.exists(temp_dir):
            import shutil
            shutil.rmtree(temp_dir)
        
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


if __name__ == "__main__":
    pytest.main([__file__]) 