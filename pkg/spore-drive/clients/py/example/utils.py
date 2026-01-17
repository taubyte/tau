#!/usr/bin/env python3
"""
Utility functions for the Spore Drive example.

This module contains helper functions for file creation, validation,
and other utility operations.
"""

import os
from rich.console import Console
from csv_handler import CSVHandler

# Initialize console
console = Console()


def create_example_files() -> None:
    """Create example configuration files."""
    with console.status("[bold blue]📝 Creating example files...") as status:
        # Create example CSV
        CSVHandler.create_example_csv("hosts.csv")
        console.log("✓ hosts.csv created")
        
        # Create example .env
        if not os.path.exists(".env"):
            with open(".env", 'w') as f:
                f.write("""# Server Configuration
SSH_KEY=ssh-key.pem
SERVERS_CSV_PATH=hosts.csv
SSH_USER=ssh-user

# Domain Configuration
ROOT_DOMAIN=pom.ac
GENERATED_DOMAIN=g.pom.ac

# Namecheap DNS Configuration (Optional)
# NAMECHEAP_API_KEY=your_api_key_here
# NAMECHEAP_IP=your_ip_here
# NAMECHEAP_USERNAME=your_username_here
""")
            console.log("✓ .env created")
        else:
            console.log("✓ .env already exists")
    
    console.print("\n📋 [bold cyan]Next steps:[/bold cyan]")
    console.print("1. Install dependencies: pip install -r requirements.txt")
    console.print("2. Edit .env with your configuration")
    console.print("3. Edit hosts.csv with your server information")
    console.print("4. Run: python displace.py")


def check_required_files() -> bool:
    """Check if required files exist."""
    required_files = ["hosts.csv"]
    
    for file_path in required_files:
        if not os.path.exists(file_path):
            console.print(f"❌ [bold red]Error: {file_path} not found![/bold red]")
            console.print("Run 'python displace.py setup' to create example files.")
            return False
    
    return True


def validate_environment() -> bool:
    """Validate environment configuration."""
    if not os.path.exists(".env"):
        console.print("⚠️  [bold yellow]Warning: .env file not found[/bold yellow]")
        console.print("Run 'python displace.py setup' to create example files.")
        return False
    
    # Check for required environment variables
    required_vars = ["SSH_KEY", "SSH_USER"]
    missing_vars = []
    
    for var in required_vars:
        if not os.getenv(var):
            missing_vars.append(var)
    
    if missing_vars:
        console.print(f"⚠️  [bold yellow]Warning: Missing environment variables: {', '.join(missing_vars)}[/bold yellow]")
        console.print("Please update your .env file with the required values.")
        return False
    
    return True


def print_banner() -> None:
    """Print the application banner."""
    console.print("=== [bold blue]Your Own PaaS/IDP as Code - Spore Drive Example[/bold blue] ===\n")


def print_success_message(root_domain: str, generated_domain: str, server_count: int) -> None:
    """Print deployment success message."""
    console.print("\n=== [bold green]🎉 Deployment completed successfully![/bold green] ===")
    console.print(f"Your custom PaaS/IDP is now running on:")
    console.print(f"  🌐 [cyan]Root domain:[/cyan] {root_domain}")
    console.print(f"  🚀 [cyan]Generated domain:[/cyan] {generated_domain}")
    console.print(f"  🖥️  [cyan]Across {server_count} servers[/cyan]")


def print_service_status(port: int, action: str = "started") -> None:
    """Print service status message."""
    if action == "started":
        console.print(f"🔧 [bold green]Spore Drive service started on port {port}[/bold green]\n")
    elif action == "stopped":
        console.print("🔧 [bold yellow]Spore Drive service stopped[/bold yellow]")


def print_configuration_committed() -> None:
    """Print configuration committed message."""
    console.print("✓ [bold green]Configuration committed[/bold green]\n") 