#!/usr/bin/env python3
"""
Simple test to verify mock server connectivity.

This test ensures that the mock server can be started and basic
connectivity works before running the full integration tests.
"""

import os
import socket
import sys
import subprocess
from urllib.parse import urlparse

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.service_manager import check_service_health
from tests.mock import start_mock, stop_mock


def _answers(url: str) -> bool:
    try:
        socket.create_connection(("localhost", urlparse(url).port), timeout=5).close()
    except ConnectionRefusedError:
        return False
    return True


def test_mock_server_startup():
    """Test that the mock server can be started and responds."""
    proc, url = start_mock()
    try:
        assert check_service_health(urlparse(url).port), "Mock server health check failed"
    finally:
        stop_mock(proc)


def test_a_stopped_mock_no_longer_answers():
    proc, url = start_mock()
    try:
        assert _answers(url)
    finally:
        stop_mock(proc)
    assert not _answers(url)


def test_a_mock_ends_when_its_stdin_closes():
    """Closing the pipe is what killing its runner outright does to it."""
    proc, url = start_mock()
    try:
        proc.stdin.close()
        assert proc.wait(timeout=10) == 0
    finally:
        stop_mock(proc)
    assert not _answers(url)


def test_go_availability():
    """Test that Go is available."""
    try:
        result = subprocess.run(
            ["go", "version"], 
            check=True, 
            capture_output=True, 
            text=True
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        assert False, "Go is not available"

def test_python_dependencies():
    """Test that required Python dependencies are available."""
    required_packages = ["pytest", "pytest-asyncio", "pyyaml"]
    missing_packages = []
    
    for package in required_packages:
        try:
            if package == "pyyaml":
                __import__("yaml")
            else:
                __import__(package.replace("-", "_"))
        except ImportError:
            missing_packages.append(package)
    
    if missing_packages:
        assert False, f"Missing packages: {', '.join(missing_packages)}"

def main():
    """Main test function."""
    tests = [
        ("Go availability", test_go_availability),
        ("Python dependencies", test_python_dependencies),
        ("Mock server startup", test_mock_server_startup),
    ]
    
    all_passed = True
    
    for test_name, test_func in tests:
        if not test_func():
            all_passed = False
    
    if all_passed:
        return 0
    else:
        return 1

if __name__ == "__main__":
    exit(main()) 