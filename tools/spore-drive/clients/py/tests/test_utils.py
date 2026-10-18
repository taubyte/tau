#!/usr/bin/env python3
"""
Tests for the Spore Drive utils module.

This module provides comprehensive tests for the utility functions
in the spore_drive.utils module.
"""

import os
import sys
import pytest
from unittest.mock import patch, MagicMock

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive import utils


class TestTauBinarySourceUtils:
    """Test suite for tau binary source utility functions."""

    def test_tau_latest(self):
        """Test tau_latest function returns TauBinarySource with latest type."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        result = utils.tau_latest()
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.LATEST
        assert result.value is None

    def test_tau_version(self):
        """Test tau_version function returns TauBinarySource with version type."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        version = "1.2.3"
        result = utils.tau_version(version)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.VERSION
        assert result.value == version

    def test_tau_version_empty_string(self):
        """Test tau_version function with empty string."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        version = ""
        result = utils.tau_version(version)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.VERSION
        assert result.value == version

    def test_tau_url(self):
        """Test tau_url function returns TauBinarySource with URL type."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        url = "https://example.com/tau.tar.gz"
        result = utils.tau_url(url)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.URL
        assert result.value == url

    def test_tau_url_local_file(self):
        """Test tau_url function with local file path."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        url = "file:///path/to/tau.tar.gz"
        result = utils.tau_url(url)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.URL
        assert result.value == url

    def test_tau_path(self):
        """Test tau_path function returns TauBinarySource with path type."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        path = "/usr/local/bin/tau"
        result = utils.tau_path(path)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.PATH
        assert result.value == path

    def test_tau_path_relative(self):
        """Test tau_path function with relative path."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        path = "./tau"
        result = utils.tau_path(path)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.PATH
        assert result.value == path

    def test_tau_path_with_spaces(self):
        """Test tau_path function with path containing spaces."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        path = "/path with spaces/tau"
        result = utils.tau_path(path)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.PATH
        assert result.value == path


class TestServiceManagementUtils:
    """Test suite for service management utility functions."""

    @patch('spore_drive.utils.start_service')
    def test_start_spore_drive_service(self, mock_start_service):
        """Test start_spore_drive_service function."""
        expected_port = 8080
        mock_start_service.return_value = expected_port
        
        result = utils.start_spore_drive_service()
        
        assert result == expected_port
        mock_start_service.assert_called_once()

    @patch('spore_drive.utils.start_service')
    def test_start_spore_drive_service_returns_port(self, mock_start_service):
        """Test start_spore_drive_service returns correct port type."""
        mock_start_service.return_value = 4242
        
        result = utils.start_spore_drive_service()
        
        assert isinstance(result, int)
        assert result == 4242

    @patch('spore_drive.utils.stop_service')
    def test_stop_spore_drive_service(self, mock_stop_service):
        """Test stop_spore_drive_service function."""
        utils.stop_spore_drive_service()
        
        mock_stop_service.assert_called_once()

    @patch('spore_drive.utils.get_existing_service_port')
    def test_get_spore_drive_service_port_with_port(self, mock_get_port):
        """Test get_spore_drive_service_port function when service is running."""
        expected_port = 8080
        mock_get_port.return_value = expected_port
        
        result = utils.get_spore_drive_service_port()
        
        assert result == expected_port
        mock_get_port.assert_called_once()

    @patch('spore_drive.utils.get_existing_service_port')
    def test_get_spore_drive_service_port_no_service(self, mock_get_port):
        """Test get_spore_drive_service_port function when no service is running."""
        mock_get_port.return_value = None
        
        result = utils.get_spore_drive_service_port()
        
        assert result is None
        mock_get_port.assert_called_once()

    @patch('spore_drive.utils.get_existing_service_port')
    def test_get_spore_drive_service_port_zero_port(self, mock_get_port):
        """Test get_spore_drive_service_port function when port is 0."""
        mock_get_port.return_value = 0
        
        result = utils.get_spore_drive_service_port()
        
        assert result == 0
        mock_get_port.assert_called_once()


class TestUtilsIntegration:
    """Integration tests for utils module."""

    @patch('spore_drive.utils.start_service')
    @patch('spore_drive.utils.stop_service')
    @patch('spore_drive.utils.get_existing_service_port')
    def test_service_lifecycle(self, mock_get_port, mock_stop_service, mock_start_service):
        """Test complete service lifecycle using utility functions."""
        # Start service
        mock_start_service.return_value = 8080
        port = utils.start_spore_drive_service()
        assert port == 8080
        
        # Check if service is running
        mock_get_port.return_value = 8080
        existing_port = utils.get_spore_drive_service_port()
        assert existing_port == 8080
        
        # Stop service
        utils.stop_spore_drive_service()
        
        # Verify all functions were called
        mock_start_service.assert_called_once()
        mock_get_port.assert_called_once()
        mock_stop_service.assert_called_once()

    def test_tau_source_functions_return_correct_types(self):
        """Test that all tau source functions return correct types."""
        from spore_drive.types import TauBinarySource
        
        # Test tau_latest returns TauBinarySource
        assert isinstance(utils.tau_latest(), TauBinarySource)
        
        # Test tau_version returns TauBinarySource
        assert isinstance(utils.tau_version("1.0.0"), TauBinarySource)
        
        # Test tau_url returns TauBinarySource
        assert isinstance(utils.tau_url("https://example.com"), TauBinarySource)
        
        # Test tau_path returns TauBinarySource
        assert isinstance(utils.tau_path("/path/to/tau"), TauBinarySource)


class TestUtilsEdgeCases:
    """Test edge cases for utils module."""

    def test_tau_version_with_special_characters(self):
        """Test tau_version with special characters."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        version = "1.0.0-beta+exp.sha.5114f85"
        result = utils.tau_version(version)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.VERSION
        assert result.value == version

    def test_tau_url_with_query_parameters(self):
        """Test tau_url with query parameters."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        url = "https://example.com/tau.tar.gz?version=1.0.0&arch=amd64"
        result = utils.tau_url(url)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.URL
        assert result.value == url

    def test_tau_path_with_unicode(self):
        """Test tau_path with unicode characters."""
        from spore_drive.types import TauBinarySource, TauSourceType
        
        path = "/path/with/unicode/测试/tau"
        result = utils.tau_path(path)
        assert isinstance(result, TauBinarySource)
        assert result.source_type == TauSourceType.PATH
        assert result.value == path

    @patch('spore_drive.utils.start_service')
    def test_start_spore_drive_service_exception_handling(self, mock_start_service):
        """Test start_spore_drive_service handles exceptions properly."""
        mock_start_service.side_effect = Exception("Service start failed")
        
        with pytest.raises(Exception, match="Service start failed"):
            utils.start_spore_drive_service()

    @patch('spore_drive.utils.stop_service')
    def test_stop_spore_drive_service_exception_handling(self, mock_stop_service):
        """Test stop_spore_drive_service handles exceptions properly."""
        mock_stop_service.side_effect = Exception("Service stop failed")
        
        with pytest.raises(Exception, match="Service stop failed"):
            utils.stop_spore_drive_service()

    @patch('spore_drive.utils.get_existing_service_port')
    def test_get_spore_drive_service_port_exception_handling(self, mock_get_port):
        """Test get_spore_drive_service_port handles exceptions properly."""
        mock_get_port.side_effect = Exception("Port check failed")
        
        with pytest.raises(Exception, match="Port check failed"):
            utils.get_spore_drive_service_port()


# Standalone test functions for better coverage
def test_tau_latest_standalone():
    """Standalone test for tau_latest function."""
    from spore_drive.types import TauBinarySource, TauSourceType
    
    result = utils.tau_latest()
    assert isinstance(result, TauBinarySource)
    assert result.source_type == TauSourceType.LATEST
    assert result.value is None


def test_tau_version_standalone():
    """Standalone test for tau_version function."""
    from spore_drive.types import TauBinarySource, TauSourceType
    
    result = utils.tau_version("2.0.0")
    assert isinstance(result, TauBinarySource)
    assert result.source_type == TauSourceType.VERSION
    assert result.value == "2.0.0"


def test_tau_url_standalone():
    """Standalone test for tau_url function."""
    from spore_drive.types import TauBinarySource, TauSourceType
    
    result = utils.tau_url("https://tau.example.com")
    assert isinstance(result, TauBinarySource)
    assert result.source_type == TauSourceType.URL
    assert result.value == "https://tau.example.com"


def test_tau_path_standalone():
    """Standalone test for tau_path function."""
    from spore_drive.types import TauBinarySource, TauSourceType
    
    result = utils.tau_path("/usr/bin/tau")
    assert isinstance(result, TauBinarySource)
    assert result.source_type == TauSourceType.PATH
    assert result.value == "/usr/bin/tau"


@patch('spore_drive.utils.start_service')
def test_start_spore_drive_service_standalone(mock_start_service):
    """Standalone test for start_spore_drive_service function."""
    mock_start_service.return_value = 9090
    assert utils.start_spore_drive_service() == 9090


@patch('spore_drive.utils.stop_service')
def test_stop_spore_drive_service_standalone(mock_stop_service):
    """Standalone test for stop_spore_drive_service function."""
    utils.stop_spore_drive_service()
    mock_stop_service.assert_called_once()


@patch('spore_drive.utils.get_existing_service_port')
def test_get_spore_drive_service_port_standalone(mock_get_port):
    """Standalone test for get_spore_drive_service_port function."""
    mock_get_port.return_value = 7070
    assert utils.get_spore_drive_service_port() == 7070 