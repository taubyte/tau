#!/usr/bin/env python3
"""
Comprehensive tests for the Python Spore Drive Config class.

This module provides comprehensive tests for the Config class functionality,
including integration tests with mock server, unit tests with mocked dependencies,
and pytest-specific test configurations.
"""

import asyncio
import os
import sys
import tempfile
import subprocess
import time
import zipfile
import yaml
import pytest
from unittest.mock import AsyncMock, MagicMock, patch
from typing import Optional, Dict, Any, List
from pathlib import Path

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.config import Config
from spore_drive.operations import (
    CloudConfig, DomainConfig, P2PConfig, BootstrapConfig,
    HostsConfig, HostConfig, SSHConfig, LocationConfig,
    AuthConfig, SignerConfig, ShapesConfig, ShapeConfig, PortsConfig,
    Cloud, Hosts, Auth, Shapes
)
from spore_drive.proto.config.v1 import config_pb2


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


async def create_config(config: Config) -> None:
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

    with_key_auth = config.auth.signer("withkey")
    await with_key_auth.username.set("tau2")
    await with_key_auth.key.path.set("/keys/test.pem")

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
    await host1.addresses.add(["1.2.3.4/24", "4.3.2.1/24"])
    await host1.ssh.address.set("1.2.3.4:4242")
    await host1.ssh.auth.add(["main"])
    await host1.location.set("1.25, 25.1")
    await host1.shapes.get("shape1").generate()
    await host1.shapes.get("shape2").generate()

    host2 = config.hosts.get("host2")
    await host2.addresses.add(["8.2.3.4/24", "4.3.2.8/24"])
    await host2.ssh.address.set("8.2.3.4:4242")
    await host2.ssh.auth.add(["withkey"])
    await host2.location.set("1.25, 25.1")
    await host2.shapes.get("shape1").generate()
    await host2.shapes.get("shape2").generate()

    # Set P2P Bootstrap
    await config.cloud.p2p.bootstrap.shape("shape1").nodes.add(["host2", "host1"])
    await config.cloud.p2p.bootstrap.shape("shape2").nodes.add(["host2", "host1"])

    await config.commit()


async def create_config_with_set(config: Config) -> None:
    """Create a test configuration using the set method approach."""
    # Set Cloud configuration
    await config.cloud.set(CloudConfig(
        domain=DomainConfig(root="test.com", generated="gtest.com"),
        p2p=P2PConfig()
    ))
    await config.cloud.domain.validation.generate()
    await config.cloud.p2p.swarm.generate()

    # Set Auth configurations
    await config.auth.set(AuthConfig(
        signers={
            "main": SignerConfig(username="tau1", password="testtest"),
            "withkey": SignerConfig(username="tau2", key="/keys/test.pem")
        }
    ))

    # Set Shapes configurations
    await config.shapes.set(ShapesConfig(
        shapes={
            "shape1": ShapeConfig(
                services=["auth", "seer"],
                ports=PortsConfig(ports={"main": 4242, "lite": 4262})
            ),
            "shape2": ShapeConfig(
                services=["gateway", "patrick", "monkey"],
                ports=PortsConfig(ports={"main": 6242, "lite": 6262}),
                plugins=["plugin1@v0.1"]
            )
        }
    ))

    # Set Hosts
    await config.hosts.set(HostsConfig(
        hosts={
            "host1": HostConfig(
                addr=["1.2.3.4/24", "4.3.2.1/24"],
                ssh=SSHConfig(addr="1.2.3.4", port=4242, auth=["main"]),
                location=LocationConfig(lat=1.25, long=25.1)
            ),
            "host2": HostConfig(
                addr=["8.2.3.4/24", "4.3.2.8/24"],
                ssh=SSHConfig(addr="8.2.3.4", port=4242, auth=["withkey"]),
                location=LocationConfig(lat=1.25, long=25.1)
            )
        }
    ))

    # Generate host instances key/id
    await config.hosts.get("host1").shapes.get("shape1").generate()
    await config.hosts.get("host1").shapes.get("shape2").generate()
    await config.hosts.get("host2").shapes.get("shape1").generate()
    await config.hosts.get("host2").shapes.get("shape2").generate()

    # Set P2P Bootstrap
    await config.cloud.p2p.set(P2PConfig(
        bootstrap=BootstrapConfig(config={
            "shape1": ["host2", "host1"],
            "shape2": ["host2", "host1"]
        })
    ))

    await config.commit()


async def extract_config_data(bundle) -> Dict[str, Any]:
    """Extract configuration data from a bundle."""
    config_data = {}
    
    # Create a temporary file to store the bundle
    with tempfile.NamedTemporaryFile(suffix=".zip", delete=False) as temp_file:
        temp_path = temp_file.name
    
    try:
        # Write bundle data to temporary file
        with open(temp_path, 'wb') as f:
            async for chunk in bundle:
                if hasattr(chunk, 'data') and hasattr(chunk.data, 'chunk'):
                    f.write(chunk.data.chunk)
                elif hasattr(chunk, 'chunk'):
                    f.write(chunk.chunk)
        
        # Extract and parse YAML files
        with zipfile.ZipFile(temp_path, 'r') as zip_file:
            for file_info in zip_file.filelist:
                if file_info.filename.endswith('.yaml'):
                    with zip_file.open(file_info.filename) as yaml_file:
                        yaml_data = yaml.safe_load(yaml_file.read().decode('utf-8'))
                        config_data.update(yaml_data)
    
    finally:
        # Clean up temporary file
        if os.path.exists(temp_path):
            os.unlink(temp_path)
    
    return config_data


class TestConfigIntegration:
    """Integration tests for the Config class."""
    
    def setup_method(self):
        """Set up test environment."""
        self.rpc_url: Optional[str] = None
        self.mock_server_process: Optional[subprocess.Popen] = None
        self.temp_dir: Optional[str] = None
        self.config: Optional[Config] = None
    
    def teardown_method(self):
        """Clean up test environment."""
        # Clean up config synchronously using internal cleanup
        if self.config:
            # Force cleanup without async
            try:
                if hasattr(self.config, '_client') and self.config._client:
                    self.config._client._channel.close()
            except:
                pass
            self.config = None
        
        if self.temp_dir and os.path.exists(self.temp_dir):
            import shutil
            shutil.rmtree(self.temp_dir)
        
        if self.mock_server_process:
            self.mock_server_process.terminate()
            try:
                self.mock_server_process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.mock_server_process.kill()
                self.mock_server_process.wait()
    
    def start_mock_server(self) -> str:
        """Start the mock server and return the RPC URL."""
        # Get the path to the mock server
        mock_server_path = os.path.join(
            os.path.dirname(__file__), 
            "..", "..", "mock"
        )
        
        # Start the mock server
        self.mock_server_process = subprocess.Popen(
            ["go", "run", "."],
            cwd=mock_server_path,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        
        # Wait for the server to start and get the URL
        time.sleep(3)  # Give the server time to start
        
        # Read the URL from stdout
        if self.mock_server_process.stdout:
            line = self.mock_server_process.stdout.readline()
            if line:
                return line.strip()
        
        raise RuntimeError("Failed to start mock server")
    
    async def setup_config(self):
        """Set up configuration for testing."""
        self.rpc_url = self.start_mock_server()
        self.temp_dir = tempfile.mkdtemp(prefix="cloud-")
        self.config = Config(self.temp_dir)
        await self.config.init(self.rpc_url)
    
    @pytest.mark.asyncio
    async def test_set_and_get_cloud_domain_root(self):
        """Test setting and getting cloud domain root."""
        await self.setup_config()
        
        await self.config.cloud.domain.root.set("the.cloud")
        domain_root = await self.config.cloud.domain.root.get()
        assert domain_root == "the.cloud"
    
    @pytest.mark.asyncio
    async def test_generate_validation_keys(self):
        """Test generating validation keys."""
        await self.setup_config()
        
        await self.config.cloud.domain.validation.generate()
        
        path_base = self.config.cloud.domain.validation.keys.path
        assert await path_base.private_key.get() == "keys/dv_private.key"
        assert await path_base.public_key.get() == "keys/dv_public.key"
        
        data_base = self.config.cloud.domain.validation.keys.data
        private_key_data = await data_base.private_key.get()
        public_key_data = await data_base.public_key.get()
        assert len(private_key_data) > 128
        assert len(public_key_data) > 128
    
    @pytest.mark.asyncio
    async def test_create_valid_configuration(self):
        """Test creating a valid configuration."""
        await self.setup_config()
        
        await create_config(self.config)
        
        # Verify parts of the configuration
        root_domain = await self.config.cloud.domain.root.get()
        assert root_domain == "test.com"
        
        generated_domain = await self.config.cloud.domain.generated.get()
        assert generated_domain == "gtest.com"
        
        hosts_list = await self.config.hosts.list()
        assert "host1" in hosts_list
        assert "host2" in hosts_list
    
    @pytest.mark.asyncio
    async def test_list_hosts(self):
        """Test listing hosts."""
        await self.setup_config()
        
        host_a = self.config.hosts.get("hostA")
        await host_a.addresses.set(["1.1.1.1", "2.2.2.1"])
        await host_a.ssh.address.set("1.1.1.1:22")
        await host_a.ssh.auth.set(["user1"])
        
        hosts = await self.config.hosts.list()
        assert isinstance(hosts, list)
        assert len(hosts) == 1
        assert "hostA" in hosts
    
    @pytest.mark.asyncio
    async def test_commit_changes(self):
        """Test committing changes."""
        await self.setup_config()
        
        result = await self.config.commit()
        assert result is not None
    
    @pytest.mark.asyncio
    async def test_download_configuration_bundle(self):
        """Test downloading configuration bundle."""
        await self.setup_config()
        
        await create_config(self.config)
        
        bundle_iterator = self.config.download()
        
        # Create temporary file for the bundle
        temp_file = tempfile.NamedTemporaryFile(suffix=".zip", delete=False)
        temp_path = temp_file.name
        
        try:
            got_data = False
            async for chunk in bundle_iterator:
                if hasattr(chunk, 'data'):
                    if hasattr(chunk.data, 'chunk'):
                        temp_file.write(chunk.data.chunk)
                        got_data = True
                    elif hasattr(chunk.data, 'type'):
                        assert chunk.data.type == config_pb2.BundleType.BUNDLE_ZIP
                elif hasattr(chunk, 'chunk'):
                    temp_file.write(chunk.chunk)
                    got_data = True
                elif hasattr(chunk, 'type'):
                    assert chunk.type == config_pb2.BundleType.BUNDLE_ZIP
            
            temp_file.close()
            
            assert got_data
            
            # Verify the zip file contains expected content
            with zipfile.ZipFile(temp_path, 'r') as zip_file:
                yaml_files = [f for f in zip_file.namelist() if f.endswith('.yaml')]
                assert len(yaml_files) > 0
                
                # Check for cloud.yaml
                cloud_yaml = None
                for file_name in yaml_files:
                    if file_name.endswith('/cloud.yaml') or file_name == 'cloud.yaml':
                        cloud_yaml = file_name
                        break
                
                assert cloud_yaml is not None
                
                # Parse and verify YAML content
                with zip_file.open(cloud_yaml) as yaml_file:
                    yaml_content = yaml.safe_load(yaml_file.read().decode('utf-8'))
                    assert yaml_content['domain']['root'] == "test.com"
        
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)
    
    @pytest.mark.asyncio
    async def test_set_and_get_swarm_key(self):
        """Test setting and getting swarm key."""
        await self.setup_config()
        
        await self.config.cloud.p2p.swarm.generate()
        swarm_key_path = await self.config.cloud.p2p.swarm.key.path.get()
        assert swarm_key_path is not None
        
        swarm_key_data = await self.config.cloud.p2p.swarm.key.data.get()
        assert len(swarm_key_data) > 0
    
    @pytest.mark.asyncio
    async def test_add_list_and_delete_auth_signer(self):
        """Test adding, listing, and deleting auth signers."""
        await self.setup_config()
        
        signer = self.config.auth.signer("testSigner")
        await signer.username.set("testUser")
        await signer.password.set("testPass")
        
        signers_list_before_delete = await self.config.auth.list()
        assert "testSigner" in signers_list_before_delete
        
        await signer.delete()
        
        signers_list_after_delete = await self.config.auth.list()
        assert "testSigner" not in signers_list_after_delete
    
    @pytest.mark.asyncio
    async def test_generate_same_config_with_different_methods(self):
        """Test that create_config and create_config_with_set generate the same config."""
        await self.setup_config()
        
        # Create first config using create_config
        config1 = Config()
        await config1.init(self.rpc_url)
        await create_config(config1)
        bundle1 = config1.download()
        config1_data = await extract_config_data(bundle1)
        
        # Create second config using create_config_with_set
        config2 = Config()
        await config2.init(self.rpc_url)
        await create_config_with_set(config2)
        bundle2 = config2.download()
        config2_data = await extract_config_data(bundle2)
        
        # Clean up configs
        await config1.free()
        await config2.free()
        
        # Recursively remove id and key from any object within the configuration data
        def remove_shape_ids(obj: Any) -> None:
            if not isinstance(obj, dict):
                return
            
            # Remove id and key if present
            if "id" in obj:
                del obj["id"]
            if "key" in obj:
                del obj["key"]
            
            # Recursively apply to all nested objects
            for value in obj.values():
                remove_shape_ids(value)
        
        remove_shape_ids(config1_data)
        remove_shape_ids(config2_data)
        
        # Compare the configs
        assert config1_data == config2_data


# Test runner
def run_tests():
    """Run all integration tests."""
    
    # Create test class instance
    test_instance = TestConfigIntegration()
    
    # Run all test methods
    test_methods = [
        test_instance.test_set_and_get_cloud_domain_root,
        test_instance.test_generate_validation_keys,
        test_instance.test_create_valid_configuration,
        test_instance.test_list_hosts,
        test_instance.test_commit_changes,
        test_instance.test_download_configuration_bundle,
        test_instance.test_set_and_get_swarm_key,
        test_instance.test_add_list_and_delete_auth_signer,
        test_instance.test_generate_same_config_with_different_methods,
    ]
    
    for test_method in test_methods:
        try:
            asyncio.run(test_method())
        except Exception as e:
            raise


class TestConfigUnit:
    """Unit test suite for the Config class."""

    def setup_method(self):
        """Set up test environment."""
        self.temp_dir = None
        self.config = None

    def teardown_method(self):
        """Clean up test environment."""
        if self.temp_dir and os.path.exists(self.temp_dir):
            import shutil
            shutil.rmtree(self.temp_dir, ignore_errors=True)

    @pytest.mark.asyncio
    async def test_config_constructor(self):
        """Test Config constructor with different parameters."""
        # Test with no source (should call new())
        config = Config()
        assert config._source is None
        assert config._client is None
        assert config._config is None
        
        # Test with directory source
        temp_dir = tempfile.mkdtemp()
        try:
            config = Config(temp_dir)
            assert config._source == temp_dir
        finally:
            import shutil
            shutil.rmtree(temp_dir)

    @pytest.mark.asyncio
    async def test_config_init_with_url(self):
        """Test config initialization with explicit URL."""
        config = Config()
        
        mock_client = AsyncMock()
        mock_config = MagicMock()
        mock_config.id = "test-config-id"
        
        with patch('spore_drive.config.ConfigClient', return_value=mock_client):
            with patch.object(mock_client, 'new', return_value=mock_config):
                await config.init("http://localhost:8080/")
                
                assert config._client == mock_client
                assert config._config == mock_config
                mock_client.new.assert_called_once()

    @pytest.mark.asyncio
    async def test_config_init_without_url(self):
        """Test config initialization without URL (uses service manager)."""
        config = Config()
        
        mock_client = AsyncMock()
        mock_config = MagicMock()
        mock_config.id = "test-config-id"
        
        with patch('spore_drive.config.start_service', return_value=8080):
            with patch('spore_drive.config.ConfigClient', return_value=mock_client):
                with patch.object(mock_client, 'new', return_value=mock_config):
                    await config.init()
                    
                    assert config._client == mock_client
                    assert config._config == mock_config

    @pytest.mark.asyncio
    async def test_config_properties_not_initialized(self):
        """Test config properties when not initialized."""
        config = Config()
        
        with pytest.raises(RuntimeError, match="Config not initialized"):
            _ = config.cloud
            
        with pytest.raises(RuntimeError, match="Config not initialized"):
            _ = config.hosts
            
        with pytest.raises(RuntimeError, match="Config not initialized"):
            _ = config.auth
            
        with pytest.raises(RuntimeError, match="Config not initialized"):
            _ = config.shapes

    @pytest.mark.asyncio
    async def test_config_commit(self):
        """Test config commit operation."""
        config = Config()
        config._client = AsyncMock()
        config._config = MagicMock()
        config._config.id = "test-config-id"
        
        mock_empty = MagicMock()
        config._client.commit.return_value = mock_empty
        
        result = await config.commit()
        assert result == mock_empty
        config._client.commit.assert_called_once_with(config._config)

    @pytest.mark.asyncio
    async def test_config_free(self):
        """Test config resource cleanup."""
        config = Config()
        config._client = AsyncMock()
        config._config = MagicMock()
        
        await config.free()
        config._client.free.assert_called_once_with(config._config)


if __name__ == "__main__":
    pytest.main([__file__]) 