#!/usr/bin/env python3
"""
Namecheap DNS Client

This module provides a Python client for managing DNS records through the Namecheap API.
It's a port of the TypeScript implementation from the spore-drive-idp-example.
"""

import asyncio
import xml.etree.ElementTree as ET
from typing import List, Optional, Dict, Any
from dataclasses import dataclass
import aiohttp
import urllib.parse


@dataclass
class DnsRecord:
    """DNS record structure."""
    name: str
    type: str
    address: str
    ttl: Optional[str] = None
    
    def __post_init__(self):
        """Validate DNS record after initialization."""
        if not self.name or not self.name.strip():
            raise ValueError("DNS record name cannot be empty")
        if not self.type or not self.type.strip():
            raise ValueError("DNS record type cannot be empty")
        if not self.address or not self.address.strip():
            raise ValueError("DNS record address cannot be empty")
        
        # Validate record type
        valid_types = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'SRV', 'NS']
        if self.type.upper() not in valid_types:
            raise ValueError(f"Invalid DNS record type: {self.type}")


class NamecheapDnsClient:
    """Namecheap DNS API client for managing DNS records."""
    
    def __init__(
        self,
        api_user: str,
        api_key: str,
        client_ip: str,
        domain: str,
        sandbox: bool = False
    ):
        """
        Initialize the Namecheap DNS client.
        
        Args:
            api_user: Namecheap API username
            api_key: Namecheap API key
            client_ip: Client IP address
            domain: Domain to manage
            sandbox: Use sandbox API (default: False)
        """
        self.api_user = api_user
        self.api_key = api_key
        self.client_ip = client_ip
        self.domain = domain
        self.sandbox = sandbox
        self.records: List[DnsRecord] = []
        
        # Set up base URL
        if sandbox:
            self.base_url = "https://api.sandbox.namecheap.com/xml.response"
        else:
            self.base_url = "https://api.namecheap.com/xml.response"
    
    async def init(self) -> None:
        """Initialize the client by fetching current DNS records."""
        try:
            sld, tld = self.domain.split(".", 1)
            
            params = {
                'ApiUser': self.api_user,
                'ApiKey': self.api_key,
                'UserName': self.api_user,
                'ClientIp': self.client_ip,
                'Command': 'namecheap.domains.dns.getHosts',
                'SLD': sld,
                'TLD': tld,
            }
            
            async with aiohttp.ClientSession() as session:
                async with session.get(self.base_url, params=params) as response:
                    if response.status != 200:
                        raise Exception(f"API request failed with status {response.status}")
                    
                    data = await response.text()
                    self.records = await self._parse_dns_records(data)
                    
        except Exception as e:
            raise Exception(f"Failed to initialize Namecheap DNS client: {e}")
    
    async def _parse_dns_records(self, data: str) -> List[DnsRecord]:
        """
        Parse DNS records from XML response.
        
        Args:
            data: XML response data
            
        Returns:
            List of DNS records
        """
        try:
            root = ET.fromstring(data)
            
            # Check for API errors
            status = root.get('Status')
            if status != 'OK':
                error_elem = root.find('.//Error')
                error_msg = error_elem.text if error_elem is not None else "Unknown error"
                raise Exception(f"Namecheap API error: {error_msg}")
            
            # Find DNS records
            hosts_elem = root.find('.//host')
            if hosts_elem is None:
                return []
            
            records = []
            for host in root.findall('.//host'):
                record = DnsRecord(
                    name=host.get('Name', ''),
                    type=host.get('Type', ''),
                    address=host.get('Address', ''),
                    ttl=host.get('TTL')
                )
                records.append(record)
            
            return records
            
        except ET.ParseError as e:
            raise Exception(f"Failed to parse XML response: {e}")
        except Exception as e:
            raise Exception(f"Failed to parse DNS records: {e}")
    
    def list(self) -> List[DnsRecord]:
        """Get all DNS records."""
        return self.records.copy()
    
    def add(self, record: DnsRecord) -> None:
        """Add a DNS record."""
        self.records.append(record)
    
    def delete(self, name: str, record_type: str) -> None:
        """Delete a specific DNS record by name and type."""
        self.records = [
            record for record in self.records
            if not (record.name == name and record.type == record_type)
        ]
    
    def delete_all(self, name: str, record_type: str) -> None:
        """Delete all DNS records with the given name and type."""
        self.records = [
            record for record in self.records
            if record.name != name or record.type != record_type
        ]
    
    def get_all(self, name: str, record_type: str) -> List[DnsRecord]:
        """Get all DNS records with the given name and type."""
        return [
            record for record in self.records
            if record.name == name and record.type == record_type
        ]
    
    def set_all(self, name: str, record_type: str, addresses: List[str]) -> None:
        """Set all DNS records for a given name and type."""
        # Remove existing records
        self.delete_all(name, record_type)
        
        # Add new records
        for address in addresses:
            record = DnsRecord(name=name, type=record_type, address=address)
            self.records.append(record)
    
    async def commit(self) -> None:
        """Commit DNS record changes to Namecheap."""
        try:
            sld, tld = self.domain.split(".", 1)
            
            # Build parameters
            params = {
                'ApiUser': self.api_user,
                'ApiKey': self.api_key,
                'UserName': self.api_user,
                'ClientIp': self.client_ip,
                'Command': 'namecheap.domains.dns.setHosts',
                'SLD': sld,
                'TLD': tld,
            }
            
            # Add DNS records
            for i, record in enumerate(self.records, 1):
                params[f'HostName{i}'] = record.name
                params[f'RecordType{i}'] = record.type
                params[f'Address{i}'] = record.address
                params[f'TTL{i}'] = record.ttl or '1800'
            
            async with aiohttp.ClientSession() as session:
                async with session.get(self.base_url, params=params) as response:
                    if response.status != 200:
                        raise Exception(f"API request failed with status {response.status}")
                    
                    data = await response.text()
                    
                    # Parse response to check for errors
                    root = ET.fromstring(data)
                    status = root.get('Status')
                    if status != 'OK':
                        error_elem = root.find('.//Error')
                        error_msg = error_elem.text if error_elem is not None else "Unknown error"
                        raise Exception(f"Failed to commit DNS records: {error_msg}")
                    
        except Exception as e:
            raise Exception(f"Failed to commit DNS records: {e}")
    
    async def add_a_record(self, name: str, address: str, ttl: Optional[str] = None) -> None:
        """Add an A record."""
        record = DnsRecord(name=name, type='A', address=address, ttl=ttl)
        self.add(record)
    
    async def add_cname_record(self, name: str, target: str, ttl: Optional[str] = None) -> None:
        """Add a CNAME record."""
        record = DnsRecord(name=name, type='CNAME', address=target, ttl=ttl)
        self.add(record)
    
    async def add_txt_record(self, name: str, text: str, ttl: Optional[str] = None) -> None:
        """Add a TXT record."""
        record = DnsRecord(name=name, type='TXT', address=text, ttl=ttl)
        self.add(record)
    
    async def update_a_records(self, name: str, addresses: List[str]) -> None:
        """Update A records for a given name."""
        self.set_all(name, 'A', addresses)
    
    async def delete_a_records(self, name: str) -> None:
        """Delete all A records for a given name."""
        self.delete_all(name, 'A')
    
    def get_a_records(self, name: str) -> List[DnsRecord]:
        """Get all A records for a given name."""
        return self.get_all(name, 'A')
    
    def get_cname_records(self, name: str) -> List[DnsRecord]:
        """Get all CNAME records for a given name."""
        return self.get_all(name, 'CNAME')
    
    def get_txt_records(self, name: str) -> List[DnsRecord]:
        """Get all TXT records for a given name."""
        return self.get_all(name, 'TXT')
    
    def print_records(self) -> None:
        """Print all DNS records in a readable format."""
        print(f"DNS Records for {self.domain}:")
        print("-" * 50)
        
        if not self.records:
            print("No DNS records found.")
            return
        
        for i, record in enumerate(self.records, 1):
            ttl_info = f" (TTL: {record.ttl})" if record.ttl else ""
            print(f"{i}. {record.name} {record.type} {record.address}{ttl_info}")
    
    def export_to_dict(self) -> Dict[str, Any]:
        """Export DNS records to dictionary format."""
        return {
            'domain': self.domain,
            'sandbox': self.sandbox,
            'records': [
                {
                    'name': record.name,
                    'type': record.type,
                    'address': record.address,
                    'ttl': record.ttl
                }
                for record in self.records
            ]
        }
    
    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> 'NamecheapDnsClient':
        """Create client from dictionary data."""
        client = cls(
            api_user=data['api_user'],
            api_key=data['api_key'],
            client_ip=data['client_ip'],
            domain=data['domain'],
            sandbox=data.get('sandbox', False)
        )
        
        # Load records
        for record_data in data.get('records', []):
            record = DnsRecord(
                name=record_data['name'],
                type=record_data['type'],
                address=record_data['address'],
                ttl=record_data.get('ttl')
            )
            client.records.append(record)
        
        return client


# Convenience functions for common operations
async def create_namecheap_client(
    api_user: str,
    api_key: str,
    client_ip: str,
    domain: str,
    sandbox: bool = False
) -> NamecheapDnsClient:
    """
    Create and initialize a Namecheap DNS client.
    
    Args:
        api_user: Namecheap API username
        api_key: Namecheap API key
        client_ip: Client IP address
        domain: Domain to manage
        sandbox: Use sandbox API (default: False)
        
    Returns:
        Initialized NamecheapDnsClient instance
    """
    client = NamecheapDnsClient(api_user, api_key, client_ip, domain, sandbox)
    await client.init()
    return client


async def update_domain_a_records(
    api_user: str,
    api_key: str,
    client_ip: str,
    domain: str,
    subdomain: str,
    addresses: List[str],
    sandbox: bool = False
) -> None:
    """
    Update A records for a subdomain.
    
    Args:
        api_user: Namecheap API username
        api_key: Namecheap API key
        client_ip: Client IP address
        domain: Domain to manage
        subdomain: Subdomain to update
        addresses: List of IP addresses
        sandbox: Use sandbox API (default: False)
    """
    client = await create_namecheap_client(api_user, api_key, client_ip, domain, sandbox)
    await client.update_a_records(subdomain, addresses)
    await client.commit()


async def add_domain_cname(
    api_user: str,
    api_key: str,
    client_ip: str,
    domain: str,
    subdomain: str,
    target: str,
    sandbox: bool = False
) -> None:
    """
    Add a CNAME record for a subdomain.
    
    Args:
        api_user: Namecheap API username
        api_key: Namecheap API key
        client_ip: Client IP address
        domain: Domain to manage
        subdomain: Subdomain to add CNAME for
        target: Target domain
        sandbox: Use sandbox API (default: False)
    """
    client = await create_namecheap_client(api_user, api_key, client_ip, domain, sandbox)
    await client.add_cname_record(subdomain, target)
    await client.commit() 