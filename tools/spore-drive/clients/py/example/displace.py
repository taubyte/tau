#!/usr/bin/env python3
"""
Your Own PaaS/IDP as Code - A Spore Drive Example

This example demonstrates how to create a custom cloud platform using Tau,
an open-source PaaS/IDP, deployed across your servers using Spore-drive.

This is a Python port of the TypeScript example from:
https://github.com/taubyte/spore-drive-idp-example
"""

import asyncio
import os
import sys
from pathlib import Path
from typing import List, Optional
from dataclasses import dataclass

# Load environment variables from .env file
try:
    from dotenv import load_dotenv
    load_dotenv()
except ImportError:
    print("Warning: python-dotenv not installed. Install with: pip install python-dotenv")

# Rich imports for better terminal output
from rich.console import Console
from rich.status import Status

from spore_drive import (
    Config, Drive, Course,
    start_spore_drive_service, stop_spore_drive_service
)
from spore_drive.types import CourseConfig

from csv_handler import CSVHandler, ServerInfo
from namecheap_client import NamecheapDnsClient
from progress import display_progress
from utils import (
    console, create_example_files, check_required_files,
    print_banner, print_success_message, print_service_status,
    print_configuration_committed
)


@dataclass
class EnvironmentConfig:
    """Environment configuration for the IDP deployment."""
    # Server Configuration
    ssh_key: str = "ssh-key.pem"
    servers_csv_path: str = "hosts.csv"
    ssh_user: str = "ssh-user"
    
    # Domain Configuration
    root_domain: str = "pom.ac"
    generated_domain: str = "g.pom.ac"
    
    # Namecheap DNS Configuration (Optional)
    namecheap_api_key: Optional[str] = None
    namecheap_ip: Optional[str] = None
    namecheap_username: Optional[str] = None


class IDPDeployer:
    """Main class for deploying the IDP platform."""
    
    def __init__(self, config_path: str = "config"):
        self.config_path = Path(config_path)
        self.config_path.mkdir(exist_ok=True)
        self.config: Optional[Config] = None
        self.env_config: Optional[EnvironmentConfig] = None
        self.servers: List[ServerInfo] = []
    
    def load_configuration(self) -> None:
        """Load environment configuration and server list."""
        with console.status("[bold blue]📋 Loading configuration...") as status:
            # Load environment config
            self.env_config = self._load_environment_config()
            console.log(f"✓ Domains: {self.env_config.root_domain}, {self.env_config.generated_domain}")
            
            # Load servers from CSV
            self.servers = self._load_servers()
            console.log(f"✓ Servers: {len(self.servers)} loaded")
    
    def _load_environment_config(self) -> EnvironmentConfig:
        """Load configuration from environment variables."""
        return EnvironmentConfig(
            ssh_key=os.getenv("SSH_KEY", "ssh-key.pem"),
            servers_csv_path=os.getenv("SERVERS_CSV_PATH", "hosts.csv"),
            ssh_user=os.getenv("SSH_USER", "ssh-user"),
            root_domain=os.getenv("ROOT_DOMAIN", "pom.ac"),
            generated_domain=os.getenv("GENERATED_DOMAIN", "g.pom.ac"),
            namecheap_api_key=os.getenv("NAMECHEAP_API_KEY"),
            namecheap_ip=os.getenv("NAMECHEAP_IP"),
            namecheap_username=os.getenv("NAMECHEAP_USERNAME")
        )
    
    def _load_servers(self) -> List[ServerInfo]:
        """Load server information from CSV file."""
        try:
            servers = CSVHandler.load_servers_from_csv(self.env_config.servers_csv_path)
            for server in servers:
                console.log(f"  - {server.hostname} ({server.public_ip})")
            return servers
        except Exception as e:
            raise RuntimeError(f"Failed to load servers from {self.env_config.servers_csv_path}: {e}")
    
    async def initialize_config(self) -> None:
        """Initialize the Spore Drive configuration."""
        with console.status("[bold green]🔧 Initializing configuration...") as status:
            self.config = await Config.from_directory(str(self.config_path))
            console.log("✓ Configuration created")
    
    async def setup_cloud_domains(self) -> None:
        """Set up cloud domain configuration."""
        with console.status("[bold cyan]🌐 Setting up cloud domains...") as status:
            # Set domain configuration
            await self.config.cloud.domain.root.set(self.env_config.root_domain)
            await self.config.cloud.domain.generated.set(self.env_config.generated_domain)
            
            # Generate keys if they don't exist
            await self._ensure_domain_validation_keys()
            await self._ensure_p2p_swarm_keys()
            
            console.log("✓ Cloud domains configured")
    
    async def _ensure_domain_validation_keys(self) -> None:
        """Ensure domain validation keys exist."""
        try:
            await self.config.cloud.domain.validation.keys.data.private_key.get()
        except Exception:
            await self.config.cloud.domain.validation.generate()
    
    async def _ensure_p2p_swarm_keys(self) -> None:
        """Ensure P2P swarm keys exist."""
        try:
            await self.config.cloud.p2p.swarm.key.data.get()
        except Exception:
            await self.config.cloud.p2p.swarm.generate()
    
    async def setup_authentication(self) -> None:
        """Set up SSH authentication."""
        with console.status("[bold yellow]🔑 Setting up authentication...") as status:
            main_auth = self.config.auth.signer("main")
            await main_auth.username.set(self.env_config.ssh_user)
            
            # Configure SSH key
            await main_auth.key.path.set("keys/ssh-key.pem")
            await main_auth.key.data.set(self._read_ssh_key())
            
            await self.config.commit()
            console.log("✓ Authentication configured")
    
    def _read_ssh_key(self) -> bytes:
        """Read SSH key from file."""
        try:
            with open(self.env_config.ssh_key, 'rb') as f:
                key_data = f.read()
            
            if not key_data:
                raise ValueError("SSH key file is empty")
            
            return key_data
        except FileNotFoundError:
            raise RuntimeError(f"SSH key file not found: {self.env_config.ssh_key}")
        except PermissionError:
            raise RuntimeError(f"Permission denied reading SSH key: {self.env_config.ssh_key}")
    
    async def setup_service_shapes(self) -> None:
        """Set up service shapes configuration."""
        with console.status("[bold magenta]📦 Setting up service shapes...") as status:
            all_shape = self.config.shapes.get("all")
            await all_shape.services.set([
                "auth", "tns", "hoarder", "seer", "substrate", "patrick", "monkey"
            ])
            await all_shape.ports.port("main").set(4242)
            await all_shape.ports.port("lite").set(4262)
            
            console.log("✓ Service shapes configured")
    
    async def setup_hosts(self) -> None:
        """Set up hosts configuration."""
        with console.status("[bold green]🖥️  Setting up hosts...") as status:
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
                    
                    console.log(f"  ✓ {server.hostname} ({server.public_ip})")
            
            # Set up P2P bootstrap
            await self.config.cloud.p2p.bootstrap.shape("all").nodes.add(bootstrappers)
            
            console.log(f"✓ {len(self.servers)} hosts configured")
    
    async def deploy_platform(self) -> None:
        """Deploy the platform using Spore Drive."""
        console.print("🚀 [bold red]Deploying platform...[/bold red]")
        
        drive = await Drive.with_latest_tau(self.config)
        async with drive:
            console.log("✓ Drive initialized")
            
            course_config = CourseConfig(
                shapes=["all"],
                concurrency=2,
                timeout=600,
                retries=3
            )
            
            async with await drive.plot(course_config) as course:
                console.log("✓ Course plotted")
                await course.displace()
                await display_progress(course)
        
        console.log("✓ Platform deployed")
    
    async def update_dns_records(self) -> None:
        """Update DNS records if Namecheap is configured."""
        if not self._is_namecheap_configured():
            console.log("🌐 Skipping DNS update (Namecheap not configured)")
            return
        
        with console.status("[bold blue]🌐 Updating DNS records...") as status:
            try:
                await self._update_namecheap_dns()
                console.log("✓ DNS records updated")
            except Exception as e:
                console.log(f"⚠️  DNS update failed: {e}")
    
    def _is_namecheap_configured(self) -> bool:
        """Check if Namecheap DNS is configured."""
        return all([
            self.env_config.namecheap_username,
            self.env_config.namecheap_api_key,
            self.env_config.namecheap_ip
        ])
    
    async def _update_namecheap_dns(self) -> None:
        """Update DNS records using Namecheap API."""
        # Extract generated domain prefix
        if self.env_config.generated_domain.endswith(f".{self.env_config.root_domain}"):
            generated_prefix = self.env_config.generated_domain[:-len(self.env_config.root_domain) - 1]
        else:
            generated_prefix = self.env_config.generated_domain
        
        # Get seer addresses
        seer_addrs = []
        for hostname in await self.config.hosts.list():
            if "all" in await self.config.hosts.get(hostname).shapes.list():
                for addr in await self.config.hosts.get(hostname).addresses.list():
                    seer_addrs.append(addr.split("/")[0])
        
        # Create client and update DNS
        client = NamecheapDnsClient(
            self.env_config.namecheap_username,
            self.env_config.namecheap_api_key,
            self.env_config.namecheap_ip,
            self.env_config.root_domain,
            False
        )
        await client.init()
        
        client.set_all("seer", "A", seer_addrs)
        client.set_all("tau", "NS", [f"seer.{self.env_config.root_domain}."])
        client.set_all(f"*.{generated_prefix}", "CNAME", [f"substrate.tau.{self.env_config.root_domain}."])
        
        await client.commit()
    
    async def deploy(self) -> None:
        """Main deployment method."""
        print_banner()
        
        # Load configuration
        self.load_configuration()
        console.print()
        
        # Start Spore Drive service
        port = start_spore_drive_service()
        print_service_status(port, "started")
        
        try:
            # Initialize and configure
            await self.initialize_config()
            await self.setup_cloud_domains()
            await self.setup_authentication()
            await self.setup_service_shapes()
            await self.setup_hosts()
            
            # Commit configuration
            await self.config.commit()
            print_configuration_committed()
            
            # Deploy and update DNS
            await self.deploy_platform()
            await self.update_dns_records()
            
        finally:
            stop_spore_drive_service()
            print_service_status(port, "stopped")
        
        print_success_message(
            self.env_config.root_domain,
            self.env_config.generated_domain,
            len(self.servers)
        )
    






async def main() -> int:
    """Main entry point."""
    if len(sys.argv) > 1 and sys.argv[1] == "setup":
        create_example_files()
        return 0
    
    # Check for required files
    if not check_required_files():
        return 1
    
    # Deploy the platform
    await IDPDeployer().deploy()
    return 0


if __name__ == "__main__":
    exit_code = asyncio.run(main())
    sys.exit(exit_code) 