#!/usr/bin/env python3
"""
Progress Display Module

This module provides functionality for displaying deployment progress
with progress bars and error handling using Rich.
"""

import re
from typing import Dict, List, Optional
from spore_drive import Course

try:
    from rich.console import Console
    from rich.progress import Progress, SpinnerColumn, TextColumn, BarColumn, TaskProgressColumn, TimeRemainingColumn, TimeElapsedColumn
    from rich.live import Live
    from rich.table import Table
    from rich.panel import Panel
    RICH_AVAILABLE = True
except ImportError:
    RICH_AVAILABLE = False


def extract_host(path: str) -> str:
    """Extract host from path."""
    match = re.search(r'/([^/]+):\d+', path)
    return match.group(1) if match else "unknown-host"


def extract_task(path: str) -> str:
    """Extract task from path."""
    parts = path.split("/")
    return parts[-1] if parts else "unknown-task"


class ProgressDisplay:
    """Progress display handler with support for multiple hosts using Rich."""
    
    def __init__(self, use_bars: bool = True):
        """
        Initialize progress display.
        
        Args:
            use_bars: Whether to use progress bars (if Rich is available)
        """
        self.use_bars = use_bars and RICH_AVAILABLE
        self.task_bars: Dict[str, any] = {}
        self.errors: List[Dict[str, str]] = []
        self.console = Console() if RICH_AVAILABLE else None
    
    async def display_progress(self, course: Course):
        """Display progress with host and task information."""
        if self.console:
            self.console.print("🚀 [bold blue]Monitoring deployment progress...[/bold blue]")
        else:
            print("Monitoring deployment progress...")
        
        if self.use_bars:
            await self._display_with_bars(course)
        else:
            await self._display_simple(course)
    
    async def _display_with_bars(self, course: Course):
        """Display progress using Rich progress bars."""
        with Progress(
            SpinnerColumn(),
            TextColumn("[progress.description]{task.description}"),
            BarColumn(),
            TextColumn("[progress.percentage]{task.percentage:>3.0f}%"),
            console=self.console,
            expand=False,
            transient=False,
            refresh_per_second=4
        ) as progress:
            # Initialize progress bars
            async for displacement in course.progress():
                host = extract_host(displacement.path)
                task = extract_task(displacement.path)
                
                if host not in self.task_bars:
                    task_id = progress.add_task(
                        f"[cyan]{host}[/cyan] - [yellow]{task}[/yellow]",
                        total=100,
                        start=True
                    )
                    self.task_bars[host] = {
                        'task_id': task_id,
                        'current_task': task,
                        'status': 'running',
                        'last_progress': 0
                    }
                else:
                    # Update task description if it changed
                    task_info = self.task_bars[host]
                    if task_info['current_task'] != task:
                        progress.update(
                            task_info['task_id'],
                            description=f"[cyan]{host}[/cyan] - [yellow]{task}[/yellow]"
                        )
                        task_info['current_task'] = task
                
                # Update progress bar with percentage
                task_info = self.task_bars[host]
                current_progress = displacement.progress
                
                # Only update if progress actually changed (helps with time estimates)
                if current_progress != task_info['last_progress']:
                    progress.update(
                        task_info['task_id'],
                        completed=current_progress
                    )
                    task_info['last_progress'] = current_progress
                
                if displacement.error:
                    self.errors.append({
                        'host': host,
                        'task': task,
                        'error': displacement.error
                    })
                    task_info['status'] = 'failed'
                    progress.update(
                        task_info['task_id'],
                        description=f"[cyan]{host}[/cyan] - [red]{task} - FAILED[/red]"
                    )
        
        self._display_summary()
    
    async def _display_simple(self, course: Course):
        """Display progress with simple text output (fallback when Rich is not available)."""
        if not RICH_AVAILABLE:
            print("Note: Install 'rich' for better progress visualization: pip install rich")
        
        async for displacement in course.progress():
            host = extract_host(displacement.path)
            task = extract_task(displacement.path)
            
            if host not in self.task_bars:
                self.task_bars[host] = {
                    'progress': 0,
                    'task': task,
                    'status': 'running'
                }
            
            self.task_bars[host]['progress'] = displacement.progress
            self.task_bars[host]['task'] = task
            
            # Print progress update with better formatting
            progress_bar = "█" * (displacement.progress // 5) + "░" * (20 - displacement.progress // 5)
            print(f"{host}: {task} - [{progress_bar}] {displacement.progress:>3}%")
            
            if displacement.error:
                self.errors.append({
                    'host': host,
                    'task': task,
                    'error': displacement.error
                })
                self.task_bars[host]['status'] = 'failed'
        
        self._display_summary()
    
    def _display_summary(self):
        """Display final deployment summary."""
        if self.console:
            # Create a summary table
            table = Table(title="🚀 Deployment Summary")
            table.add_column("Host", style="cyan", no_wrap=True)
            table.add_column("Status", style="bold")
            table.add_column("Task", style="yellow")
            
            for host, info in self.task_bars.items():
                error_for_host = next((err for err in self.errors if err['host'] == host), None)
                if error_for_host:
                    table.add_row(host, "❌ FAILED", error_for_host['task'])
                else:
                    table.add_row(host, "✅ SUCCESS", "")
            
            self.console.print(table)
            
            if self.errors:
                error_table = Table(title="❌ Errors Encountered")
                error_table.add_column("Host", style="red")
                error_table.add_column("Task", style="yellow")
                error_table.add_column("Error", style="red")
                
                for err in self.errors:
                    error_table.add_row(err['host'], err['task'], err['error'])
                
                self.console.print(error_table)
                raise Exception("Displacement failed")
        else:
            # Fallback to simple text output
            print("\nDeployment Summary:")
            print("-" * 50)
            
            for host, info in self.task_bars.items():
                error_for_host = next((err for err in self.errors if err['host'] == host), None)
                if error_for_host:
                    print(f"❌ {host}: {error_for_host['task']} - FAILED")
                else:
                    print(f"✅ {host}: {info.get('current_task', info.get('task', 'SUCCESS'))} - SUCCESS")
            
            if self.errors:
                print("\nErrors encountered:")
                for err in self.errors:
                    print(f"Host: {err['host']}, Task: {err['task']}, Error: {err['error']}")
                raise Exception("Displacement failed")


# Convenience functions for backward compatibility
async def display_progress(course: Course, use_bars: bool = True):
    """
    Display progress with host and task information.
    
    Args:
        course: The course to monitor
        use_bars: Whether to use progress bars (if Rich is available)
    """
    progress_display = ProgressDisplay(use_bars=use_bars)
    await progress_display.display_progress(course)


async def display_progress_with_bars(course: Course):
    """Display progress using Rich progress bars."""
    await display_progress(course, use_bars=True)


async def display_progress_simple(course: Course):
    """Display progress with simple text output."""
    await display_progress(course, use_bars=False)


def is_rich_available() -> bool:
    """Check if Rich is available for progress bars."""
    return RICH_AVAILABLE


def get_progress_bar_info() -> Dict[str, any]:
    """Get information about progress bar capabilities."""
    return {
        'rich_available': RICH_AVAILABLE,
        'recommended_install': 'pip install rich' if not RICH_AVAILABLE else None
    } 