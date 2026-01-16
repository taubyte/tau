#!/bin/bash

# Define the directory to search in
SEARCH_DIR="."

# Find all yaml or yml files and loop over them
find "$SEARCH_DIR" -type f \( -name "*.yaml" -o -name "*.yml" \) | while read -r file; do
    echo "$file:"
    cat "$file"
    echo "--"
    echo
done
