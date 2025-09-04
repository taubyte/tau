#!/usr/bin/env python3
"""
CSV Handler for Server Management

This module provides functionality for reading and writing server information
from/to CSV files, including validation and error handling.
"""

import csv
import os
from typing import List, Dict, Optional
from dataclasses import dataclass


@dataclass
class ServerInfo:
    """Server information from CSV file."""
    hostname: str
    public_ip: str
    
    def __post_init__(self):
        """Validate server information after initialization."""
        if not self.hostname or not self.hostname.strip():
            raise ValueError("Hostname cannot be empty")
        if not self.public_ip or not self.public_ip.strip():
            raise ValueError("Public IP cannot be empty")
        
        # Basic IP validation (simple check)
        ip_parts = self.public_ip.split('.')
        if len(ip_parts) != 4:
            raise ValueError(f"Invalid IP address format: {self.public_ip}")
        
        try:
            for part in ip_parts:
                if not 0 <= int(part) <= 255:
                    raise ValueError(f"Invalid IP address: {self.public_ip}")
        except ValueError:
            raise ValueError(f"Invalid IP address: {self.public_ip}")


class CSVHandler:
    """Handler for CSV operations related to server management."""
    
    REQUIRED_COLUMNS = {'hostname', 'public_ip'}
    
    @classmethod
    def load_servers_from_csv(cls, csv_path: str) -> List[ServerInfo]:
        """
        Load server information from CSV file.
        
        Args:
            csv_path: Path to the CSV file
            
        Returns:
            List of ServerInfo objects
            
        Raises:
            FileNotFoundError: If CSV file doesn't exist
            ValueError: If CSV format is invalid or empty
        """
        if not os.path.exists(csv_path):
            raise FileNotFoundError(f"CSV file not found: {csv_path}")
        
        servers = []
        
        with open(csv_path, 'r', newline='', encoding='utf-8') as csvfile:
            reader = csv.DictReader(csvfile)
            
            # Validate required columns
            if not reader.fieldnames:
                raise ValueError("CSV file is empty or has no headers")
            
            missing_columns = cls.REQUIRED_COLUMNS - set(reader.fieldnames)
            if missing_columns:
                raise ValueError(f"CSV missing required columns: {missing_columns}")
            
            # Read and validate rows
            for row_num, row in enumerate(reader, start=2):  # Start at 2 for header row
                try:
                    # Clean and validate data
                    hostname = row['hostname'].strip()
                    public_ip = row['public_ip'].strip()
                    
                    if not hostname or not public_ip:
                        raise ValueError(f"Row {row_num}: Empty hostname or public_ip")
                    
                    server = ServerInfo(hostname=hostname, public_ip=public_ip)
                    servers.append(server)
                    
                except KeyError as e:
                    raise ValueError(f"Row {row_num}: Missing column {e}")
                except ValueError as e:
                    raise ValueError(f"Row {row_num}: {e}")
        
        if not servers:
            raise ValueError(f"No valid servers found in CSV file: {csv_path}")
        
        return servers
    
    @classmethod
    def save_servers_to_csv(cls, servers: List[ServerInfo], csv_path: str) -> None:
        """
        Save server information to CSV file.
        
        Args:
            servers: List of ServerInfo objects
            csv_path: Path to save the CSV file
            
        Raises:
            ValueError: If servers list is empty
        """
        if not servers:
            raise ValueError("Cannot save empty servers list")
        
        # Create directory if it doesn't exist
        os.makedirs(os.path.dirname(csv_path) if os.path.dirname(csv_path) else '.', exist_ok=True)
        
        with open(csv_path, 'w', newline='', encoding='utf-8') as csvfile:
            writer = csv.writer(csvfile)
            
            # Write header
            writer.writerow(['hostname', 'public_ip'])
            
            # Write server data
            for server in servers:
                writer.writerow([server.hostname, server.public_ip])
    
    @classmethod
    def create_example_csv(cls, csv_path: str = "hosts.csv") -> None:
        """
        Create an example CSV file with sample server data.
        
        Args:
            csv_path: Path to create the CSV file
        """
        example_servers = [
            ServerInfo(hostname="node1.mycloud.com", public_ip="203.0.113.1"),
            ServerInfo(hostname="node2.mycloud.com", public_ip="203.0.113.2"),
        ]
        
        cls.save_servers_to_csv(example_servers, csv_path)
        print(f"Example CSV file created: {csv_path}")
        print("Please edit the file with your actual server information.")
    
    @classmethod
    def validate_csv_format(cls, csv_path: str) -> bool:
        """
        Validate CSV file format without loading all data.
        
        Args:
            csv_path: Path to the CSV file
            
        Returns:
            True if format is valid, False otherwise
        """
        try:
            if not os.path.exists(csv_path):
                return False
            
            with open(csv_path, 'r', newline='', encoding='utf-8') as csvfile:
                reader = csv.DictReader(csvfile)
                
                if not reader.fieldnames:
                    return False
                
                missing_columns = cls.REQUIRED_COLUMNS - set(reader.fieldnames)
                return len(missing_columns) == 0
                
        except Exception:
            return False
    
    @classmethod
    def get_csv_info(cls, csv_path: str) -> Dict[str, any]:
        """
        Get information about a CSV file.
        
        Args:
            csv_path: Path to the CSV file
            
        Returns:
            Dictionary with CSV information
        """
        info = {
            'exists': False,
            'valid_format': False,
            'server_count': 0,
            'columns': [],
            'errors': []
        }
        
        if not os.path.exists(csv_path):
            info['errors'].append(f"File not found: {csv_path}")
            return info
        
        info['exists'] = True
        
        try:
            with open(csv_path, 'r', newline='', encoding='utf-8') as csvfile:
                reader = csv.DictReader(csvfile)
                
                if reader.fieldnames:
                    info['columns'] = list(reader.fieldnames)
                    info['valid_format'] = cls.validate_csv_format(csv_path)
                    
                    # Count valid rows
                    row_count = 0
                    for row in reader:
                        try:
                            hostname = row.get('hostname', '').strip()
                            public_ip = row.get('public_ip', '').strip()
                            if hostname and public_ip:
                                row_count += 1
                        except Exception:
                            pass
                    
                    info['server_count'] = row_count
                else:
                    info['errors'].append("No headers found in CSV file")
                    
        except Exception as e:
            info['errors'].append(f"Error reading CSV: {e}")
        
        return info
    
    @classmethod
    def merge_csv_files(cls, csv_files: List[str], output_path: str) -> None:
        """
        Merge multiple CSV files into one.
        
        Args:
            csv_files: List of CSV file paths to merge
            output_path: Path for the merged CSV file
            
        Raises:
            ValueError: If no valid CSV files provided
        """
        all_servers = []
        
        for csv_file in csv_files:
            try:
                servers = cls.load_servers_from_csv(csv_file)
                all_servers.extend(servers)
            except Exception as e:
                print(f"Warning: Could not load {csv_file}: {e}")
        
        if not all_servers:
            raise ValueError("No valid servers found in any CSV file")
        
        # Remove duplicates based on hostname
        unique_servers = {}
        for server in all_servers:
            unique_servers[server.hostname] = server
        
        cls.save_servers_to_csv(list(unique_servers.values()), output_path)
        print(f"Merged {len(unique_servers)} unique servers to: {output_path}")


# Convenience functions for backward compatibility
def load_servers_from_csv(csv_path: str) -> List[ServerInfo]:
    """Convenience function to load servers from CSV."""
    return CSVHandler.load_servers_from_csv(csv_path)


def create_example_csv(csv_path: str = "hosts.csv") -> None:
    """Convenience function to create example CSV."""
    return CSVHandler.create_example_csv(csv_path) 