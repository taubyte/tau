#!/usr/bin/env python3
"""
Simple test to verify mock server connectivity.

This test ensures that the mock server can be started and basic
connectivity works before running the full integration tests.
"""

import os
import sys
import subprocess
import time

# Add the spore_drive module to the path
sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

from spore_drive.service_manager import check_service_health

def test_mock_server_startup():
    """Test that the mock server can be started and responds."""
    mock_server_path = os.path.join(
        os.path.dirname(__file__), 
        "..", "..", "mock"
    )
    
    if not os.path.exists(mock_server_path):
        assert False, f"Mock server directory not found: {mock_server_path}"
    
    main_go_path = os.path.join(mock_server_path, "main.go")
    if not os.path.exists(main_go_path):
        assert False, f"Mock server main.go not found: {main_go_path}"
    
    try:
        process = subprocess.Popen(
            ["go", "run", "."],
            cwd=mock_server_path,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        
        time.sleep(3)
        
        if process.stdout:
            line = process.stdout.readline()
            if line:
                url = line.strip()
                
                try:
                    port = int(url.split(":")[-1].rstrip("/"))
                    
                    if check_service_health(port):
                        success = True
                    else:
                        success = False
                except Exception as e:
                    success = False
                
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                
                assert success, "Mock server health check failed"
            else:
                process.terminate()
                process.wait()
                assert False, "Mock server didn't output URL"
        else:
            process.terminate()
            process.wait()
            assert False, "Mock server stdout not available"
            
    except Exception as e:
        assert False, f"Failed to start mock server: {e}"

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