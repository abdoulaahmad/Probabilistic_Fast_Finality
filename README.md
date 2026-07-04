<div align="center">

# ⚡ Probabilistic Fast Finality (PFF)

**A Go implementation of a finality gadget that reduces BFT consensus latency by up to ~46% in the common case**
by adding an optimistic fast path on top of standard BFT protocols.

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat)](LICENSE)
[![Status](https://img.shields.io/badge/Status-Active%20Research-orange?style=flat)]()
[![Protocol](https://img.shields.io/badge/Protocol-Ed25519%20%7C%20BFT-blueviolet?style=flat)]()

> 📄 Companion to: [Probabilistic Fast Finality: Reducing BFT Latency by Up to 46% in the Common Case](https://medium.com/@abdullahiabbaahmad39/probabilistic-fast-finality-reducing-bft-latency-by-up-to-46-in-the-common-case-04895ded843a)

</div>

---

## 🔑 The Core Idea

Standard BFT protocols (e.g., HotStuff) require **three consecutive voting rounds** per block, even when the network is perfectly healthy. PFF adds a **fast path** that short-circuits this when strong evidence of agreement is observed:

```
  Coordinator broadcasts signed block hash
              │
  ┌───────────▼────────────┐
  │  Collect validator     │
  │  signatures (300 ms)   │
  └───────────┬────────────┘
              │
  ┌───────────▼────────────┐
  │  ≥ 90% signatures?     │
  └────┬──────────────┬────┘
       │ YES          │ NO
  ┌────▼────┐   ┌─────▼──────┐
  │FAST_PFF │   │FALLBACK_BFT│
  │ ~177 ms │   │  ~331 ms   │
  └─────────┘   └────────────┘
```

**No safety is sacrificed.** Waiting for the tail latency of one 90% quorum round is provably faster than three consecutive 67% quorum round-trips in healthy network conditions.

---

## 📊 Results

> Measured across **500 consensus rounds** on **5 live VPS nodes** over real WAN links.

### Headline: PFF vs Standard BFT

![Latency Comparison: PFF Fast Path vs Standard HotStuff across validator counts](docs/pff_vs_hotstuff_latency_comparison.png)

| Path | Mean Latency | Condition |
|---|---|---|
| `FAST_PFF` | **~177 ms** | Healthy network (≥ 90% quorum in 300 ms) |
| `FALLBACK_BFT` | **~331 ms** | Degraded network / timeout expired |
| **Improvement** | **~46% faster** | Common case |

<details>
<summary>📈 Detailed Test Results — Latency Proof & Threshold Sweet Spot</summary>

<br>

![Test A.1 Latency Reduction Proof and Test B.2 Threshold vs Latency Sweet Spot](docs/latency_proof_threshold_sweetspot.png)

**Left — Test A.1: Latency Reduction Proof**
PFF fast path holds ~177 ms across all validator counts (10–500), while standard HotStuff converges to ~331 ms. The gap is consistent and validator-count-independent in the healthy-network case.

**Right — Test B.2: Threshold vs Latency (Sweet Spot)**
As the confidence threshold `k` increases from 0.70 → 0.95, latency rises gradually (120 ms → 210 ms). Beyond 0.95 the curve steepens sharply — this empirically justifies **k = 0.90** as the optimal operating point.

</details>

<details>
<summary>📈 Detailed Test Results — Throughput Scaling & Safety Heatmap</summary>

<br>

![Test A.2 Throughput Scaling and Test B.1 Safety Threshold Heatmap](docs/throughput_scaling_safety_heatmap.png)

**Left — Test A.2: Throughput Scaling (Max Capacity)**
Latency remains flat (182–186 ms) from 100 TPS up to ~50,000 TPS, demonstrating PFF scales well under load. The spike at 100k TPS reflects coordinator saturation, not a protocol flaw.

**Right — Test B.1: Safety Threshold Heatmap**
2D safety margin across adversarial power `f` (0.10–0.35) and PFF threshold `k` (0.70–0.95). The red dot marks the chosen design point (k=0.90, f≤0.20), sitting comfortably in the high-safety (green) zone.

</details>

---

## 🏗️ Architecture

<details>
<summary>View file structure & component roles</summary>

<br>

```
PFF-Lab/
├── main.go             # Entry point — -mode flag routing (coordinator | validator | keygen)
├── coordinator.go      # Node 1: broadcasts proposals, collects votes, decides fast vs fallback path
├── validator.go        # Nodes 2–5: verifies coordinator signature, signs block hash, sends vote
├── network.go          # TCP wire protocol — length-prefixed JSON framing
├── config.go           # Ed25519 keygen, protocol constants, cluster config loading
├── analyze_results.py  # Post-experiment graph generation (Python/matplotlib)
├── requirements.txt    # Python deps: pandas, matplotlib, seaborn
└── docs/               # Result graphs and documentation assets
```

**Wire protocol** (`network.go`): all messages are JSON with a 4-byte big-endian length prefix.

| Message Type | Direction | Purpose |
|---|---|---|
| `CONNECT` | Validator → Coordinator | Validator registration |
| `PROPOSE` | Coordinator → Validators | Signed block hash broadcast |
| `VOTE` | Validators → Coordinator | Ed25519-signed vote |

No external Go dependencies — pure stdlib.

</details>

---

## ⚙️ Protocol Constants

<details>
<summary>View configurable protocol parameters</summary>

<br>

Defined in `config.go`, overridable via `cluster_config.json`:

| Constant | Default | Meaning |
|---|---|---|
| `PFFThresholdDefault` | `0.90` | Fraction of validators required for fast path |
| `BFTThresholdDefault` | `0.67` | Standard BFT quorum threshold |
| `FastTimeoutMS` | `300 ms` | Deadline for fast-path vote collection |
| `BFTDelayMS` | `150 ms` | Simulated BFT multi-round processing delay |

</details>

---

## 🚀 Quick Start

### Prerequisites

- **Go 1.21+**
- **5 VPS nodes** with Ubuntu 22.04 (or any Linux), TCP port `8080` open
- **Python 3.8+** with `pip install -r requirements.txt` *(for analysis only)*

### 1. Generate keypairs — run once on Node 1

```bash
go run . -mode=keygen
```

Writes `cluster_config.json` with Ed25519 keypairs for all 5 nodes.
Edit the hardcoded IPs in `config.go` → `RunKeygen()` to match your VPS public IPs first.

> ⚠️ Distribute `cluster_config.json` to all nodes — **never commit it, it contains private keys.**

### 2. Build the binary

```bash
go build -o pff-sidecar .
```

Copy the binary and `cluster_config.json` to all 5 nodes.

### 3. Start validators — Nodes 2–5

```bash
./pff-sidecar -mode=validator -node-id=node2   # on node 2
./pff-sidecar -mode=validator -node-id=node3   # on node 3
./pff-sidecar -mode=validator -node-id=node4   # on node 4
./pff-sidecar -mode=validator -node-id=node5   # on node 5
```

Validators retry the coordinator connection up to **40 times** (3 s between attempts) — start them before the coordinator if needed.

### 4. Start the coordinator — Node 1

```bash
./pff-sidecar -mode=coordinator -rounds=500 -suite=A3_Baseline
```

The coordinator waits for all 4 validators to register, then runs the specified number of consensus rounds, streaming results to `results.csv`.

---

## 🎛️ CLI Reference

<details>
<summary>View all flags</summary>

<br>

| Flag | Default | Description |
|---|---|---|
| `-mode` | — | `keygen` \| `coordinator` \| `validator` (required) |
| `-node-id` | — | Node ID for validator mode, e.g. `node2` (required in validator mode) |
| `-rounds` | `100` | Number of consensus rounds to run (coordinator only) |
| `-suite` | `A3_Baseline` | Test suite label written to `results.csv` |
| `-config` | `cluster_config.json` | Path to cluster config file |

</details>

---

## 🔥 Chaos Engineering Suites

<details>
<summary>View test suite configurations</summary>

<br>

Three suites reproduce experimental conditions for evaluating PFF under adversarial network scenarios. See `CHAOS_AND_DEPLOYMENT.md` for exact commands.

| Suite | Label | What It Tests |
|---|---|---|
| A.3 | `A3_Baseline` | Healthy network — no interference |
| C.1 | `C1_Jitter` | Network jitter via `tc netem` (100 ms ± 30 ms) |
| C.3 | `C3_Partition` | Partition simulation via `iptables`, then heal |

</details>

---

## 📈 Analyzing Results

After downloading `results.csv` from the coordinator node:

```bash
pip install -r requirements.txt
python analyze_results.py
```

Outputs to `figures/`:
- `Graph_1_Noise_Impact.png` — boxplot comparing `A3_Baseline` vs `C1_Jitter`
- `Graph_2_Protocol_Resumption.png` — round-by-round latency timeline for `C3_Partition`

<details>
<summary>View results.csv schema</summary>

<br>

| Column | Type | Description |
|---|---|---|
| `RoundID` | int | Consensus round number |
| `TestSuite` | string | Suite label (e.g. `A3_Baseline`) |
| `LatencyMS` | float | Time-to-finality in milliseconds |
| `SignaturesReceived` | int | Number of valid validator signatures collected |
| `PathTaken` | string | `FAST_PFF` or `FALLBACK_BFT` |

</details>

---

## 🔒 Security Notes

- `cluster_config.json` contains **Ed25519 private keys** for all nodes — never commit it (already in `.gitignore`)
- The coordinator **verifies every validator signature** before counting a vote
- Each validator **verifies the coordinator's signature** before signing a proposal
- Message length is **bounded to 1 MB** to prevent memory exhaustion attacks

---

## 🔬 Research Status

> This project spans both a **working prototype** and an **ongoing research agenda**. The Go implementation and latency results are real and reproducible. The following areas remain under active investigation:

| Area | Status |
|---|---|
| Evidence threshold formula (`t_evidence`) | 🔄 Under investigation |
| Randomness mechanism (aggregated sigs vs threshold VRF) | 🔄 Under investigation |
| Probability function (`p_func`) mapping | 🔄 Under investigation |
| Formal safety & liveness proofs | 🔄 In progress |
| Integration with AlephBFT / DAG-based protocols | 🔄 Planned |
| Simulation vs real-network validation | ✅ Real WAN (5 VPS nodes, 500 rounds) |

Performance numbers (~177 ms / ~331 ms) reflect measurements on the current Go implementation and are reproducible. Theoretical bounds and formal claims are subject to revision as the research matures.

---

## 🤝 Contributing

This is an active research project. If you have ideas, questions, or find issues:

1. Open an [Issue](https://github.com/abdoulaahmad/PFF-Lab/issues) to discuss
2. Fork → branch → PR for code contributions
3. For research discussion, reach out via the Medium article comments

---

<div align="center">

Made with ⚡ by [@abdoulaahmad](https://github.com/abdoulaahmad)

</div>

---

## How It Works

Standard BFT protocols (e.g., HotStuff) require three consecutive voting rounds per block even when the network is healthy. PFF adds a fast path that short-circuits this when network conditions allow:

```
         ┌─────────────────────────────────────────────────────┐
         │  Coordinator broadcasts signed block hash           │
         └───────────────────────┬─────────────────────────────┘
                                 │
              ┌──────────────────▼──────────────────┐
              │  Collect validator signatures        │
              │  within 300 ms deadline              │
              └──────────────────┬──────────────────┘
                                 │
              ┌──────────────────▼──────────────────┐
              │  ≥ 90% signatures arrived?           │
              └──────┬──────────────────────┬────────┘
                     │ YES                  │ NO
             ┌───────▼───────┐    ┌─────────▼──────────┐
             │  FAST_PFF     │    │  FALLBACK_BFT       │
             │  ~177 ms      │    │  ~331 ms            │
             └───────────────┘    └────────────────────┘
```

**Fast path** (`FAST_PFF`): triggered when ≥ 90% of validators return Ed25519-signed votes within 300 ms. No safety is sacrificed — waiting for the tail latency of one 90% quorum round is faster than three consecutive 67% quorum round-trips.

**Fallback** (`FALLBACK_BFT`): transparently reverts to full BFT processing when participation drops below the threshold or the timeout expires.

**Results across 500 consensus rounds on 5 live VPS nodes:**

| Path | Mean Latency | Condition |
|---|---|---|
| FAST_PFF | ~177 ms | Healthy network (≥ 90% quorum in 300 ms) |
| FALLBACK_BFT | ~331 ms | Degraded network / timeout |
| **Improvement** | **~46% faster** | Common case |

---

## Architecture

```
pff-sidecar/
├── main.go          # Entry point, -mode flag routing
├── coordinator.go   # Node 1: broadcasts proposals, collects votes, decides path
├── validator.go     # Nodes 2–5: verify signature, sign block hash, send vote
├── network.go       # TCP wire protocol (length-prefixed JSON framing)
├── config.go        # Keygen, protocol constants, cluster config loading
├── analyze_results.py  # Post-test graph generation (Python)
└── requirements.txt    # Python deps: pandas, matplotlib, seaborn
```

The binary runs in one of three modes controlled by the `-mode` flag. No external Go dependencies — pure stdlib.

**Wire protocol** (`network.go`): all messages are JSON with a 4-byte big-endian length prefix. Three message types: `CONNECT` (validator registration), `PROPOSE` (coordinator → validators), `VOTE` (validators → coordinator).

---

## Prerequisites

- Go 1.21+
- Python 3.8+ with `pip install -r requirements.txt` (analysis only)
- 5 VPS nodes with Ubuntu 22.04 (or any Linux), TCP port 8080 open

---

## Quick Start

### 1. Generate keypairs (run once on Node 1)

```bash
go run . -mode=keygen
```

This writes `cluster_config.json` with Ed25519 keypairs for all 5 nodes. Edit the hardcoded IPs in `config.go` (`RunKeygen`) to match your VPS public IPs before running.

Distribute `cluster_config.json` to all nodes — **keep it secret, it contains private keys**.

### 2. Build the binary

```bash
go build -o pff-sidecar .
```

Copy the binary and `cluster_config.json` to all 5 nodes.

### 3. Start validators (Nodes 2–5)

```bash
# On node2
./pff-sidecar -mode=validator -node-id=node2

# On node3
./pff-sidecar -mode=validator -node-id=node3

# On node4
./pff-sidecar -mode=validator -node-id=node4

# On node5
./pff-sidecar -mode=validator -node-id=node5
```

Validators retry the coordinator connection up to 40 times (3 s between attempts), so they can be started before the coordinator.

### 4. Start the coordinator (Node 1)

```bash
./pff-sidecar -mode=coordinator -rounds=500 -suite=A3_Baseline
```

The coordinator waits for all 4 validators to register, then runs the specified number of consensus rounds. Results stream to `results.csv`.

---

## CLI Flags

| Flag | Default | Description |
|---|---|---|
| `-mode` | — | `keygen` \| `coordinator` \| `validator` (required) |
| `-node-id` | — | Node ID for validator mode, e.g. `node2` (required in validator mode) |
| `-rounds` | `100` | Number of consensus rounds to run (coordinator only) |
| `-suite` | `A3_Baseline` | Test suite label written to `results.csv` |
| `-config` | `cluster_config.json` | Path to cluster config file |

---

## Protocol Constants

Defined in `config.go`, overridable via `cluster_config.json`:

| Constant | Value | Meaning |
|---|---|---|
| `PFFThresholdDefault` | 0.90 | Fraction of validators required for fast path |
| `BFTThresholdDefault` | 0.67 | Standard BFT quorum threshold |
| `FastTimeoutMS` | 300 ms | Deadline for fast-path vote collection |
| `BFTDelayMS` | 150 ms | Simulated BFT multi-round processing delay |

---

## Chaos Engineering Suites

Three test suites reproduce the paper's experimental conditions. See `CHAOS_AND_DEPLOYMENT.md` for exact commands.

| Suite | Label | What it tests |
|---|---|---|
| A.3 | `A3_Baseline` | Healthy network, no interference |
| C.1 | `C1_Jitter` | Network jitter via `tc netem` (100 ms ± 30 ms) |
| C.3 | `C3_Partition` | Partition simulation via `iptables`, then heal |

---

## Analyzing Results

After downloading `results.csv` from the coordinator:

```bash
pip install -r requirements.txt
python analyze_results.py
```

Outputs to `figures/`:
- `Graph_1_Noise_Impact.png` — boxplot comparing A3_Baseline vs C1_Jitter
- `Graph_2_Protocol_Resumption.png` — round-by-round latency timeline for C3_Partition

---

## `results.csv` Schema

| Column | Type | Description |
|---|---|---|
| `RoundID` | int | Consensus round number |
| `TestSuite` | string | Suite label (e.g. `A3_Baseline`) |
| `LatencyMS` | float | Time-to-finality in milliseconds |
| `SignaturesReceived` | int | Number of valid validator signatures collected |
| `PathTaken` | string | `FAST_PFF` or `FALLBACK_BFT` |

---

## Security Notes

- `cluster_config.json` contains Ed25519 private keys for all nodes. Never commit it.
- The coordinator verifies every validator signature before counting a vote.
- Each validator verifies the coordinator's signature before signing a proposal.
- Message length is bounded to 1 MB to prevent memory exhaustion.
