#!/usr/bin/env python3
"""
Script to fix import issues in generated Python protobuf files.
"""

import os
import re
import glob


def fix_imports_in_file(file_path):
    """Fix import statements in a single file."""
    print(f"Fixing imports in: {file_path}")
    
    with open(file_path, 'r', encoding='utf-8') as f:
        content = f.read()
    
    # Fix the problematic import pattern
    # from config.v1 import config_pb2 as config_dot_v1_dot_config__pb2
    # becomes
    # from ...config.v1 import config_pb2 as config_dot_v1_dot_config__pb2
    
    # Pattern to match imports that need fixing
    pattern = r'from ([a-zA-Z_][a-zA-Z0-9_]*)\.([a-zA-Z_][a-zA-Z0-9_]*) import ([a-zA-Z_][a-zA-Z0-9_]*) as ([a-zA-Z_][a-zA-Z0-9_]*)'
    
    def replace_import(match):
        module = match.group(1)
        submodule = match.group(2)
        imported = match.group(3)
        alias = match.group(4)
        
        # Only fix imports that are not from google.protobuf or other external modules
        if module in ['google', 'grpc']:
            return match.group(0)  # Keep original
        
        # Fix the import to be relative
        return f'from ...{module}.{submodule} import {imported} as {alias}'
    
    new_content = re.sub(pattern, replace_import, content)
    
    # Write back if content changed
    if new_content != content:
        with open(file_path, 'w', encoding='utf-8') as f:
            f.write(new_content)
        print(f"  ✅ Fixed imports in {file_path}")
    else:
        print(f"  ℹ️  No changes needed in {file_path}")


def fix_all_python_imports():
    """Fix imports in all generated Python protobuf files."""
    py_dir = "../clients/py/spore_drive/proto"
    
    if not os.path.exists(py_dir):
        print(f"Directory {py_dir} does not exist. Skipping import fixes.")
        return
    
    # Find all Python files in the proto directory
    python_files = glob.glob(f"{py_dir}/**/*.py", recursive=True)
    
    if not python_files:
        print("No Python files found to fix.")
        return
    
    print(f"Found {len(python_files)} Python files to check for import fixes...")
    
    for file_path in python_files:
        fix_imports_in_file(file_path)
    
    print("✅ Import fixing completed!")


if __name__ == "__main__":
    fix_all_python_imports() 