# PFF Sidecar: Deployment & Chaos Engineering Guide

This guide contains the exact steps to deploy your Probabilistic Fast Finality (PFF) Sidecar code to your 5 live Ubuntu VPS instances and execute the chaos engineering tests for your research paper.

## Phase 2: Deployment & Configuration

### 1. Upload Files & VPS Setup Script
Upload your Go code (`*.go`, `go.mod`) and the `setup_vps.sh` script to all 5 of your Ubuntu VPS instances.

On **every VPS instance**, run the setup script to install dependencies (like Go and Git) and configure the UFW firewall:
```bash
chmod +x setup_vps.sh
./setup_vps.sh
```

### 2. Generate Configuration (On Node 1)
Since we already updated `config.go` with your exact VPS IP addresses, you can generate the cryptographic keys directly on your first VPS. 
SSH into **Node 1 (Coordinator)** and run:
```bash
cd ~/pff-consensus-lab
go run . -mode=keygen
```
This will create a `cluster_config.json` file. 
You must now copy this exact `cluster_config.json` file from Node 1 to the `~/pff-consensus-lab` folder on Nodes 2, 3, 4, and 5.



### 3. Build & Run
On all 5 nodes, compile the binary:
```bash
cd ~/pff-consensus-lab
go build -o pff-sidecar
```

> [!IMPORTANT]
> Always start the coordinator first, then start the validators.

**On Node 1 (Coordinator):**
```bash
./pff-sidecar -mode=coordinator -rounds=500 -suite=A3_Baseline
```

**On Nodes 2, 3, 4, 5 (Validators):**
```bash
./pff-sidecar -mode=validator -node-id=node2
# Use node3, node4, node5 for the other instances
```

---

## Phase 3: Live Chaos Engineering

Run these tests while the Go program is running to observe real-time latency changes.

### Suite A.3: Baseline Performance
Run the nodes normally without any injected faults. The coordinator should log the `FAST_PFF` path at around `~177ms` per round. Let it complete `500` rounds to build a baseline in `results.csv`.

### Suite C.1: Network Jitter (Traffic Control)
We will use Linux `tc` (Traffic Control) to inject latency and packet loss on the validators, simulating a degraded network.

**On Validators (Node 2, 3, 4):**
Run this command to add 100ms delay, 20ms jitter, and 5% packet loss:
```bash
sudo tc qdisc add dev eth0 root netem delay 100ms 20ms loss 5%
```
*Note: Change `eth0` to your actual network interface name (e.g., `ens3` or `eth1`) if necessary. Check with `ip addr`.*

Now, run the coordinator with the jitter suite label:
```bash
./pff-sidecar -mode=coordinator -rounds=300 -suite=C1_Jitter
```
You should see the system dynamically drop back to `FALLBACK_BFT` or barely hit `FAST_PFF` due to the delayed signatures.

**To clear the jitter rules when finished:**
```bash
sudo tc qdisc del dev eth0 root netem
```

### Suite C.3: Network Partition (Split-Brain Simulation)
We will use `iptables` to block the coordinator's port entirely on two nodes, breaking the 90% threshold (since only 3/5 nodes will respond).

**On Validators (Node 4 and Node 5):**
Run this command while consensus is active to simulate a hard partition:
```bash
sudo iptables -A INPUT -p tcp --dport 8080 -j DROP
```
Observe the Coordinator terminal: it will immediately timeout after 300ms and log `PATH_TAKEN: FALLBACK_BFT`.

**The "Heal Speed" Test:**
To test protocol resumption (how fast the network recovers to the fast lane), flush the firewall rules while the coordinator is still running:
```bash
sudo iptables -F
```
The coordinator should instantly start collecting 5/5 signatures again and revert to `FAST_PFF`.

---

## Phase 4: Data Processing & Graphing

Once you have completed the tests, download the `results.csv` file from your **Coordinator (Node 1)** to your local machine.

Make sure you have the required python packages:
```bash
pip install -r requirements.txt
```

Run the analysis script to generate your paper graphs:
```bash
python analyze_results.py
```

This will create a `figures/` directory containing:
1. `Graph_1_Noise_Impact.png` - A violin/boxplot comparing latency.
2. `Graph_2_Protocol_Resumption.png` - A timeline showing the exact moment the firewall was added and flushed.
