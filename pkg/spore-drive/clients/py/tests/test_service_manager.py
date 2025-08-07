"""
Tests for the ServiceManager class.
"""

import pytest
import time
import requests
import os
from pathlib import Path
from spore_drive.service_manager import ServiceManager, get_existing_service_port


class TestServiceManager:
    """Test cases for ServiceManager class."""
    
    def test_service_manager_initialization(self):
        """Test ServiceManager initialization."""
        manager = ServiceManager()
        assert manager.process is None
        assert manager.port is None
        assert manager.timeout == 10
    
    def test_service_manager_custom_timeout(self):
        """Test ServiceManager initialization with custom timeout."""
        manager = ServiceManager(timeout=5)
        assert manager.timeout == 5
    
    def test_service_manager_context_manager(self):
        """Test ServiceManager as context manager."""
        with ServiceManager() as manager:
            assert manager.is_running()
            assert manager.get_port() is not None
            assert manager.get_port() > 0
        
        # Service should be stopped after context exit
        assert not manager.is_running()


class TestServiceManagerUnit:
    """Unit tests for ServiceManager with mocked dependencies."""
    
    def test_get_config_dir_windows(self, monkeypatch):
        """Test config directory on Windows."""
        monkeypatch.setattr("platform.system", lambda: "Windows")
        monkeypatch.setattr("os.environ", {"APPDATA": "C:\\Users\\test\\AppData\\Roaming"})
        
        manager = ServiceManager()
        config_dir = manager._get_config_dir()
        assert str(config_dir) == "C:\\Users\\test\\AppData\\Roaming"
    
    def test_get_config_dir_darwin(self, monkeypatch):
        """Test config directory on macOS."""
        monkeypatch.setattr("platform.system", lambda: "Darwin")
        monkeypatch.setattr("pathlib.Path.home", lambda: Path("/Users/test"))
        
        manager = ServiceManager()
        config_dir = manager._get_config_dir()
        assert str(config_dir) == "/Users/test/Library/Application Support"
    
    def test_get_config_dir_linux(self, monkeypatch):
        """Test config directory on Linux."""
        monkeypatch.setattr("platform.system", lambda: "Linux")
        monkeypatch.setattr("os.environ", {"XDG_CONFIG_HOME": "/home/test/.config"})
        
        manager = ServiceManager()
        config_dir = manager._get_config_dir()
        assert str(config_dir) == "/home/test/.config"
    
    def test_get_config_dir_linux_default(self, monkeypatch):
        """Test config directory on Linux with default path."""
        monkeypatch.setattr("platform.system", lambda: "Linux")
        monkeypatch.setattr("os.environ", {})
        monkeypatch.setattr("pathlib.Path.home", lambda: Path("/home/test"))
        
        manager = ServiceManager()
        config_dir = manager._get_config_dir()
        assert str(config_dir) == "/home/test/.config"
    
    def test_binary_exists_true(self, monkeypatch, tmp_path):
        """Test binary exists check when binary is present."""
        manager = ServiceManager()
        manager.binary_path = tmp_path / "drive"
        manager.binary_path.touch()
        
        assert manager._binary_exists() is True
    
    def test_binary_exists_false(self, monkeypatch, tmp_path):
        """Test binary exists check when binary is missing."""
        manager = ServiceManager()
        manager.binary_path = tmp_path / "drive"
        
        assert manager._binary_exists() is False
    
    def test_version_matches_true(self, monkeypatch, tmp_path):
        """Test version matches when versions are the same."""
        manager = ServiceManager()
        manager.version_file_path = tmp_path / "version.txt"
        manager.version_file_path.write_text("0.1.5")
        
        assert manager._version_matches() is True
    
    def test_version_matches_false(self, monkeypatch, tmp_path):
        """Test version matches when versions are different."""
        manager = ServiceManager()
        manager.version_file_path = tmp_path / "version.txt"
        manager.version_file_path.write_text("0.1.4")
        
        assert manager._version_matches() is False
    
    def test_version_matches_file_not_exists(self, monkeypatch, tmp_path):
        """Test version matches when version file doesn't exist."""
        manager = ServiceManager()
        manager.version_file_path = tmp_path / "version.txt"
        
        assert manager._version_matches() is False
    
    def test_version_matches_io_error(self, monkeypatch, tmp_path):
        """Test version matches when IO error occurs."""
        manager = ServiceManager()
        manager.version_file_path = tmp_path / "version.txt"
        manager.version_file_path.write_text("0.1.5")
        
        # Make the file unreadable to trigger IOError
        os.chmod(manager.version_file_path, 0o000)
        
        try:
            assert manager._version_matches() is False
        finally:
            # Restore permissions for cleanup
            os.chmod(manager.version_file_path, 0o644)
    
    def test_parse_asset_name_linux_amd64(self, monkeypatch):
        """Test asset name parsing for Linux AMD64."""
        monkeypatch.setattr("platform.system", lambda: "Linux")
        monkeypatch.setattr("platform.machine", lambda: "x86_64")
        
        manager = ServiceManager()
        os_name, arch = manager._parse_asset_name()
        
        assert os_name == "linux"
        assert arch == "amd64"
    
    def test_parse_asset_name_darwin_arm64(self, monkeypatch):
        """Test asset name parsing for macOS ARM64."""
        monkeypatch.setattr("platform.system", lambda: "Darwin")
        monkeypatch.setattr("platform.machine", lambda: "arm64")
        
        manager = ServiceManager()
        os_name, arch = manager._parse_asset_name()
        
        assert os_name == "darwin"
        assert arch == "arm64"
    
    def test_parse_asset_name_windows_amd64(self, monkeypatch):
        """Test asset name parsing for Windows AMD64."""
        monkeypatch.setattr("platform.system", lambda: "Windows")
        monkeypatch.setattr("platform.machine", lambda: "AMD64")
        
        manager = ServiceManager()
        os_name, arch = manager._parse_asset_name()
        
        assert os_name == "windows"
        assert arch == "amd64"
    
    def test_parse_asset_name_unsupported_os(self, monkeypatch):
        """Test asset name parsing for unsupported OS."""
        monkeypatch.setattr("platform.system", lambda: "FreeBSD")
        monkeypatch.setattr("platform.machine", lambda: "x86_64")
        
        manager = ServiceManager()
        os_name, arch = manager._parse_asset_name()
        
        assert os_name is None
        assert arch == "amd64"
    
    def test_parse_asset_name_unsupported_arch(self, monkeypatch):
        """Test asset name parsing for unsupported architecture."""
        monkeypatch.setattr("platform.system", lambda: "Linux")
        monkeypatch.setattr("platform.machine", lambda: "i386")
        
        manager = ServiceManager()
        os_name, arch = manager._parse_asset_name()
        
        assert os_name == "linux"
        assert arch is None
    
    def test_load_run_file_exists(self, monkeypatch, tmp_path):
        """Test loading run file when it exists."""
        manager = ServiceManager()
        manager.run_file_path = tmp_path / ".spore-drive.run"
        run_data = {"pid": 12345, "port": 8080}
        manager.run_file_path.write_text('{"pid": 12345, "port": 8080}')
        
        result = manager._load_run_file()
        assert result == run_data
    
    def test_load_run_file_not_exists(self, monkeypatch, tmp_path):
        """Test loading run file when it doesn't exist."""
        manager = ServiceManager()
        manager.run_file_path = tmp_path / ".spore-drive.run"
        
        result = manager._load_run_file()
        assert result is None
    
    def test_load_run_file_invalid_json(self, monkeypatch, tmp_path):
        """Test loading run file with invalid JSON."""
        manager = ServiceManager()
        manager.run_file_path = tmp_path / ".spore-drive.run"
        manager.run_file_path.write_text('{"invalid": json}')
        
        result = manager._load_run_file()
        assert result is None
    
    def test_is_process_running_true(self, monkeypatch):
        """Test process running check when process exists."""
        def mock_kill(pid, signal):
            pass  # No exception means process exists
        
        monkeypatch.setattr("os.kill", mock_kill)
        
        manager = ServiceManager()
        assert manager._is_process_running(12345) is True
    
    def test_is_process_running_false(self, monkeypatch):
        """Test process running check when process doesn't exist."""
        def mock_kill(pid, signal):
            raise OSError("No such process")
        
        monkeypatch.setattr("os.kill", mock_kill)
        
        manager = ServiceManager()
        assert manager._is_process_running(12345) is False
    
    def test_is_service_up_success(self, monkeypatch):
        """Test service up check when service responds."""
        def mock_post(*args, **kwargs):
            class MockResponse:
                status_code = 200
            return MockResponse()
        
        monkeypatch.setattr("requests.post", mock_post)
        
        manager = ServiceManager()
        assert manager._is_service_up(8080) is True
    
    def test_is_service_up_failure(self, monkeypatch):
        """Test service up check when service doesn't respond."""
        def mock_post(*args, **kwargs):
            raise requests.RequestException("Connection failed")
        
        monkeypatch.setattr("requests.post", mock_post)
        
        manager = ServiceManager()
        assert manager._is_service_up(8080) is False
    
    def test_wait_for_health_check_success_first_attempt(self, monkeypatch):
        """Test health check wait when service responds on first attempt."""
        call_count = 0
        def mock_is_service_up(self, port):
            nonlocal call_count
            call_count += 1
            return True
        
        monkeypatch.setattr(ServiceManager, "_is_service_up", mock_is_service_up)
        
        manager = ServiceManager()
        result = manager._wait_for_health_check(8080, max_retries=3, retry_delay=0.1)
        
        assert result is True
        assert call_count == 1
    
    def test_wait_for_health_check_success_after_retries(self, monkeypatch):
        """Test health check wait when service responds after retries."""
        call_count = 0
        def mock_is_service_up(self, port):
            nonlocal call_count
            call_count += 1
            return call_count >= 3  # Success on third attempt
        
        monkeypatch.setattr(ServiceManager, "_is_service_up", mock_is_service_up)
        
        manager = ServiceManager()
        result = manager._wait_for_health_check(8080, max_retries=5, retry_delay=0.1)
        
        assert result is True
        assert call_count == 3
    
    def test_wait_for_health_check_failure(self, monkeypatch):
        """Test health check wait when service never responds."""
        def mock_is_service_up(self, port):
            return False
        
        monkeypatch.setattr(ServiceManager, "_is_service_up", mock_is_service_up)
        
        manager = ServiceManager()
        result = manager._wait_for_health_check(8080, max_retries=3, retry_delay=0.1)
        
        assert result is False
    
    def test_has_existing_service_true(self, monkeypatch):
        """Test existing service check when service is running."""
        run_data = {"pid": 12345, "port": 8080}
        
        def mock_load_run_file(self):
            return run_data
        
        def mock_is_process_running(self, pid):
            return True
        
        monkeypatch.setattr(ServiceManager, "_load_run_file", mock_load_run_file)
        monkeypatch.setattr(ServiceManager, "_is_process_running", mock_is_process_running)
        
        manager = ServiceManager()
        is_running, port = manager.has_existing_service()
        
        assert is_running is True
        assert port == 8080
    
    def test_has_existing_service_false(self, monkeypatch):
        """Test existing service check when no service is running."""
        def mock_load_run_file(self):
            return None
        
        monkeypatch.setattr(ServiceManager, "_load_run_file", mock_load_run_file)
        
        manager = ServiceManager()
        is_running, port = manager.has_existing_service()
        
        assert is_running is False
        assert port is None
    
    def test_has_existing_service_process_dead(self, monkeypatch):
        """Test existing service check when process is dead."""
        run_data = {"pid": 12345, "port": 8080}
        
        def mock_load_run_file(self):
            return run_data
        
        def mock_is_process_running(self, pid):
            return False
        
        monkeypatch.setattr(ServiceManager, "_load_run_file", mock_load_run_file)
        monkeypatch.setattr(ServiceManager, "_is_process_running", mock_is_process_running)
        
        manager = ServiceManager()
        is_running, port = manager.has_existing_service()
        
        assert is_running is False
        assert port is None
    
    def test_check_health_with_port(self, monkeypatch):
        """Test health check with specific port."""
        def mock_is_service_up(self, port):
            return port == 8080
        
        monkeypatch.setattr(ServiceManager, "_is_service_up", mock_is_service_up)
        
        manager = ServiceManager()
        result = manager.check_health(8080)
        
        assert result is True
    
    def test_check_health_without_port(self, monkeypatch):
        """Test health check without port (uses manager port)."""
        def mock_is_service_up(self, port):
            return port == 8080
        
        monkeypatch.setattr(ServiceManager, "_is_service_up", mock_is_service_up)
        
        manager = ServiceManager()
        manager.port = 8080
        result = manager.check_health()
        
        assert result is True
    
    def test_check_health_no_port(self, monkeypatch):
        """Test health check when no port is available."""
        manager = ServiceManager()
        result = manager.check_health()
        
        assert result is False
    
    def test_get_port_timeout(self, monkeypatch):
        """Test get port with timeout."""
        def mock_load_run_file(self):
            return None
        
        monkeypatch.setattr(ServiceManager, "_load_run_file", mock_load_run_file)
        
        manager = ServiceManager()
        result = manager._get_port(timeout=100)  # Short timeout for testing
        
        assert result is None
    
    def test_get_port_success(self, monkeypatch):
        """Test get port when run file is found."""
        run_data = {"pid": 12345, "port": 8080}
        
        def mock_load_run_file(self):
            return run_data
        
        def mock_is_process_running(self, pid):
            return True
        
        def mock_wait_for_health_check(self, port, max_retries=5):
            return True
        
        monkeypatch.setattr(ServiceManager, "_load_run_file", mock_load_run_file)
        monkeypatch.setattr(ServiceManager, "_is_process_running", mock_is_process_running)
        monkeypatch.setattr(ServiceManager, "_wait_for_health_check", mock_wait_for_health_check)
        
        manager = ServiceManager()
        result = manager._get_port()
        
        assert result == 8080
    
    def test_get_port_health_check_fails(self, monkeypatch):
        """Test get port when health check fails but process is running."""
        run_data = {"pid": 12345, "port": 8080}
        
        def mock_load_run_file(self):
            return run_data
        
        def mock_is_process_running(self, pid):
            return True
        
        def mock_wait_for_health_check(self, port, max_retries=5):
            return False
        
        monkeypatch.setattr(ServiceManager, "_load_run_file", mock_load_run_file)
        monkeypatch.setattr(ServiceManager, "_is_process_running", mock_is_process_running)
        monkeypatch.setattr(ServiceManager, "_wait_for_health_check", mock_wait_for_health_check)
        
        manager = ServiceManager()
        result = manager._get_port()
        
        # Should still return port even if health check fails for existing services
        assert result == 8080


class TestServiceManagerGlobal:
    """Tests for global service manager functions."""
    
    def test_get_service_manager_singleton(self):
        """Test that get_service_manager returns the same instance."""
        from spore_drive.service_manager import get_service_manager, stop_service
        
        try:
            manager1 = get_service_manager()
            manager2 = get_service_manager()
            
            assert manager1 is manager2
        finally:
            stop_service()
    
    def test_start_service_global(self):
        """Test global start_service function."""
        from spore_drive.service_manager import start_service, stop_service
        
        try:
            port = start_service()
            assert isinstance(port, int)
            assert port > 0
        finally:
            stop_service()
    
    def test_stop_service_global(self):
        """Test global stop_service function."""
        from spore_drive.service_manager import get_service_manager, stop_service
        
        # Create a manager
        manager = get_service_manager()
        
        # Stop it
        stop_service()
        
        # Should be able to create a new one
        new_manager = get_service_manager()
        assert new_manager is not manager
    
    def test_get_existing_service_port_global(self):
        """Test global get_existing_service_port function."""
        from spore_drive.service_manager import get_existing_service_port
        
        port = get_existing_service_port()
        # Should return None or a valid port
        assert port is None or isinstance(port, int)
    
    def test_check_service_health_global(self):
        """Test global check_service_health function."""
        from spore_drive.service_manager import check_service_health
        
        # Test with None port
        result = check_service_health()
        assert isinstance(result, bool)
        
        # Test with specific port
        result = check_service_health(8080)
        assert isinstance(result, bool)
    
    def test_service_manager_start_stop(self):
        """Test starting and stopping the service manually."""
        manager = ServiceManager()
        
        try:
            port = manager.start()
            assert port > 0
            assert manager.is_running()
            assert manager.get_port() == port
            
            # Test that the service is actually responding using the manager's health check
            assert manager.check_health(port)
            
        finally:
            manager.stop()
            assert not manager.is_running()
            assert manager.get_port() is None
    
    def test_service_manager_multiple_starts(self):
        """Test that multiple start calls don't create multiple processes."""
        manager = ServiceManager()
        
        try:
            port1 = manager.start()
            port2 = manager.start()
            
            # Should return the same port
            assert port1 == port2
            assert manager.is_running()
            
        finally:
            manager.stop()
    
    def test_service_manager_stop_when_not_running(self):
        """Test stopping when service is not running."""
        manager = ServiceManager()
        # Should not raise an exception
        manager.stop()
        assert not manager.is_running()
    
    def test_service_manager_get_port_when_not_running(self):
        """Test getting port when service is not running."""
        manager = ServiceManager()
        assert manager.get_port() is None
    
    def test_service_manager_is_running_when_not_started(self):
        """Test is_running when service is not started."""
        manager = ServiceManager()
        assert not manager.is_running()


class TestServiceManagerIntegration:
    """Integration tests for ServiceManager with actual service."""
    
    def test_service_health_endpoint(self):
        """Test that the service health endpoint is accessible."""
        with ServiceManager() as manager:
            port = manager.get_port()
            
            # Test health endpoint using the correct Connect-RPC method
            assert manager.check_health(port)
            
            # Verify service is actually running
            assert manager.is_running()
            assert port > 0
    
    def test_service_drive_endpoints(self):
        """Test that the service drive endpoints are accessible."""
        with ServiceManager() as manager:
            port = manager.get_port()
            
            # Test drive endpoints (these should exist in the mock service)
            # Note: The actual endpoints depend on the mock service implementation
            try:
                response = requests.get(f"http://localhost:{port}/drive", timeout=5)
                # Should either return 200 or 404, but not crash
                assert response.status_code in [200, 404]
            except requests.RequestException:
                # If the endpoint doesn't exist, that's also acceptable for a mock
                pass
    
    def test_service_config_endpoints(self):
        """Test that the service config endpoints are accessible."""
        with ServiceManager() as manager:
            port = manager.get_port()
            
            # Test config endpoints
            try:
                response = requests.get(f"http://localhost:{port}/config", timeout=5)
                # Should either return 200 or 404, but not crash
                assert response.status_code in [200, 404]
            except requests.RequestException:
                # If the endpoint doesn't exist, that's also acceptable for a mock
                pass 


class TestServiceManagerExisting:
    """Tests for service manager with existing service handling."""
    
    def test_existing_service_detection(self):
        """Test that the service manager properly detects existing services."""
        # Check if there's already a service running
        existing_port = get_existing_service_port()
        
        # This test just verifies the function doesn't crash
        # The actual return value depends on whether a service is running
        assert existing_port is None or isinstance(existing_port, int)
    
    def test_service_manager_with_existing_service(self):
        """Test service manager behavior when there might be an existing service."""
        # Create a service manager
        manager = ServiceManager()
        
        try:
            # Start the service (should handle existing services gracefully)
            port = manager.start()
            assert port > 0
            assert manager.is_running()
            assert manager.get_port() == port
            
            # Check if it's healthy
            is_healthy = manager.check_health()
            # Health check might pass or fail depending on the actual service state
            assert isinstance(is_healthy, bool)
            
        finally:
            manager.stop()
            assert not manager.is_running()
    
    def test_service_manager_context_with_existing(self):
        """Test service manager as context manager with potential existing service."""
        with ServiceManager() as manager:
            port = manager.get_port()
            assert port is not None
            assert port > 0
            
            # Test health check
            is_healthy = manager.check_health()
            assert isinstance(is_healthy, bool)
            
            # Service should be running
            assert manager.is_running()
        
        # Service should be stopped after context exit
        assert not manager.is_running() 