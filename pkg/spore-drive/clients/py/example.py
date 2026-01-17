#!/usr/bin/env python3
"""
Example usage of the Spore Drive Python client.

This example demonstrates how to use the Config, Drive, and Course classes
to manage Spore Drive operations with the integrated service manager.
"""

import asyncio
from spore_drive import (
    Config, Drive, Course, 
    tau_latest, tau_version, tau_url,
    start_spore_drive_service, stop_spore_drive_service, get_spore_drive_service_port
)


async def main():
    """Main example function."""
    
    existing_port = get_spore_drive_service_port()
    if existing_port:
        pass
    else:
        port = start_spore_drive_service()
    
    # Create new configuration
    config = Config()
    await config.init()
    
    # Alternative: Load from directory
    # config = await Config.from_directory("/path/to/config/directory")
    
    # Alternative: Load from archive data
    # config_data = b"configuration_archive_data"
    # config = await Config.from_archive(config_data)
    
    cloud_config = config.cloud
    hosts_config = config.hosts
    auth_config = config.auth
    shapes_config = config.shapes
    
    await config.commit()
    
    drive = Drive(config, tau=tau_latest())
    await drive.init()
    
    drive_version = Drive(config, tau=tau_version("1.0.0"))
    await drive_version.init()
    
    drive_url = Drive(config, tau=tau_url("https://example.com/tau.tar.gz"))
    await drive_url.init()
    
    course_config = {
        "shapes": ["web", "database", "function"],
        "concurrency": 2
    }
    
    course = await drive.plot(course_config)
    
    await course.displace()
    
    async for progress in course.progress():
        if progress.progress >= 100:
            break
    
    try:
        await course.abort()
    except Exception as e:
        pass
    
    await drive.free()
    await config.free()


async def config_loading_examples():
    """Example showing different ways to load configurations."""
    print("=== Configuration Loading Examples ===")
    
    port = start_spore_drive_service()
    
    try:
        # Method 1: Create new empty configuration
        print("Creating new configuration...")
        config_new = await Config.new()
        print(f"New config ID: {config_new.id}")
        await config_new.free()
        
        # Method 2: Load from directory
        print("Loading from directory...")
        try:
            config_from_dir = await Config.from_directory("/path/to/config/directory")
            print(f"Loaded config ID: {config_from_dir.id}")
            await config_from_dir.free()
        except Exception as e:
            print(f"Could not load from directory: {e}")
        
        # Method 3: Load from archive data
        print("Loading from archive data...")
        config_data = b"example_configuration_archive_data"
        config_from_archive = await Config.from_archive(config_data)
        print(f"Archive config ID: {config_from_archive.id}")
        await config_from_archive.free()
        
    finally:
        stop_spore_drive_service()


async def service_management_example():
    """Example showing service management patterns."""
    
    try:
        port = start_spore_drive_service()
        
        existing_port = get_spore_drive_service_port()
        if existing_port:
            pass
        
        config = Config()
        await config.init()
        
        await config.free()
        
    except Exception as e:
        pass
    finally:
        stop_spore_drive_service()


async def error_handling_example():
    """Example showing error handling patterns."""
    
    try:
        config = Config()
        await config.init()
        
        drive = Drive(config, tau=tau_latest())
        await drive.init()
        
    except RuntimeError as e:
        pass
    except Exception as e:
        pass
    finally:
        try:
            if 'drive' in locals():
                await drive.free()
            if 'config' in locals():
                await config.free()
        except Exception as e:
            pass


if __name__ == "__main__":
    # Run the main example
    asyncio.run(main())
    
    # Run configuration loading examples
    asyncio.run(config_loading_examples())
    
    # Run service management example
    asyncio.run(service_management_example())
    
    # Run error handling example
    asyncio.run(error_handling_example()) 