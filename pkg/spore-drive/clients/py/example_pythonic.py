#!/usr/bin/env python3
"""
Example usage of the improved Spore Drive Python client.

This example demonstrates the new pythonic features:
1. Context managers for automatic resource cleanup
2. Factory methods for easier object creation
3. Dataclasses for better configuration validation
"""

import asyncio
from spore_drive import (
    Config, Drive, Course, 
    tau_latest, tau_version, tau_url, tau_path,
    start_spore_drive_service, stop_spore_drive_service
)
from spore_drive.types import CourseConfig, TauBinarySource


async def context_manager_example():
    """Example using context managers for automatic resource cleanup."""
    print("=== Context Manager Example ===")
    
    # Start service
    port = start_spore_drive_service()
    
    try:
        # Using context managers - no manual cleanup needed!
        async with Config.new() as config:
            print(f"Config ID: {config.id}")
            
            # Access configuration sections
            cloud = config.cloud
            hosts = config.hosts
            auth = config.auth
            shapes = config.shapes
            
            # Commit changes
            await config.commit()
            
            # Create drive with context manager
            async with Drive.with_latest_tau(config) as drive:
                print(f"Drive initialized with latest tau")
                
                # Create course configuration using dataclass
                course_config = CourseConfig(
                    shapes=["web", "database", "function"],
                    concurrency=2,
                    timeout=300,
                    retries=3
                )
                
                # Plot course with context manager
                async with await drive.plot(course_config) as course:
                    print("Course plotted successfully")
                    
                    # Start displacement
                    await course.displace()
                    
                    # Monitor progress
                    async for progress in course.progress():
                        print(f"Progress: {progress.progress}%")
                        if progress.progress >= 100:
                            break
                    
                    print("Course completed successfully")
                    # Course automatically aborted when context exits
    
    finally:
        stop_spore_drive_service()


async def factory_methods_example():
    """Example using factory methods for easier object creation."""
    print("\n=== Factory Methods Example ===")
    
    port = start_spore_drive_service()
    
    try:
        # Factory methods for different configuration sources
        print("Creating configurations from different sources...")
        
        # Load from directory
        try:
            config_from_dir = await Config.from_directory("/path/to/config/directory")
            print(f"Loaded config from directory: {config_from_dir.id}")
            await config_from_dir.free()
        except Exception as e:
            print(f"Could not load from directory: {e}")
        
        # Load from archive data
        config_data = b"example_configuration_archive_data"
        config_from_archive = await Config.from_archive(config_data)
        print(f"Loaded config from archive: {config_from_archive.id}")
        await config_from_archive.free()
        
        # Create new empty config
        config_new = await Config.new()
        print(f"Created new config: {config_new.id}")
        await config_new.free()
        
        # Factory methods for different tau sources
        config = await Config.new()
        drive_latest = await Drive.with_latest_tau(config)
        drive_version = await Drive.with_version(config, "1.0.0")
        drive_url = await Drive.with_url(config, "https://example.com/tau.tar.gz")
        drive_path = await Drive.with_path(config, "/local/path/to/tau.tar.gz")
        
        # Clean up
        await drive_latest.free()
        await drive_version.free()
        await drive_url.free()
        await drive_path.free()
        await config.free()
        
    finally:
        stop_spore_drive_service()


async def dataclass_validation_example():
    """Example demonstrating dataclass validation."""
    print("\n=== Dataclass Validation Example ===")
    
    port = start_spore_drive_service()
    
    try:
        async with Config.new() as config:
            async with Drive.with_latest_tau(config) as drive:
                # Valid course configuration
                valid_config = CourseConfig(
                    shapes=["shape1", "shape2"],
                    concurrency=4,
                    timeout=600,
                    retries=5
                )
                
                print("Valid config created successfully")
                
                # Invalid configurations will raise ValueError
                try:
                    invalid_config = CourseConfig(
                        shapes=["shape1"],
                        concurrency=0,  # Invalid: must be >= 1
                        retries=-1      # Invalid: must be >= 0
                    )
                except ValueError as e:
                    print(f"Validation error caught: {e}")
                
                # Create course with valid config
                async with await drive.plot(valid_config) as course:
                    await course.displace()
                    
                    async for progress in course.progress():
                        if progress.progress >= 100:
                            break
    
    finally:
        stop_spore_drive_service()


async def tau_source_examples():
    """Example demonstrating different tau source types."""
    print("\n=== Tau Source Examples ===")
    
    port = start_spore_drive_service()
    
    try:
        async with Config.new() as config:
            # Different ways to specify tau sources
            sources = [
                ("Latest", tau_latest()),
                ("Version", tau_version("1.0.0")),
                ("URL", tau_url("https://example.com/tau.tar.gz")),
                ("Path", tau_path("/local/path/to/tau.tar.gz")),
                ("Direct dataclass", TauBinarySource.latest()),
                ("Direct dataclass with version", TauBinarySource.version("2.0.0"))
            ]
            
            for name, source in sources:
                print(f"Creating drive with {name} tau source: {source}")
                async with Drive.create(config, tau=source) as drive:
                    print(f"Drive created successfully with {name} source")
    
    finally:
        stop_spore_drive_service()


async def error_handling_example():
    """Example demonstrating improved error handling with context managers."""
    print("\n=== Error Handling Example ===")
    
    port = start_spore_drive_service()
    
    try:
        async with Config.new() as config:
            async with Drive.with_latest_tau(config) as drive:
                # Even if an exception occurs, resources are automatically cleaned up
                try:
                    # Simulate an error
                    raise RuntimeError("Simulated error during course operation")
                    
                    async with await drive.plot(CourseConfig(shapes=["test"])) as course:
                        await course.displace()
                        
                except RuntimeError as e:
                    print(f"Error caught: {e}")
                    print("Resources automatically cleaned up by context managers")
    
    finally:
        stop_spore_drive_service()


async def main():
    """Run all examples."""
    await context_manager_example()
    await factory_methods_example()
    await dataclass_validation_example()
    await tau_source_examples()
    await error_handling_example()
    
    print("\n=== All examples completed successfully! ===")


if __name__ == "__main__":
    asyncio.run(main()) 