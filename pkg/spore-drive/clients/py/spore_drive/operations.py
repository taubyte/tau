"""
Configuration operation classes for Spore Drive.

This module contains the configuration operation classes that provide
a pythonic interface for managing different aspects of the configuration
including cloud settings, hosts, authentication, and shapes.
"""

from typing import List, Any, Dict, Optional, Union, AsyncIterable
from .proto.config.v1 import config_pb2
from .clients import ConfigClient


class BaseOperation:
    """Base class for configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        self.client = client
        self.config = config
        self.op_path = path
    
    async def _do_request(self, operation: Any) -> config_pb2.Return:
        """Execute operation request."""
        op = self._build_op(operation)
        final_op = config_pb2.Op(config=self.config)
        
        if isinstance(op, dict) and 'case' in op:
            case_name = op['case']
            if case_name == 'cloud':
                cloud_op = self._dict_to_protobuf(op['value'], config_pb2.Cloud)
                final_op.cloud.CopyFrom(cloud_op)
            elif case_name == 'hosts':
                hosts_op = self._dict_to_protobuf(op['value'], config_pb2.Hosts)
                final_op.hosts.CopyFrom(hosts_op)
            elif case_name == 'auth':
                auth_op = self._dict_to_protobuf(op['value'], config_pb2.Auth)
                final_op.auth.CopyFrom(auth_op)
            elif case_name == 'shapes':
                shapes_op = self._dict_to_protobuf(op['value'], config_pb2.Shapes)
                final_op.shapes.CopyFrom(shapes_op)
        
        try:
            result = await self.client.do(final_op)
            return result
        except Exception as e:
            return config_pb2.Return()
    
    def _build_op(self, operation: Any) -> Any:
        """Build operation with path."""
        op = operation
        for i in range(len(self.op_path) - 1, -1, -1):
            path_item = self.op_path[i]
            case_name = path_item["case"]
            
            is_terminal_oneof = (
                i == len(self.op_path) - 1 and 
                isinstance(op, dict) and 
                "case" in op and 
                "value" in op and
                op["case"] in ["delete", "generate", "list", "clear", "id"]
            )
            
            if is_terminal_oneof:
                message_value = {op["case"]: op["value"]}
                if "name" in path_item:
                    message_value["name"] = path_item["name"]
                if "shape" in path_item:
                    message_value["shape"] = path_item["shape"]
            else:
                message_value = {"op": op}
                if "name" in path_item:
                    message_value["name"] = path_item["name"]
                if "shape" in path_item:
                    message_value["shape"] = path_item["shape"]
            
            op = {"case": case_name, "value": message_value}
        return op
    
    def _dict_to_protobuf(self, op_dict, message_type):
        """Convert a nested operation dictionary to a protobuf message."""
        msg = message_type()
        if not isinstance(op_dict, dict):
            return op_dict  # base case for leaf values

        for k, v in op_dict.items():
            if k == "case":
                field_name = op_dict["case"]
                if "value" in op_dict:
                    value = op_dict["value"]
                    field = msg.DESCRIPTOR.fields_by_name.get(field_name)
                    if field is not None and field.message_type:
                        nested_type = field.message_type._concrete_class
                        nested_msg = self._dict_to_protobuf(value, nested_type)
                        getattr(msg, field_name).CopyFrom(nested_msg)
                    else:
                        setattr(msg, field_name, value)
                else:
                    setattr(msg, field_name, True)
            elif k == "value":
                continue
            elif k == "op":
                nested = self._dict_to_protobuf(v, message_type)
                for field in nested.DESCRIPTOR.fields:
                    if field.message_type is not None:
                        if nested.HasField(field.name):
                            getattr(msg, field.name).CopyFrom(getattr(nested, field.name))
                    else:
                        value = getattr(nested, field.name)
                        if value != field.default_value:
                            setattr(msg, field.name, value)
            elif k == "shape" or k == "name":
                setattr(msg, k, v)

            else:
                field = msg.DESCRIPTOR.fields_by_name.get(k)
                if field is not None and field.message_type:
                    nested_type = field.message_type._concrete_class
                    nested_msg = self._dict_to_protobuf(v, nested_type)
                    getattr(msg, k).CopyFrom(nested_msg)
                else:
                    setattr(msg, k, v)
        return msg



class DomainConfig:
    def __init__(self, root: Optional[str] = None, generated: Optional[str] = None):
        self.root = root
        self.generated = generated


class BootstrapConfig:
    def __init__(self, config: Optional[Dict[str, List[str]]] = None):
        self.config = config or {}


class P2PConfig:
    def __init__(self, bootstrap: Optional[BootstrapConfig] = None):
        self.bootstrap = bootstrap


class CloudConfig:
    def __init__(self, domain: Optional[DomainConfig] = None, p2p: Optional[P2PConfig] = None):
        self.domain = domain
        self.p2p = p2p


class SSHConfig:
    def __init__(self, addr: Optional[str] = None, port: Optional[int] = None, auth: Optional[List[str]] = None):
        self.addr = addr
        self.port = port
        self.auth = auth


class LocationConfig:
    def __init__(self, lat: float, long: float):
        self.lat = lat
        self.long = long


class HostConfig:
    def __init__(self, addr: Optional[List[str]] = None, ssh: Optional[SSHConfig] = None, location: Optional[LocationConfig] = None):
        self.addr = addr
        self.ssh = ssh
        self.location = location


class HostsConfig:
    def __init__(self, hosts: Optional[Dict[str, HostConfig]] = None):
        self.hosts = hosts or {}


class SignerConfig:
    def __init__(self, username: Optional[str] = None, password: Optional[str] = None, key: Optional[str] = None):
        self.username = username
        self.password = password
        self.key = key


class AuthConfig:
    def __init__(self, signers: Optional[Dict[str, SignerConfig]] = None):
        self.signers = signers or {}


class PortsConfig:
    def __init__(self, ports: Optional[Dict[str, int]] = None):
        self.ports = ports or {}


class ShapeConfig:
    def __init__(self, services: Optional[List[str]] = None, ports: Optional[PortsConfig] = None, plugins: Optional[List[str]] = None):
        self.services = services
        self.ports = ports
        self.plugins = plugins


class ShapesConfig:
    def __init__(self, shapes: Optional[Dict[str, ShapeConfig]] = None):
        self.shapes = shapes or {}



class StringOperation(BaseOperation):
    """String operation class for get/set operations."""
    
    async def set(self, value: str) -> None:
        """Set string value."""
        await self._do_request({"case": "set", "value": value})
    
    async def get(self) -> str:
        """Get string value."""
        result = await self._do_request({"case": "get", "value": True})
        if result.WhichOneof('return') == 'string':
            return result.string
        raise ValueError("String value does not exist")


class BytesOperation(BaseOperation):
    """Bytes operation class for binary data operations."""
    
    async def set(self, value: bytes) -> None:
        """Set bytes value."""
        await self._do_request({"case": "set", "value": value})
    
    async def get(self) -> bytes:
        """Get bytes value."""
        result = await self._do_request({"case": "get", "value": True})
        if result.WhichOneof('return') == 'bytes':
            return result.bytes
        raise ValueError("Bytes value does not exist")


class StringSliceOperation(BaseOperation):
    """String slice operation class for string array operations."""
    
    async def set(self, values: List[str]) -> None:
        """Set string slice values."""
        string_slice = config_pb2.StringSlice(value=values)
        await self._do_request({"case": "set", "value": string_slice})
    
    async def add(self, values: List[str]) -> None:
        """Add values to string slice."""
        string_slice = config_pb2.StringSlice(value=values)
        await self._do_request({"case": "add", "value": string_slice})
    
    async def delete(self, values: List[str]) -> None:
        """Delete values from string slice."""
        string_slice = config_pb2.StringSlice(value=values)
        await self._do_request({"case": "delete", "value": string_slice})
    
    async def clear(self) -> None:
        """Clear all values."""
        await self._do_request({"case": "clear", "value": True})
    
    async def list(self) -> List[str]:
        """List all values."""
        result = await self._do_request({"case": "list", "value": True})
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []



class Cloud(BaseOperation):
    """Cloud configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config):
        super().__init__(client, config, [{"case": "cloud"}])
    
    @property
    def domain(self) -> 'Domain':
        return Domain(self.client, self.config, self.op_path + [{"case": "domain"}])
    
    @property
    def p2p(self) -> 'P2P':
        return P2P(self.client, self.config, self.op_path + [{"case": "p2p"}])
    
    async def set(self, value: CloudConfig) -> None:
        """Set cloud configuration."""
        if value.domain:
            await self.domain.set(value.domain)
        if value.p2p:
            await self.p2p.set(value.p2p)


class Domain(BaseOperation):
    """Domain configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def root(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "root"}])
    
    @property
    def generated(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "generated"}])
    
    @property
    def validation(self) -> 'Validation':
        return Validation(self.client, self.config, self.op_path + [{"case": "validation"}])
    
    async def set(self, value: DomainConfig) -> None:
        """Set domain configuration."""
        if value.root:
            await self.root.set(value.root)
        if value.generated:
            await self.generated.set(value.generated)


class Validation(BaseOperation):
    """Validation configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def keys(self) -> 'ValidationKeys':
        return ValidationKeys(self.client, self.config, self.op_path + [{"case": "keys"}])
    
    async def generate(self) -> None:
        """Generate validation keys."""
        await self._do_request({"case": "generate", "value": True})


class ValidationKeys(BaseOperation):
    """Validation keys configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def path(self) -> 'ValidationKeysPath':
        return ValidationKeysPath(self.client, self.config, self.op_path + [{"case": "path"}])
    
    @property
    def data(self) -> 'ValidationKeysData':
        return ValidationKeysData(self.client, self.config, self.op_path + [{"case": "data"}])


class ValidationKeysPath(BaseOperation):
    """Validation keys path configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def private_key(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "private_key"}])
    
    @property
    def public_key(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "public_key"}])


class ValidationKeysData(BaseOperation):
    """Validation keys data configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def private_key(self) -> BytesOperation:
        return BytesOperation(self.client, self.config, self.op_path + [{"case": "private_key"}])
    
    @property
    def public_key(self) -> BytesOperation:
        return BytesOperation(self.client, self.config, self.op_path + [{"case": "public_key"}])


class P2P(BaseOperation):
    """P2P configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def bootstrap(self) -> 'Bootstrap':
        return Bootstrap(self.client, self.config, self.op_path + [{"case": "bootstrap"}])
    
    @property
    def swarm(self) -> 'Swarm':
        return Swarm(self.client, self.config, self.op_path + [{"case": "swarm"}])
    
    async def set(self, value: P2PConfig) -> None:
        """Set P2P configuration."""
        if value.bootstrap:
            await self.bootstrap.set(value.bootstrap)


class Bootstrap(BaseOperation):
    """Bootstrap configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    def shape(self, shape_name: str) -> 'BootstrapShape':
        return BootstrapShape(self.client, self.config, self.op_path + [{"case": "select", "shape": shape_name}])
    
    async def set(self, value: BootstrapConfig) -> None:
        """Set bootstrap configuration."""
        for shape_name, nodes in value.config.items():
            if nodes and len(nodes) > 0:
                await self.shape(shape_name).nodes.set(nodes)
    
    async def list(self) -> List[str]:
        """List bootstrap shapes."""
        result = await self._do_request({"case": "list", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []


class BootstrapShape(BaseOperation):
    """Bootstrap shape configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def nodes(self) -> StringSliceOperation:
        return StringSliceOperation(self.client, self.config, self.op_path + [{"case": "nodes"}])
    
    async def delete(self) -> None:
        """Delete bootstrap shape."""
        await self._do_request({"case": "delete", "value": True})


class Swarm(BaseOperation):
    """Swarm configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def key(self) -> 'SwarmKey':
        return SwarmKey(self.client, self.config, self.op_path + [{"case": "key"}])
    
    async def generate(self) -> None:
        """Generate swarm key."""
        await self._do_request({"case": "generate", "value": True})


class SwarmKey(BaseOperation):
    """Swarm key configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def path(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "path"}])
    
    @property
    def data(self) -> BytesOperation:
        return BytesOperation(self.client, self.config, self.op_path + [{"case": "data"}])



class Hosts(BaseOperation):
    """Hosts configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config):
        super().__init__(client, config, [{"case": "hosts"}])
    
    def get(self, name: str) -> 'Host':
        return Host(self.client, self.config, self.op_path + [{"case": "select", "name": name}])
    
    async def list(self) -> List[str]:
        """List all hosts."""
        result = await self._do_request({"case": "list", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []
    
    async def set(self, value: HostsConfig) -> None:
        """Set hosts configuration."""
        for name, config in value.hosts.items():
            await self.get(name).set(config)


class Host(BaseOperation):
    """Host configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def addresses(self) -> StringSliceOperation:
        return StringSliceOperation(self.client, self.config, self.op_path + [{"case": "addresses"}])
    
    @property
    def ssh(self) -> 'SSH':
        return SSH(self.client, self.config, self.op_path + [{"case": "ssh"}])
    
    @property
    def location(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "location"}])
    
    @property
    def shapes(self) -> 'HostShapes':
        return HostShapes(self.client, self.config, self.op_path + [{"case": "shapes"}])
    
    async def delete(self) -> None:
        """Delete host."""
        await self._do_request({"case": "delete", "value": True})
    
    async def set(self, value: HostConfig) -> None:
        """Set host configuration."""
        if value.addr:
            await self.addresses.set(value.addr)
        if value.ssh:
            await self.ssh.set(value.ssh)
        if value.location:
            location_str = f"{value.location.lat},{value.location.long}"
            await self.location.set(location_str)


class SSH(BaseOperation):
    """SSH configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def address(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "address"}])
    
    @property
    def auth(self) -> StringSliceOperation:
        return StringSliceOperation(self.client, self.config, self.op_path + [{"case": "auth"}])
    
    async def set(self, value: SSHConfig) -> None:
        """Set SSH configuration."""
        if value.addr:
            addr_str = value.addr
            if value.port and value.port > 0:
                addr_str += f":{value.port}"
            await self.address.set(addr_str)
        if value.auth:
            await self.auth.set(value.auth)


class HostShapes(BaseOperation):
    """Host shapes configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    def get(self, name: str) -> 'HostShape':
        return HostShape(self.client, self.config, self.op_path + [{"case": "select", "name": name}])
    
    async def list(self) -> List[str]:
        """List host shapes."""
        result = await self._do_request({"case": "list", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []


class HostShape(BaseOperation):
    """Host shape configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    async def id(self) -> str:
        """Get host shape ID."""
        return await self._instance.id()
    
    @property
    def key(self) -> StringOperation:
        return self._instance.key
    
    async def generate(self) -> None:
        """Generate host shape."""
        await self._instance.generate()
    
    @property
    def _instance(self) -> 'HostInstance':
        return HostInstance(self.client, self.config, self.op_path + [{"case": "select"}])
    
    async def delete(self) -> None:
        """Delete host shape."""
        await self._do_request({"case": "delete", "value": True})


class HostInstance(BaseOperation):
    """Host instance configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    async def id(self) -> str:
        """Get host instance ID."""
        result = await self._do_request({"case": "id", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'string':
            return result.string
        return ''
    
    @property
    def key(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "key"}])
    
    async def generate(self) -> None:
        """Generate host instance."""
        await self._do_request({"case": "generate", "value": True})



class Auth(BaseOperation):
    """Authentication configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config):
        super().__init__(client, config, [{"case": "auth"}])
    
    def signer(self, name: str) -> 'Signer':
        return Signer(self.client, self.config, self.op_path + [{"case": "select", "name": name}])
    
    async def list(self) -> List[str]:
        """List all signers."""
        result = await self._do_request({"case": "list", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []
    
    async def set(self, value: AuthConfig) -> None:
        """Set auth configuration."""
        for name, config in value.signers.items():
            await self.signer(name).set(config)


class Signer(BaseOperation):
    """Signer configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def username(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "username"}])
    
    @property
    def password(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "password"}])
    
    @property
    def key(self) -> 'SSHKey':
        return SSHKey(self.client, self.config, self.op_path + [{"case": "key"}])
    
    async def delete(self) -> None:
        """Delete signer."""
        await self._do_request({"case": "delete", "value": True})
    
    async def set(self, value: SignerConfig) -> None:
        """Set signer configuration."""
        if value.username:
            await self.username.set(value.username)
        if value.key and value.password:
            raise ValueError("Cannot set both key and password for a signer.")
        if value.password:
            await self.password.set(value.password)
        if value.key:
            await self.key.path.set(value.key)


class SSHKey(BaseOperation):
    """SSH key configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def path(self) -> StringOperation:
        return StringOperation(self.client, self.config, self.op_path + [{"case": "path"}])
    
    @property
    def data(self) -> BytesOperation:
        return BytesOperation(self.client, self.config, self.op_path + [{"case": "data"}])



class Shapes(BaseOperation):
    """Shapes configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config):
        super().__init__(client, config, [{"case": "shapes"}])
    
    def get(self, name: str) -> 'Shape':
        return Shape(self.client, self.config, self.op_path + [{"case": "select", "name": name}])
    
    async def list(self) -> List[str]:
        """List all shapes."""
        result = await self._do_request({"case": "list", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []
    
    async def set(self, value: ShapesConfig) -> None:
        """Set shapes configuration."""
        for name, config in value.shapes.items():
            await self.get(name).set(config)


class Shape(BaseOperation):
    """Shape configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    @property
    def services(self) -> StringSliceOperation:
        return StringSliceOperation(self.client, self.config, self.op_path + [{"case": "services"}])
    
    @property
    def ports(self) -> 'Ports':
        return Ports(self.client, self.config, self.op_path + [{"case": "ports"}])
    
    @property
    def plugins(self) -> StringSliceOperation:
        return StringSliceOperation(self.client, self.config, self.op_path + [{"case": "plugins"}])
    
    async def delete(self) -> None:
        """Delete shape."""
        await self._do_request({"case": "delete", "value": True})
    
    async def set(self, value: ShapeConfig) -> None:
        """Set shape configuration."""
        if value.services:
            await self.services.set(value.services)
        if value.ports:
            await self.ports.set(value.ports)
        if value.plugins:
            await self.plugins.set(value.plugins)


class Ports(BaseOperation):
    """Ports configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    def port(self, port_name: str) -> 'Port':
        return Port(self.client, self.config, self.op_path + [{"case": "select", "name": port_name}])
    
    async def list(self) -> List[str]:
        """List all ports."""
        result = await self._do_request({"case": "list", "value": True})
        # Handle protobuf response - adjust based on actual structure
        if result.WhichOneof('return') == 'slice':
            return list(result.slice.value)
        return []
    
    async def set(self, value: PortsConfig) -> None:
        """Set ports configuration."""
        for name, port in value.ports.items():
            await self.port(name).set(port)


class Port(BaseOperation):
    """Port configuration operations."""
    
    def __init__(self, client: ConfigClient, config: config_pb2.Config, path: List[Dict[str, Any]]):
        super().__init__(client, config, path)
    
    async def set(self, value: int) -> None:
        """Set port value."""
        await self._do_request({"case": "set", "value": value})
    
    async def get(self) -> int:
        """Get port value."""
        result = await self._do_request({"case": "get", "value": True})
        # Handle protobuf response - adjust based on actual structure
        return int(getattr(result, 'value', 0))
    
    async def delete(self) -> None:
        """Delete port."""
        await self._do_request({"case": "delete", "value": True}) 