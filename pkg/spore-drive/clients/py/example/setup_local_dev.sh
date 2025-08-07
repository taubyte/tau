#!/bin/bash

# Setup script for running the spore-drive example as if in a separate repo
# This script creates a virtual environment and installs spore-drive from local path

set -e  # Exit on any error

echo "Setting up spore-drive example environment..."

# Check if Python 3 is available
if ! command -v python3 &> /dev/null; then
    echo "Error: Python 3 is not installed or not in PATH"
    exit 1
fi

# Create virtual environment if it doesn't exist
if [ ! -d "venv" ]; then
    echo "Creating virtual environment..."
    python3 -m venv venv
else
    echo "Virtual environment already exists"
fi

# Activate virtual environment
echo "Activating virtual environment..."
source venv/bin/activate

# Upgrade pip
echo "Upgrading pip..."
pip install --upgrade pip

# Install spore-drive in editable mode from local path
echo "Installing spore-drive from local path..."
pip install -e ../

# Install other dependencies from requirements.txt
echo "Installing other dependencies..."
pip install -r requirements.txt

echo ""
echo "Setup complete! To activate the environment, run:"
echo "source venv/bin/activate"
echo ""
echo "To run the examples:"
echo "python demo.py"
echo "python displace.py"
echo "python namecheap_client.py" 