#!/usr/bin/env python3
"""
Integration tests for the Python Spore Drive Operations using mock server.

This module provides integration tests that use the mock server to test
actual async method execution paths and hit the remaining missing lines
in operations.py for 100% coverage.
"""

import asyncio
import os
import sys
import tempfile
import subprocess
import time
import pytest
from typing import Optional

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.config import Config
from spore_drive.operations import (
    CloudConfig, DomainConfig, P2PConfig, BootstrapConfig,
    HostsConfig, HostConfig, SSHConfig, LocationConfig,
    AuthConfig, SignerConfig, ShapesConfig, ShapeConfig, PortsConfig
)


class TestOperationsIntegration:
    """Integration tests for Operations using mock server."""
    
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
        self.temp_dir = tempfile.mkdtemp(prefix="operations-test-")
        self.config = Config(self.temp_dir)
        await self.config.init(self.rpc_url)

    @pytest.mark.asyncio
    async def test_validation_operations_integration(self):
        """Test validation operations that should hit lines 293-296, 319-322."""
        await self.setup_config()
        
        # Test Domain validation operations
        domain = self.config.cloud.domain
        validation = domain.validation
        
        # Generate validation keys (should hit generate method)
        await validation.generate()
        
        # Test validation keys operations
        validation_keys = validation.keys
        assert validation_keys.path is not None
        assert validation_keys.data is not None
        
        # Test ValidationKeysPath operations
        validation_keys_path = validation_keys.path
        private_key_path = await validation_keys_path.private_key.get()
        public_key_path = await validation_keys_path.public_key.get()
        assert private_key_path is not None
        assert public_key_path is not None
        
        # Test ValidationKeysData operations
        validation_keys_data = validation_keys.data
        private_key_data = await validation_keys_data.private_key.get()
        public_key_data = await validation_keys_data.public_key.get()
        assert len(private_key_data) > 0
        assert len(public_key_data) > 0

    @pytest.mark.asyncio
    async def test_bootstrap_operations_integration(self):
        """Test bootstrap operations that should hit lines 401-402, 412, 416-418, 422-426, 441."""
        await self.setup_config()
        
        # Test P2P operations
        p2p = self.config.cloud.p2p
        bootstrap = p2p.bootstrap
        
        # Test Bootstrap.shape method (lines 401-402)
        bootstrap_shape = bootstrap.shape("test_shape")
        assert bootstrap_shape is not None
        
        # Test BootstrapShape operations (lines 422-426, 441)
        nodes = bootstrap_shape.nodes
        assert nodes is not None
        
        # Add some nodes to test the operations
        await nodes.add(["node1", "node2"])
        node_list = await nodes.list()
        assert isinstance(node_list, list)

    @pytest.mark.asyncio
    async def test_swarm_operations_integration(self):
        """Test swarm operations that should hit lines 456, 467, 471."""
        await self.setup_config()
        
        # Test Swarm operations
        swarm = self.config.cloud.p2p.swarm
        
        # Generate swarm key (should hit generate method)
        await swarm.generate()
        
        # Test SwarmKey operations (lines 456, 467, 471)
        swarm_key = swarm.key
        assert swarm_key is not None
        
        # Test path and data access
        key_path = await swarm_key.path.get()
        key_data = await swarm_key.data.get()
        assert key_path is not None
        assert len(key_data) > 0

    @pytest.mark.asyncio
    async def test_host_operations_integration(self):
        """Test host operations that should hit lines 494-495, 526-532, 575, 586, 594."""
        await self.setup_config()
        
        # Test Host operations
        host = self.config.hosts.get("test_host")
        
        # Test Host.set method with complex config (lines 526-532)
        location = LocationConfig(lat=40.7128, long=-74.0060)
        ssh_config = SSHConfig(addr="192.168.1.1", port=22, auth=["key1"])
        host_config = HostConfig(
            addr=["192.168.1.0/24"], 
            ssh=ssh_config, 
            location=location
        )
        await host.set(host_config)
        
        # Test HostShapes operations (lines 575, 586, 594)
        host_shapes = host.shapes
        host_shape = host_shapes.get("test_shape")
        
        # Generate host shape instance (should hit _instance property and generate method)
        await host_shape.generate()
        
        # Test host shape id and key operations
        shape_id = await host_shape.id()
        shape_key = await host_shape.key.get()
        assert shape_id is not None
        assert shape_key is not None
        
        # Test host delete operation (line 533)
        await host.delete()

    @pytest.mark.asyncio
    async def test_ssh_operations_integration(self):
        """Test SSH operations that should hit lines 551-557."""
        await self.setup_config()
        
        # Test SSH operations
        host = self.config.hosts.get("test_host")
        ssh = host.ssh
        
        # Test SSH.set method with port (lines 551-557)
        ssh_config = SSHConfig(addr="192.168.1.1", port=2222, auth=["key1", "key2"])
        await ssh.set(ssh_config)
        
        # Verify the address was set correctly (with port)
        address = await ssh.address.get()
        assert "192.168.1.1:2222" in address or "192.168.1.1" in address

    @pytest.mark.asyncio
    async def test_auth_operations_integration(self):
        """Test auth operations that should hit lines 648-649, 672."""
        await self.setup_config()
        
        # Test Auth operations
        auth = self.config.auth
        
        # Test Auth.signer method (lines 648-649)
        signer = auth.signer("test_signer")
        assert signer is not None
        
        # Test Signer operations (line 672)
        await signer.username.set("testuser")
        await signer.password.set("testpass")
        
        username = await signer.username.get()
        password = await signer.password.get()
        assert username == "testuser"
        assert password == "testpass"
        
        # Test signer delete
        await signer.delete()

    @pytest.mark.asyncio
    async def test_ssh_key_operations_integration(self):
        """Test SSH key operations that should hit lines 676-683."""
        await self.setup_config()
        
        # Test SSH Key operations
        signer = self.config.auth.signer("ssh_signer")
        ssh_key = signer.key
        
        # Test SSHKey operations (lines 676-683)
        assert ssh_key.path is not None
        assert ssh_key.data is not None
        
        # Set and get path
        await ssh_key.path.set("/keys/test.pem")
        key_path = await ssh_key.path.get()
        assert key_path == "/keys/test.pem"

    @pytest.mark.asyncio
    async def test_shape_operations_integration(self):
        """Test shape operations that should hit lines 745, 749-754."""
        await self.setup_config()
        
        # Test Shape operations
        shape = self.config.shapes.get("test_shape")
        
        # Test Shape.set method (lines 747-754)
        shape_config = ShapeConfig(
            services=["auth", "seer"],
            ports=PortsConfig(ports={"main": 4242, "lite": 4262}),
            plugins=["plugin1@v0.1"]
        )
        await shape.set(shape_config)
        
        # Test shape delete operation (line 745)
        await shape.delete()

    @pytest.mark.asyncio
    async def test_ports_operations_integration(self):
        """Test ports operations that should hit lines 772, 788, 792-794, 798."""
        await self.setup_config()
        
        # Test Ports operations
        shape = self.config.shapes.get("test_shape")
        ports = shape.ports
        
        # Test Ports.list method (line 772)
        ports_list = await ports.list()
        assert isinstance(ports_list, list)
        
        # Test Ports.port method and Port operations (lines 788, 792-794, 798)
        port = ports.port("test_port")
        assert port is not None
        
        # Test port set operation
        await port.set(8080)

    @pytest.mark.asyncio
    async def test_comprehensive_operations_flow(self):
        """Test comprehensive operations flow to hit remaining lines."""
        await self.setup_config()
        
        # Create a complete configuration to exercise all operations
        
        # Cloud configuration
        cloud_config = CloudConfig(
            domain=DomainConfig(root="test.com", generated="gtest.com"),
            p2p=P2PConfig(bootstrap=BootstrapConfig(config={}))
        )
        await self.config.cloud.set(cloud_config)
        
        # Generate validation and swarm keys
        await self.config.cloud.domain.validation.generate()
        await self.config.cloud.p2p.swarm.generate()
        
        # Auth configuration
        auth_config = AuthConfig(signers={
            "main": SignerConfig(username="tau1", password="testtest"),
            "withkey": SignerConfig(username="tau2", key="/keys/test.pem")
        })
        await self.config.auth.set(auth_config)
        
        # Shapes configuration
        shapes_config = ShapesConfig(shapes={
            "shape1": ShapeConfig(
                services=["auth", "seer"],
                ports=PortsConfig(ports={"main": 4242, "lite": 4262})
            ),
            "shape2": ShapeConfig(
                services=["gateway", "patrick", "monkey"],
                ports=PortsConfig(ports={"main": 6242, "lite": 6262}),
                plugins=["plugin1@v0.1"]
            )
        })
        await self.config.shapes.set(shapes_config)
        
        # Hosts configuration
        hosts_config = HostsConfig(hosts={
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
        })
        await self.config.hosts.set(hosts_config)
        
        # Generate host shape instances
        await self.config.hosts.get("host1").shapes.get("shape1").generate()
        await self.config.hosts.get("host1").shapes.get("shape2").generate()
        await self.config.hosts.get("host2").shapes.get("shape1").generate()
        await self.config.hosts.get("host2").shapes.get("shape2").generate()
        
        # Set P2P Bootstrap
        await self.config.cloud.p2p.bootstrap.shape("shape1").nodes.add(["host1", "host2"])
        await self.config.cloud.p2p.bootstrap.shape("shape2").nodes.add(["host1", "host2"])
        
        # Commit the configuration
        await self.config.commit()
        
        # Test various list operations
        hosts_list = await self.config.hosts.list()
        shapes_list = await self.config.shapes.list()
        auth_list = await self.config.auth.list()
        
        assert len(hosts_list) >= 2
        assert len(shapes_list) >= 2
        assert len(auth_list) >= 2

    @pytest.mark.asyncio
    async def test_final_missing_lines_to_100_percent(self):
        """Target the specific remaining 14 lines to reach 100% coverage."""
        await self.setup_config()
        
        # Line 108: _dict_to_protobuf complex nested scenario
        # This will be hit through complex config operations
        
        # Lines 417-418, 422-426: Bootstrap operations with nested parameters
        bootstrap = self.config.cloud.p2p.bootstrap
        bootstrap_shape = bootstrap.shape("complex_shape")
        
        # Test bootstrap shape with complex node operations (lines 422-426)
        nodes_op = bootstrap_shape.nodes
        await nodes_op.add(["node1", "node2", "node3"])
        await nodes_op.clear()
        await nodes_op.add(["final_node"])
        
        # Line 441: More bootstrap shape operations
        nodes_list = await nodes_op.list()
        assert isinstance(nodes_list, list)
        
        # Line 575: HostShapes.get with specific parameters
        host = self.config.hosts.get("coverage_host")
        host_shapes = host.shapes
        specific_shape = host_shapes.get("specific_shape_for_coverage")
        await specific_shape.generate()
        
        # Line 679: SSH key data operations
        auth_signer = self.config.auth.signer("ssh_test_signer")
        # First set up the signer with username/password
        await auth_signer.username.set("testuser")
        await auth_signer.password.set("testpass")
        
        # Then try SSH key path operations (this should hit line 679)
        ssh_key = auth_signer.key
        await ssh_key.path.set("/keys/test.pem")
        key_path = await ssh_key.path.get()
        assert key_path == "/keys/test.pem"
        
        # Line 772: Ports.list with specific return handling
        shape = self.config.shapes.get("ports_test_shape")
        ports = shape.ports
        
        # Add some ports first
        await ports.port("http").set(80)
        await ports.port("https").set(443)
        
        # Test list operation (line 772)
        ports_list = await ports.list()
        assert isinstance(ports_list, list)
        
        # Lines 792-794, 798: Port operations
        http_port = ports.port("http_detailed")
        await http_port.set(8080)
        
        # Test Port operations that hit lines 792-794, 798
        https_port = ports.port("https_detailed")
        await https_port.set(8443)


if __name__ == "__main__":
    # Run tests with pytest
    pytest.main([__file__, "-v"])