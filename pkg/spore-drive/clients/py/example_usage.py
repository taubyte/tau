#!/usr/bin/env python3
"""
Example usage of the Spore Drive Python client.

This example demonstrates how to use the refactored client modules
to work with Spore Drive configuration and drive operations.
"""

import asyncio
from spore_drive import (
    Config, Drive, Course,
    tau_latest, tau_version, tau_url, tau_path,
    start_spore_drive_service, stop_spore_drive_service
)


async def main():
    """Main example function."""
    
    port = start_spore_drive_service()
    
    try:
        # Method 1: Create new configuration
        config = Config()
        await config.init()
        
        # Method 2: Load from directory
        # config = await Config.from_directory("/path/to/config/directory")
        
        # Method 3: Load from archive data
        # config_data = b"configuration_archive_data"
        # config = await Config.from_archive(config_data)
        
        cloud_config = config.cloud
        hosts_config = config.hosts
        auth_config = config.auth
        shapes_config = config.shapes
        
        drive = Drive(config, tau=tau_latest())
        await drive.init()
        
        course_config = {
            'shapes': ['shape1', 'shape2'],
            'concurrency': 4
        }
        
        course = await drive.plot(course_config)
        
        await course.displace()
        
        async for progress in course.progress():
            if progress.progress >= 100:
                break
        
        await course.abort()
        await drive.free()
        await config.free()
        
    except Exception as e:
        pass
    
    finally:
        stop_spore_drive_service()


async def config_examples():
    """Examples of different configuration loading methods."""
    print("=== Configuration Examples ===")
    
    port = start_spore_drive_service()
    
    try:
        # Create new empty configuration
        print("Creating new configuration...")
        config_new = await Config.new()
        print(f"New config ID: {config_new.id}")
        await config_new.free()
        
        # Load from directory
        print("Loading from directory...")
        try:
            config_dir = await Config.from_directory("/path/to/config/directory")
            print(f"Directory config ID: {config_dir.id}")
            await config_dir.free()
        except Exception as e:
            print(f"Directory loading failed: {e}")
        
        # Load from archive data
        print("Loading from archive data...")
        config_data = b"example_configuration_archive_data"
        config_archive = await Config.from_archive(config_data)
        print(f"Archive config ID: {config_archive.id}")
        await config_archive.free()
        
    finally:
        stop_spore_drive_service()


if __name__ == "__main__":
    # Run the main example
    asyncio.run(main())
    
    # Run configuration examples
    asyncio.run(config_examples()) 