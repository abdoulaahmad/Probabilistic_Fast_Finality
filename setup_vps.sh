#!/bin/bash
# PFF Sidecar — Ubuntu VPS Setup Script
# Run this on ALL nodes.

set -euo pipefail

PORT="${PFF_PORT:-8080}"
WORKDIR="${PFF_WORKDIR:-$HOME/pff-consensus-lab}"

echo "=========================================="
echo " Setting up PFF Sidecar Node"
echo "=========================================="

echo "=> Installing dependencies (Go, Git, UFW)..."
sudo apt-get update -y
sudo apt-get install -y golang git ufw

echo "=> Configuring UFW firewall..."
sudo ufw allow ssh
sudo ufw allow "${PORT}/tcp"
sudo ufw --force enable
sudo ufw status

echo "=> Setting up working directory at ${WORKDIR}..."
mkdir -p "${WORKDIR}/keys"
chmod 700 "${WORKDIR}/keys"
cd "${WORKDIR}"

cat <<EOF

==========================================
Setup complete.

Next steps:
  1. Upload the Go sources and cluster_config.json to ${WORKDIR}

  2. Upload ONLY this node's private key to ${WORKDIR}/keys/
     e.g. for node2:  scp keys/node2.key <this-host>:${WORKDIR}/keys/

     Do NOT copy the whole keys/ directory to every node. A node that
     holds another node's private key can forge that node's votes, which
     defeats the point of verifying signatures at all.

  3. Build:  go build -o pff-sidecar .

  4. Run the coordinator:
       ./pff-sidecar -mode=coordinator -rounds=500 -suite=A3_Baseline
     ...or a validator:
       ./pff-sidecar -mode=validator -node-id=node2
==========================================
EOF
