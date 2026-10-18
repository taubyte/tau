#!/usr/bin/env python3
"""
Simple script to test that protobuf imports work correctly.
"""

def test_imports():
    """Test that all protobuf imports work."""
    try:
        from spore_drive.proto.drive.v1 import drive_pb2
        from spore_drive.proto.config.v1 import config_pb2
        from spore_drive.proto.health.v1 import health_pb2
        
        config = config_pb2.Config(id="test-config")
        drive = drive_pb2.Drive(id="test-drive")
        
    except ImportError as e:
        assert False, f"Import error: {e}"
    except Exception as e:
        assert False, f"Unexpected error: {e}"


if __name__ == "__main__":
    test_imports() 