#!/bin/bash

# Default values for variables
path="."
rSrc="default"
dataSrc="default"
target="default"
extraFlag="0" # Default value for extraFlag

# Parse command-line arguments
for param in "$@"; do
    case $param in
    path=*) path="${param#*=}" ;;           # Extract value after "path="
    rSrc=*) rSrc="${param#*=}" ;;           # Extract value after "rSrc="
    dataSrc=*) dataSrc="${param#*=}" ;;     # Extract value after "dataSrc="
    target=*) target="${param#*=}" ;;       # Extract value after "target="
    extraFlag=*) extraFlag="${param#*=}" ;; # Extract value after "extraFlag="
    esac
done

# Check if required files exist
if [ ! -f "$path/$rSrc.r" ]; then
    echo "Error: R script '$path/$rSrc.r' not found!"
    exit 1
fi

if [ ! -f "$path/$dataSrc.txt" ]; then
    echo "Error: Data file '$path/$dataSrc.txt' not found!"
    exit 1
fi

# Run Rscript with or without extraFlag
if [ "$extraFlag" == "0" ]; then
    Rscript "$path/$rSrc.r" "$path/$dataSrc.txt" "$path/$target.pdf"
else
    Rscript "$path/$rSrc.r" "$path/$dataSrc.txt" "$path/$target.pdf" "$extraFlag"
fi

# Check if pdfcrop is installed
if ! command -v pdfcrop &>/dev/null; then
    echo "Warning: 'pdfcrop' is not installed. Skipping PDF cropping."
else
    pdfcrop "$path/$target.pdf" "$path/$target.pdf"
fi
