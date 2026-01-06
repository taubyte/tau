#!/bin/bash
# Switch containerd between rootful (systemd) and rootless modes
# Usage: switch_containerd_mode.sh [rootful|rootless]

set -e

MODE="${1:-}"
if [ -z "$MODE" ]; then
    echo "Usage: $0 [rootful|rootless]" >&2
    exit 1
fi

if [ "$MODE" != "rootful" ] && [ "$MODE" != "rootless" ]; then
    echo "Error: Mode must be 'rootful' or 'rootless'" >&2
    exit 1
fi

# Get current user info
CURRENT_USER=$(whoami)
CURRENT_UID=$(id -u)
XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$CURRENT_UID}"
ROOTLESS_SOCKET_DIR="$XDG_RUNTIME_DIR/tau/containerd"
ROOTLESS_SOCKET="$ROOTLESS_SOCKET_DIR/containerd.sock"
ROOTLESS_PID_FILE="$ROOTLESS_SOCKET_DIR/containerd.pid"
ROOTFUL_SOCKET="/run/containerd/containerd.sock"

# Function to stop rootless containerd
stop_rootless_containerd() {
    echo "Stopping rootless containerd..."
    
    # Kill any rootlesskit processes that might be running containerd
    pkill -f "rootlesskit.*containerd" || true
    
    # Also check for PID file and kill if exists
    if [ -f "$ROOTLESS_PID_FILE" ]; then
        ROOTLESS_PID=$(cat "$ROOTLESS_PID_FILE" 2>/dev/null || echo "")
        if [ -n "$ROOTLESS_PID" ] && kill -0 "$ROOTLESS_PID" 2>/dev/null; then
            echo "Killing rootless containerd process $ROOTLESS_PID..."
            kill "$ROOTLESS_PID" || true
            sleep 1
            # Force kill if still running
            kill -9 "$ROOTLESS_PID" 2>/dev/null || true
        fi
        rm -f "$ROOTLESS_PID_FILE"
    fi
    
    # Wait a bit for processes to clean up
    sleep 2
    
    # Verify rootless containerd is stopped
    if pgrep -f "rootlesskit.*containerd" >/dev/null; then
        echo "Warning: Some rootless containerd processes may still be running"
    else
        echo "Rootless containerd stopped"
    fi
}

# Function to start rootful containerd
start_rootful_containerd() {
    echo "Starting rootful containerd..."
    
    # Enable and start systemd service
    sudo systemctl enable containerd
    sudo systemctl start containerd
    
    # Wait for socket to be ready
    echo "Waiting for containerd socket..."
    for i in $(seq 1 30); do
        if [ -S "$ROOTFUL_SOCKET" ]; then
            # Make socket accessible to vagrant user
            sudo chmod 666 "$ROOTFUL_SOCKET" || true
            echo "Rootful containerd is ready"
            return 0
        fi
        sleep 1
    done
    
    echo "Error: Rootful containerd socket not ready after 30 seconds" >&2
    sudo systemctl status containerd --no-pager || true
    return 1
}

# Function to stop rootful containerd
stop_rootful_containerd() {
    echo "Stopping rootful containerd..."
    
    # Stop and disable systemd service
    sudo systemctl stop containerd || true
    sudo systemctl disable containerd || true
    
    # Wait a bit for service to stop
    sleep 2
    
    # Verify it's stopped
    if systemctl is-active --quiet containerd 2>/dev/null; then
        echo "Warning: Containerd service may still be active"
    else
        echo "Rootful containerd stopped"
    fi
}

# Function to verify rootless prerequisites
verify_rootless_prerequisites() {
    echo "Verifying rootless prerequisites..."
    
    # Check for rootlesskit
    if ! command -v rootlesskit >/dev/null 2>&1; then
        echo "Error: rootlesskit not found. Please install it." >&2
        return 1
    fi
    
    # Check for slirp4netns (preferred) or vpnkit
    if ! command -v slirp4netns >/dev/null 2>&1 && ! command -v vpnkit >/dev/null 2>&1; then
        echo "Error: Neither slirp4netns nor vpnkit found. Please install slirp4netns." >&2
        return 1
    fi
    
    # Check subuid/subgid mappings
    if ! grep -q "^${CURRENT_USER}:" /etc/subuid 2>/dev/null; then
        echo "Warning: No subuid mapping found for user $CURRENT_USER" >&2
        echo "Rootless mode may not work correctly without subuid configuration" >&2
    fi
    
    if ! grep -q "^${CURRENT_USER}:" /etc/subgid 2>/dev/null; then
        echo "Warning: No subgid mapping found for user $CURRENT_USER" >&2
        echo "Rootless mode may not work correctly without subgid configuration" >&2
    fi
    
    echo "Rootless prerequisites verified"
    return 0
}

# Main logic
case "$MODE" in
    rootful)
        echo "Switching to rootful mode..."
        stop_rootless_containerd
        start_rootful_containerd
        
        # Verify rootful mode is working
        if [ -S "$ROOTFUL_SOCKET" ]; then
            echo "Successfully switched to rootful mode"
            echo "Socket: $ROOTFUL_SOCKET"
        else
            echo "Error: Failed to switch to rootful mode" >&2
            exit 1
        fi
        ;;
        
    rootless)
        echo "Switching to rootless mode..."
        stop_rootful_containerd
        verify_rootless_prerequisites
        
        # Ensure XDG_RUNTIME_DIR exists
        mkdir -p "$ROOTLESS_SOCKET_DIR"
        
        # Note: We don't start rootless containerd here - the Go code will do it via AutoStart
        # We just ensure the environment is ready
        echo "Rootless mode is ready (containerd will be started by the application)"
        echo "Socket will be at: $ROOTLESS_SOCKET"
        ;;
        
    *)
        echo "Error: Invalid mode: $MODE" >&2
        exit 1
        ;;
esac

