"""
Service manager for starting and managing the spore-drive service.
"""

import os
import subprocess
import time
import tempfile
import json
import platform
import sys
from pathlib import Path
from typing import Optional
import requests
import tarfile
import urllib.request
from urllib.parse import urlparse
from .proto.health.v1 import health_pb2


class ServiceManager:
    """Manages the spore-drive service."""
    
    def __init__(self, timeout: int = 10):
        self.timeout = timeout
        self.process: Optional[subprocess.Popen] = None
        self.port: Optional[int] = None
        self.temp_dir: Optional[tempfile.TemporaryDirectory] = None
        
        # Service configuration
        self.service_version = "0.1.5"
        self.binary_dir = Path(__file__).parent / "bin"
        self.binary_name = "drive.exe" if platform.system() == "Windows" else "drive"
        self.binary_path = self.binary_dir / self.binary_name
        self.version_file_path = self.binary_dir / "version.txt"
        self.run_file_path = self._get_config_dir() / ".spore-drive.run"
    
    def _get_config_dir(self) -> Path:
        """Get the configuration directory based on platform."""
        system = platform.system()
        if system == "Windows":
            return Path(os.environ.get("APPDATA", Path.home() / "AppData" / "Roaming"))
        elif system == "Darwin":
            return Path.home() / "Library" / "Application Support"
        else:
            return Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
    
    def _binary_exists(self) -> bool:
        """Check if the binary exists."""
        return self.binary_path.exists()
    
    def _version_matches(self) -> bool:
        """Check if the installed version matches the expected version."""
        if not self.version_file_path.exists():
            return False
        try:
            installed_version = self.version_file_path.read_text().strip()
            return installed_version == self.service_version
        except (IOError, OSError):
            return False
    
    def _parse_asset_name(self) -> tuple[Optional[str], Optional[str]]:
        """Parse the asset name for the current platform."""
        system = platform.system().lower()
        machine = platform.machine().lower()
        
        # Map system names
        os_map = {
            "darwin": "darwin",
            "linux": "linux", 
            "windows": "windows"
        }
        
        # Map architecture names
        arch_map = {
            "x86_64": "amd64",
            "amd64": "amd64",
            "aarch64": "arm64",
            "arm64": "arm64"
        }
        
        return os_map.get(system), arch_map.get(machine)
    
    def _download_and_extract_binary(self):
        """Download and extract the binary from GitHub releases."""
        if self._binary_exists() and self._version_matches():
            return
        
        current_os, current_arch = self._parse_asset_name()
        if not current_os or not current_arch:
            raise RuntimeError(f"Unsupported OS or architecture: {platform.system()}/{platform.machine()}")
        
        asset_name = f"spore-drive-service_{self.service_version}_{current_os}_{current_arch}.tar.gz"
        asset_url = f"https://github.com/taubyte/spore-drive/releases/download/v{self.service_version}/{asset_name}"
        

        
        # Create binary directory if it doesn't exist
        self.binary_dir.mkdir(parents=True, exist_ok=True)
        
        # Download the asset
        tar_path = self.binary_dir / asset_name
        try:
            urllib.request.urlretrieve(asset_url, tar_path)
        except Exception as e:
            raise RuntimeError(f"Failed to download binary: {e}")
        
        # Extract the tar.gz file
        try:
            with tarfile.open(tar_path, 'r:gz') as tar:
                tar.extractall(self.binary_dir)
            tar_path.unlink()  # Remove the tar file
        except Exception as e:
            raise RuntimeError(f"Failed to extract binary: {e}")
        
        # Write version file
        self.version_file_path.write_text(self.service_version)
        
        # Make binary executable on Unix systems
        if platform.system() != "Windows":
            os.chmod(self.binary_path, 0o755)
    
    def _load_run_file(self) -> Optional[dict]:
        """Load the run file to get port and PID information."""
        if self.run_file_path.exists():
            try:
                run_data = self.run_file_path.read_text()
                return json.loads(run_data)
            except (json.JSONDecodeError, IOError):
                pass
        return None
    
    def _is_process_running(self, pid: int) -> bool:
        """Check if a process with the given PID is running."""
        try:
            os.kill(pid, 0)
            return True
        except OSError:
            return False
    
    def _is_service_up(self, port: int) -> bool:
        """Check if the service is up on the given port using Connect-RPC health endpoint."""
        try:
            # Connect-RPC health endpoint expects a POST request with protobuf data
            # The endpoint is /health.v1.HealthService/Ping
            url = f"http://localhost:{port}/health.v1.HealthService/Ping"
            
            # Connect-RPC expects specific headers and protobuf format
            headers = {
                "Content-Type": "application/proto",
                "Connect-Protocol-Version": "1"
            }
            
            # Send an empty protobuf message (Empty message)
            empty_message = health_pb2.Empty()
            empty_proto = empty_message.SerializeToString()
            
            response = requests.post(url, data=empty_proto, headers=headers, timeout=1)
            return response.status_code == 200
        except requests.RequestException:
            return False
    
    def _wait_for_health_check(self, port: int, max_retries: int = 5, retry_delay: float = 0.5) -> bool:
        """Wait for health check to pass with retries."""
        for attempt in range(max_retries):
            if self._is_service_up(port):
                return True
            else:
                if attempt < max_retries - 1:
                    time.sleep(retry_delay)
        return False
    
    def _execute_binary(self):
        """Execute the binary as a detached process."""
        if not self._binary_exists():
            raise RuntimeError("Binary not found. Please run the install script.")
        
        try:
            self.process = subprocess.Popen(
                [str(self.binary_path)],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                start_new_session=True
            )
        except Exception as e:
            raise RuntimeError(f"Failed to execute binary: {e}")
    
    def _get_port(self, timeout: int = 3500) -> Optional[int]:
        """Get the port from the run file with timeout."""
        start_time = time.time() * 1000  # Convert to milliseconds
        
        while (time.time() * 1000) - start_time < timeout:
            run_file = self._load_run_file()
            if run_file:
                if self._is_process_running(run_file.get('pid', 0)):
                    port = run_file.get('port')
                    if self._wait_for_health_check(port, max_retries=5):
                        return port
                    else:
                        return port
            time.sleep(0.5)
        
        return None
    
    def start(self) -> int:
        """Start the service and return the port it's running on."""
        if self.process is not None:
            return self.port
        
        port = self._get_port()
        if port is not None:
            self.port = port
            return port
        
        # Download and extract binary if needed
        self._download_and_extract_binary()
        
        # Execute the binary
        self._execute_binary()
        
        # Wait for the service to start and get the port
        port = self._get_port()
        if port is None:
            raise RuntimeError("Failed to start service")
        
        if not self._wait_for_health_check(port, max_retries=10, retry_delay=1.0):
            pass
        
        self.port = port
        return port
    
    def stop(self):
        """Stop the service."""
        if self.process is not None:
            try:
                self.process.terminate()
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
            finally:
                self.process = None
                self.port = None
        
        # Also try to kill any existing service using the run file
        run_file = self._load_run_file()
        if run_file and self._is_process_running(run_file.get('pid', 0)):
            try:
                os.kill(run_file.get('pid'), 15)  # SIGTERM
                if self.run_file_path.exists():
                    self.run_file_path.unlink()
            except OSError:
                pass
        
        if self.temp_dir is not None:
            self.temp_dir.cleanup()
            self.temp_dir = None
    
    def get_port(self) -> Optional[int]:
        """Get the port the service is running on."""
        return self.port
    
    def is_running(self) -> bool:
        """Check if the service is running."""
        if self.process is None:
            # Check if there's a service running via run file
            run_file = self._load_run_file()
            if run_file:
                return self._is_process_running(run_file.get('pid', 0))
            return False
        return self.process.poll() is None
    
    def has_existing_service(self) -> tuple[bool, Optional[int]]:
        """Check if there's an existing service running and return (is_running, port)."""
        run_file = self._load_run_file()
        if run_file and self._is_process_running(run_file.get('pid', 0)):
            return True, run_file.get('port')
        return False, None
    
    def check_health(self, port: Optional[int] = None) -> bool:
        """Check the health of the service."""
        if port is None:
            port = self.port
        if port is None:
            return False
        return self._is_service_up(port)
    
    def __enter__(self):
        """Context manager entry."""
        self.start()
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit."""
        self.stop()


# Global service manager instance
_service_manager: Optional[ServiceManager] = None


def get_service_manager() -> ServiceManager:
    """Get the global service manager instance."""
    global _service_manager
    if _service_manager is None:
        _service_manager = ServiceManager()
    return _service_manager


def start_service() -> int:
    """Start the service and return the port."""
    return get_service_manager().start()


def stop_service():
    """Stop the service."""
    global _service_manager
    if _service_manager is not None:
        _service_manager.stop()
        _service_manager = None


def get_existing_service_port() -> Optional[int]:
    """Get the port of an existing service if one is running."""
    manager = get_service_manager()
    is_running, port = manager.has_existing_service()
    return port if is_running else None


def check_service_health(port: Optional[int] = None) -> bool:
    """Check the health of the service."""
    manager = get_service_manager()
    return manager.check_health(port) 