#!/usr/bin/env python3
"""
Demo launcher script.

This script allows users to choose between the original text-based demo
and the new interactive Textual-based demo.
"""

import sys
import subprocess
from pathlib import Path


def check_textual_available():
    """Check if Textual is available."""
    try:
        import textual
        return True
    except ImportError:
        return False


def install_textual():
    """Install Textual dependencies."""
    print("Installing Textual dependencies...")
    try:
        subprocess.check_call([
            sys.executable, "-m", "pip", "install", 
            "textual>=0.40.0", "textual-dev>=0.3.0", "rich>=13.0.0"
        ])
        print("✅ Textual installed successfully!")
        return True
    except subprocess.CalledProcessError:
        print("❌ Failed to install Textual. Please install manually:")
        print("   pip install textual textual-dev rich")
        return False


def show_menu():
    """Show the demo selection menu."""
    print("🚀 IDP Example Demo Launcher")
    print("=" * 40)
    print()
    print("Choose your demo experience:")
    print()
    print("1. 📝 Original Text-based Demo")
    print("   - Simple text output")
    print("   - Sequential execution")
    print("   - No additional dependencies")
    print()
    print("2. 🎨 Interactive Textual Demo")
    print("   - Beautiful terminal UI")
    print("   - Interactive navigation")
    print("   - Syntax highlighting")
    print("   - Real-time progress")
    print("   - Requires Textual library")
    print()
    print("3. 📦 Install Textual Dependencies")
    print("   - Install required packages")
    print()
    print("4. ❌ Exit")
    print()
    
    while True:
        try:
            choice = input("Enter your choice (1-4): ").strip()
            
            if choice == "1":
                return "text"
            elif choice == "2":
                if check_textual_available():
                    return "textual"
                else:
                    print("\n❌ Textual is not installed.")
                    install_choice = input("Would you like to install it now? (y/n): ").strip().lower()
                    if install_choice in ['y', 'yes']:
                        if install_textual():
                            return "textual"
                        else:
                            print("Please install Textual manually and try again.")
                            return None
                    else:
                        print("Please install Textual manually: pip install textual textual-dev rich")
                        return None
            elif choice == "3":
                install_textual()
                return None
            elif choice == "4":
                return "exit"
            else:
                print("Invalid choice. Please enter 1, 2, 3, or 4.")
        except KeyboardInterrupt:
            print("\n\nGoodbye!")
            return "exit"


def run_text_demo():
    """Run the original text-based demo."""
    print("Running original text-based demo...")
    print("=" * 50)
    
    try:
        # Import and run the original demo
        from demo import main
        main()
    except ImportError as e:
        print(f"❌ Error importing demo: {e}")
        print("Make sure you're in the correct directory and all files are present.")
    except Exception as e:
        print(f"❌ Error running demo: {e}")


def run_textual_demo():
    """Run the Textual-based demo."""
    print("Running interactive Textual demo...")
    print("=" * 50)
    
    try:
        # Import and run the Textual demo
        from demo_textual import main
        main()
    except ImportError as e:
        print(f"❌ Error importing Textual demo: {e}")
        print("Make sure Textual is installed and all files are present.")
    except Exception as e:
        print(f"❌ Error running Textual demo: {e}")


def main():
    """Main launcher function."""
    print("Welcome to the IDP Example Demo!")
    print()
    
    # Check if we're in the right directory
    demo_file = Path("demo.py")
    if not demo_file.exists():
        print("❌ Error: demo.py not found in current directory.")
        print("Please run this script from the example directory.")
        sys.exit(1)
    
    # Show menu and get user choice
    choice = show_menu()
    
    if choice == "text":
        run_text_demo()
    elif choice == "textual":
        run_textual_demo()
    elif choice == "exit":
        print("Goodbye!")
    else:
        print("No demo selected.")


if __name__ == "__main__":
    main() 