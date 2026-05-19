#!/bin/bash
# PFF Sidecar - Ubuntu VPS Setup Script
# Run this on ALL 5 VPS instances

set -e

echo "=========================================="
echo "🚀 Setting up PFF Sidecar Node"
echo "=========================================="

# 1. Update and install dependencies
echo "=> Installing dependencies (Go, Git, UFW)..."
sudo apt-get update -y
sudo apt-get install -y golang git ufw

# 2. Configure Firewall (allow port 8080 and SSH)
echo "=> Configuring UFW Firewall..."
sudo ufw allow ssh
sudo ufw allow 8080/tcp
# Enable firewall if not active (we do this non-interactively)
sudo ufw --force enable
sudo ufw status

# 3. Create working directory
echo "=> Setting up working directory..."
mkdir -p ~/pff-consensus-lab
cd ~/pff-consensus-lab

echo "=========================================="
echo "✅ Setup Complete!"
echo "Next steps:"
echo "1. Upload your Go source files and cluster_config.json to ~/pff-consensus-lab"
echo "2. Build the binary: go build -o pff-sidecar"
echo "3. Run coordinator: ./pff-sidecar -mode=coordinator -rounds=1000 -suite=A3_Baseline"
echo "   OR"
echo "   Run validator:  ./pff-sidecar -mode=validator -node-id=node2"
echo "=========================================="
