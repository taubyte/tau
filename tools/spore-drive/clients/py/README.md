# Spore Drive Python Client

A Python client library for the Spore Drive service, providing a pythonic interface to manage configuration, drives, and course operations.

## Features

The client has been designed with modern Python best practices:

- **Context Managers**: Automatic resource cleanup with `async with` statements
- **Factory Methods**: Easy object creation with descriptive class methods
- **Dataclasses**: Type-safe configuration with validation
- **Async/Await**: Full async support for all operations
- **Type Hints**: Comprehensive type annotations throughout

## Structure

The client has been refactored into logical modules for better maintainability:

### Core Modules

- **`types.py`** - Dataclasses and type definitions (`TauBinarySource`, `CourseConfig`, `ServiceConfig`)
- **`clients.py`** - Low-level RPC client implementations (`ConfigClient`, `DriveClient`)
- **`operations.py`** - Configuration operation classes (`BaseOperation`, `Cloud`, `Hosts`, `Auth`, `Shapes`)
- **`config.py`** - Configuration management (`Config` class)
- **`drive.py`** - Drive and course management (`Drive`, `Course` classes)
- **`utils.py`** - Utility functions for tau sources and service management
- **`client.py`** - Main client module with public API exports

### Public API

The main entry point is through the `spore_drive` package:

```python
from spore_drive import (
    Config, Drive, Course,
    tau_latest, tau_version, tau_url, tau_path,
    start_spore_drive_service, stop_spore_drive_service
)
from spore_drive.types import CourseConfig, TauBinarySource
```

## Usage

### Context Managers (Recommended)

The most pythonic way to use the client is with context managers for automatic resource cleanup:

```python
import asyncio
from spore_drive import Config, Drive, start_spore_drive_service, stop_spore_drive_service
from spore_drive.types import CourseConfig

async def main():
    # Start service
    port = start_spore_drive_service()
    
    try:
        # Using context managers - no manual cleanup needed!
        async with Config.new() as config:
            async with Drive.with_latest_tau(config) as drive:
                # Create course configuration using dataclass
                course_config = CourseConfig(
                    shapes=["web", "database", "function"],
                    concurrency=2,
                    timeout=300,
                    retries=3
                )
                
                # Plot course with context manager
                async with await drive.plot(course_config) as course:
                    await course.displace()
                    
                    # Monitor progress
                    async for progress in course.progress():
                        print(f"Progress: {progress.progress}%")
                        if progress.progress >= 100:
                            break
                    
                    # All resources automatically cleaned up when context exits
    finally:
        stop_spore_drive_service()

asyncio.run(main())
```

### Factory Methods

Factory methods provide easy object creation with descriptive names:

```python
# Configuration factory methods
config = await Config.new()                    # New empty config
config = await Config.from_file("config.yaml") # From file
config = await Config.from_bytes(data)         # From bytes
config = await Config.create(source="path")    # Generic create

# Drive factory methods
drive = await Drive.with_latest_tau(config)           # Latest version
drive = await Drive.with_version(config, "1.0.0")     # Specific version
drive = await Drive.with_url(config, "https://...")   # From URL
drive = await Drive.with_path(config, "/path/to/tau") # From local path
drive = await Drive.create(config, tau=source)        # Generic create
```

### Dataclass Configuration

Use dataclasses for type-safe configuration with validation:

```python
from spore_drive.types import CourseConfig, TauBinarySource

# Course configuration with validation
course_config = CourseConfig(
    shapes=["shape1", "shape2"],
    concurrency=4,      # Must be >= 1
    timeout=600,        # Optional timeout
    retries=5           # Must be >= 0
)

# Tau binary sources
tau_latest = TauBinarySource.latest()
tau_version = TauBinarySource.version("1.0.0")
tau_url = TauBinarySource.url("https://example.com/tau.tar.gz")
tau_path = TauBinarySource.path("/local/path/to/tau.tar.gz")

# Validation errors
try:
    invalid_config = CourseConfig(concurrency=0)  # ValueError: must be >= 1
except ValueError as e:
    print(f"Validation error: {e}")
```

### Basic Configuration Management

```python
import asyncio
from spore_drive import Config, start_spore_drive_service, stop_spore_drive_service

async def main():
    # Start service
    port = start_spore_drive_service()
    
    # Create configuration using context manager
    async with Config.new() as config:
        # Access configuration sections
        cloud = config.cloud
        hosts = config.hosts
        auth = config.auth
        shapes = config.shapes
        
        # Commit changes
        await config.commit()
        # Config automatically freed when context exits
    
    stop_spore_drive_service()

asyncio.run(main())
```

### Drive Operations

```python
from spore_drive import Drive, tau_latest

async def drive_example():
    async with Config.new() as config:
        # Create drive with latest tau binary using context manager
        async with Drive.with_latest_tau(config) as drive:
            # Plot a course using dataclass
            course_config = CourseConfig(
                shapes=['shape1', 'shape2'],
                concurrency=4,
                timeout=300
            )
            
            async with await drive.plot(course_config) as course:
                # Start displacement
                await course.displace()
                
                # Monitor progress
                async for progress in course.progress():
                    print(f"Progress: {progress.progress}%")
                
                # Course automatically aborted when context exits
```

### Tau Binary Sources

The client supports multiple ways to specify tau binary sources:

```python
# Using utility functions
drive = Drive(config, tau=tau_latest())
drive = Drive(config, tau=tau_version("1.0.0"))
drive = Drive(config, tau=tau_url("https://example.com/tau.tar.gz"))
drive = Drive(config, tau=tau_path("/path/to/tau.tar.gz"))

# Using dataclasses directly
drive = Drive(config, tau=TauBinarySource.latest())
drive = Drive(config, tau=TauBinarySource.version("1.0.0"))
drive = Drive(config, tau=TauBinarySource.url("https://example.com/tau.tar.gz"))
drive = Drive(config, tau=TauBinarySource.path("/path/to/tau.tar.gz"))

# Using factory methods
drive = await Drive.with_latest_tau(config)
drive = await Drive.with_version(config, "1.0.0")
drive = await Drive.with_url(config, "https://example.com/tau.tar.gz")
drive = await Drive.with_path(config, "/path/to/tau.tar.gz")
```

## Service Management

The client includes utilities for managing the Spore Drive service:

```python
from spore_drive import (
    start_spore_drive_service,
    stop_spore_drive_service,
    get_spore_drive_service_port
)

# Start service and get port
port = start_spore_drive_service()

# Check if service is running
existing_port = get_spore_drive_service_port()

# Stop service
stop_spore_drive_service()
```

## Testing

### Integration Tests

The Python client includes comprehensive integration tests that use the same mock server technique as the TypeScript tests. These tests verify the complete functionality of the Config class and related operations.

#### Running Integration Tests

```bash
# Run all integration tests
python run_integration_tests.py

# Run pytest integration tests only
pytest test_config_pytest.py -v

# Run manual integration tests only
python test_config_integration.py
```

#### Test Files

- **`test_config_integration.py`**: Manual integration tests with custom test runner
- **`test_config_pytest.py`**: Pytest-compatible integration tests with fixtures
- **`run_integration_tests.py`**: Test runner script that checks dependencies and runs all tests

#### Test Coverage

The integration tests cover:

- Cloud domain configuration (root, generated, validation keys)
- P2P swarm key generation and management
- Authentication signer management (add, list, delete)
- Host configuration and management
- Shape configuration and management
- Configuration bundle download and verification
- Configuration commit operations
- Comparison between different configuration creation methods

#### Mock Server

The tests use the same Go mock server (`../mock/main.go`) as the TypeScript tests, ensuring consistent behavior across language implementations.

### Unit Tests

```bash
# Run unit tests
pytest tests/ -v

# Run specific test file
pytest tests/test_service_manager.py -v
```

## Development

### Module Responsibilities

- **`types.py`**: Dataclasses and type definitions with validation
- **`clients.py`**: RPC communication layer
- **`operations.py`**: Configuration operation interfaces
- **`config.py`**: High-level configuration management with factory methods
- **`drive.py`**: Drive and course operations with context managers
- **`utils.py`**: Helper functions and utilities
- **`client.py`**: Public API exports

### Adding New Features

1. Add dataclasses to `types.py` if needed
2. Implement RPC calls in `clients.py`
3. Create operation classes in `operations.py` for configuration operations
4. Add high-level interfaces in `config.py` or `drive.py`
5. Export new functionality in `client.py` and `__init__.py`
6. Add corresponding integration tests

### Test Requirements

To run the integration tests, you need:

- Go (for the mock server)
- Python packages: `pytest`, `pytest-asyncio`, `pyyaml`

Install dependencies:
```bash
pip install pytest pytest-asyncio pyyaml
```

## Examples

See the following example files for complete working examples:

- **`example_pythonic.py`**: Demonstrates all new pythonic features
- **`example_usage.py`**: Basic usage patterns
- **`example.py`**: Original example with manual resource management

## Migration Guide

### From Legacy Usage

If you're upgrading from the previous version:

```python
# Old way (still supported for backward compatibility)
config = Config()
await config.init()
drive = Drive(config, tau=True)
await drive.init()
await drive.free()
await config.free()

# New way (recommended)
async with Config.new() as config:
    async with Drive.with_latest_tau(config) as drive:
        # Use drive
        pass  # Resources automatically cleaned up

# Old way
course_config = {"shapes": ["web"], "concurrency": 2}

# New way
course_config = CourseConfig(shapes=["web"], concurrency=2)
``` 