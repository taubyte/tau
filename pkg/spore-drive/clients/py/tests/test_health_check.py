#!/usr/bin/env python3
"""
Comprehensive health check tests for spore drive services.
"""

import sys
import os
import pytest
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.service_manager import ServiceManager, get_existing_service_port, check_service_health
from spore_drive.proto.health.v1 import health_pb2


class TestHealthCheck:
    """Test suite for health check functionality."""
    
    def test_basic_health_check_function(self):
        """Test basic health check functionality."""
        # Check if there's an existing service
        port = get_existing_service_port()
        
        if port:
            # Check health
            is_healthy = check_service_health(port)
            # Should return a boolean
            assert isinstance(is_healthy, bool)
        else:
            # No existing service, that's also valid
            assert port is None
    
    def test_health_check_with_service_manager(self):
        """Test health check using service manager."""
        with ServiceManager() as manager:
            port = manager.get_port()
            assert port is not None
            assert port > 0
            
            # Test health check
            is_healthy = manager.check_health()
            assert isinstance(is_healthy, bool)
    
    def test_health_check_invalid_port(self):
        """Test health check with invalid port."""
        # Test with a port that's unlikely to be in use
        is_healthy = check_service_health(99999)
        assert is_healthy is False
    
    def test_existing_service_detection(self):
        """Test existing service detection."""
        port = get_existing_service_port()
        # Should return either None or a valid port number
        assert port is None or (isinstance(port, int) and port > 0)


class TestConnectHealthCheck:
    """Test suite for Connect-RPC health check implementation."""
    
    def test_protobuf_serialization(self):
        """Test protobuf serialization for health check."""
        # Test protobuf serialization
        empty_message = health_pb2.Empty()
        serialized = empty_message.SerializeToString()
        
        # Should produce some serialized data
        assert isinstance(serialized, bytes)
    
    def test_connect_health_check_with_service_manager(self):
        """Test Connect-RPC health check implementation with service manager."""
        sm = ServiceManager()
        
        # Check if there's an existing service
        is_running, port = sm.has_existing_service()
        if is_running and port:
            # Test health check
            is_healthy = sm.check_health(port)
            assert isinstance(is_healthy, bool)
        else:
            # No existing service found, which is also valid
            assert not is_running
    
    def test_service_manager_has_existing_service(self):
        """Test the has_existing_service method."""
        sm = ServiceManager()
        is_running, port = sm.has_existing_service()
        
        # Should return a boolean and either None or int
        assert isinstance(is_running, bool)
        assert port is None or isinstance(port, int)


class TestHealthCheckIntegration:
    """Integration tests for health check functionality."""
    
    def test_full_health_check_workflow(self):
        """Test complete health check workflow."""
        # Start a service and test health check
        with ServiceManager() as manager:
            port = manager.get_port()
            assert port > 0
            
            # Service should be running
            assert manager.is_running()
            
            # Health check should work
            is_healthy = manager.check_health()
            assert isinstance(is_healthy, bool)
            
            # Check with standalone function too
            is_healthy_standalone = check_service_health(port)
            assert isinstance(is_healthy_standalone, bool)
        
        # After context exit, service should be stopped
        assert not manager.is_running()


def test_health_check():
    """Legacy test function for compatibility."""
    # Check if there's an existing service
    port = get_existing_service_port()
    if port:
        # Check health
        is_healthy = check_service_health(port)
        assert isinstance(is_healthy, bool)


def test_connect_health_check():
    """Legacy Connect-RPC health check test for compatibility."""
    # Test protobuf serialization
    empty_message = health_pb2.Empty()
    serialized = empty_message.SerializeToString()
    assert isinstance(serialized, bytes)
    
    # Test service manager health check
    sm = ServiceManager()
    
    # Check if there's an existing service
    is_running, port = sm.has_existing_service()
    if is_running and port:
        # Test health check
        is_healthy = sm.check_health(port)
        assert isinstance(is_healthy, bool)


if __name__ == "__main__":
    pytest.main([__file__]) 