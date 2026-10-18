# Your Own PaaS/IDP as Code - Python Example

This is a Python port of the [spore-drive-idp-example](https://github.com/taubyte/spore-drive-idp-example) that demonstrates how to create a custom cloud platform using Tau, an open-source PaaS/IDP, deployed across your servers using Spore-drive.

## Overview

Creating a custom cloud platform brings powerful advantages—cost savings, full control, data sovereignty, and, crucially, the ability to make your solution self-hostable. This tool helps you deploy Tau, an open-source PaaS/IDP, across your servers using Spore-drive.

## Features

- **Python-native**: Written in Python with async/await support
- **Type-safe**: Uses dataclasses and type hints for better development experience
- **Context managers**: Automatic resource cleanup with async context managers
- **Error handling**: Comprehensive error handling and validation
- **Progress monitoring**: Real-time deployment progress tracking
- **Configuration management**: Environment-based configuration with validation
- **CSV management**: Dedicated CSV handler for server management with validation
- **DNS management**: Namecheap DNS client for automatic DNS record updates
- **Progress visualization**: Advanced progress bars with multi-host support

## Prerequisites

1. **Python 3.8+** with pip
2. **Spore Drive Python client** installed
3. **Linux servers** with SSH access on port 22
4. **User account** with sudo/root privileges and SSH key authentication

## Installation

1. **Install Dependencies**
   ```bash
   pip install -r requirements.txt
   ```

2. **Setup Example Files**
   ```bash
   python displace.py setup
   ```

3. **Configure Environment**
   ```bash
   cp .env.example .env
   # Edit .env with your values
   ```

## Configuration

### Environment Variables

The following variables can be configured in your `.env` file:

```bash
# Server Configuration
SSH_KEY=ssh-key.pem                    # Path to SSH private key
SERVERS_CSV_PATH=hosts.csv             # Path to servers list
SSH_USER=ssh-user                      # SSH user for server access

# Domain Configuration
ROOT_DOMAIN=pom.ac                     # Root domain for your platform
GENERATED_DOMAIN=g.pom.ac              # Generated subdomain for your platform

# Namecheap DNS Configuration (Optional)
NAMECHEAP_API_KEY=your_api_key
NAMECHEAP_IP=your_ip
NAMECHEAP_USERNAME=your_username
```

### CSV File Format

The servers CSV file should contain the following columns:

- `hostname`: The fully qualified domain name of your server
- `public_ip`: The public IP address of your server

Example:
```csv
hostname,public_ip
node1.mycloud.com,203.0.113.1
node2.mycloud.com,203.0.113.2
```

### CSV Handler

The example includes a dedicated `csv_handler.py` module that provides:

- **Validation**: IP address and hostname validation
- **Error handling**: Detailed error messages for CSV issues
- **File operations**: Save, load, and merge CSV files
- **Information**: Get detailed information about CSV files
- **Utilities**: Create example files and validate formats

```python
from csv_handler import CSVHandler, ServerInfo

# Load servers with validation
servers = CSVHandler.load_servers_from_csv('hosts.csv')

# Validate CSV format
is_valid = CSVHandler.validate_csv_format('hosts.csv')

# Get CSV information
info = CSVHandler.get_csv_info('hosts.csv')
print(f"Server count: {info['server_count']}")

# Save servers to CSV
CSVHandler.save_servers_to_csv(servers, 'backup.csv')

# Merge multiple CSV files
CSVHandler.merge_csv_files(['file1.csv', 'file2.csv'], 'merged.csv')
```

### Namecheap DNS Client

The example includes a `namecheap_client.py` module that provides DNS management functionality:

- **DNS Record Management**: Add, update, and delete DNS records
- **API Integration**: Direct integration with Namecheap DNS API
- **Async Support**: Full async/await support for all operations
- **Validation**: DNS record validation and error handling
- **Sandbox Support**: Support for both sandbox and production APIs

```python
from namecheap_client import NamecheapDnsClient, update_domain_a_records

# Create and initialize client
client = NamecheapDnsClient(
    api_user='your_username',
    api_key='your_api_key',
    client_ip='your_ip',
    domain='example.com',
    sandbox=False
)
await client.init()

# Add DNS records
await client.add_a_record('www', '192.168.1.1', '3600')
await client.add_cname_record('api', 'api.example.com', '1800')

# Update A records for a subdomain
await update_domain_a_records(
    api_user='your_username',
    api_key='your_api_key',
    client_ip='your_ip',
    domain='example.com',
    subdomain='www',
    addresses=['192.168.1.1', '192.168.1.2']
)

# Commit changes
await client.commit()
```

### Progress Module

The example includes a `progress.py` module that provides deployment progress visualization:

- **Multiple Progress Bars**: One progress bar per host
- **Real-time Updates**: Live progress updates with task names
- **Error Handling**: Collects and displays deployment errors
- **Fallback Support**: Works with or without tqdm library
- **Summary Display**: Final deployment status summary

```python
from progress import display_progress, ProgressDisplay

# Simple usage
await display_progress(course, use_bars=True)

# Advanced usage with custom settings
pd = ProgressDisplay(use_bars=True)
await pd.display_progress(course)

# Check progress bar capabilities
from progress import is_tqdm_available
if is_tqdm_available():
    print("Progress bars available")
```

## Usage

### Basic Deployment

```bash
python displace.py
```

### Setup Only

```bash
python displace.py setup
```

## Code Structure

The example is organized into a main `IDPDeployer` class with several key methods:

### Configuration Loading

```python
# Load environment configuration and server list
deployer = IDPDeployer()
deployer.load_configuration()
```

### Cloud Configuration

```python
async def setup_cloud_domains(self) -> None:
    """Set up cloud domain configuration."""
    # Set domain configuration
    await self.config.cloud.domain.root.set(self.env_config.root_domain)
    await self.config.cloud.domain.generated.set(self.env_config.generated_domain)
    
    # Generate keys if they don't exist
    await self._ensure_domain_validation_keys()
    await self._ensure_p2p_swarm_keys()
```

### Authentication Configuration

```python
async def setup_authentication(self) -> None:
    """Set up SSH authentication."""
    main_auth = self.config.auth.signer("main")
    await main_auth.username.set(self.env_config.ssh_user)
    
    # Configure SSH key
    await main_auth.key.path.set("keys/ssh-key.pem")
    await main_auth.key.data.set(self._read_ssh_key())
    
    await self.config.commit()
```

### Shapes Configuration

```python
async def setup_service_shapes(self) -> None:
    """Set up service shapes configuration."""
    all_shape = self.config.shapes.get("all")
    await all_shape.services.set([
        "auth", "tns", "hoarder", "seer", "substrate", "patrick", "monkey"
    ])
    await all_shape.ports.port("main").set(4242)
    await all_shape.ports.port("lite").set(4262)
```

### Hosts Configuration

```python
async def setup_hosts(self) -> None:
    """Set up hosts configuration."""
    existing_hosts = await self.config.hosts.list()
    bootstrappers = []
    
    for server in self.servers:
        if server.hostname not in existing_hosts:
            host = self.config.hosts.get(server.hostname)
            bootstrappers.append(server.hostname)
            
            # Configure host
            await host.addresses.add([f"{server.public_ip}/32"])
            await host.ssh.address.set(f"{server.public_ip}:22")
            await host.ssh.auth.add(["main"])
            await host.location.set("40.730610, -73.935242")
            
            # Generate shape instance
            if "all" not in await host.shapes.list():
                await host.shapes.get("all").generate()
    
    # Set up P2P bootstrap
    await self.config.cloud.p2p.bootstrap.shape("all").nodes.add(bootstrappers)
```

### Deployment

```python
async def deploy_platform(self) -> None:
    """Deploy the platform using Spore Drive."""
    drive = await Drive.with_latest_tau(self.config)
    async with drive:
        course_config = CourseConfig(
            shapes=["all"],
            concurrency=2,
            timeout=600,
            retries=3
        )
        
        async with await drive.plot(course_config) as course:
            await course.displace()
            await display_progress(course)
```