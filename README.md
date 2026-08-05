<div align="center">

# ⚡ Probabilistic Fast Finality (PFF)

**A Go prototype exploring whether BFT consensus latency can be reduced by adding an
optimistic fast path — currently an authenticated vote-collection benchmark on real WAN links.**

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white)](https://golang.org)
[![Status](https://img.shields.io/badge/Status-Early%20Prototype-yellow?style=flat)]()
[![Protocol](https://img.shields.io/badge/Protocol-Ed25519%20%7C%20TCP-blueviolet?style=flat)]()

> Companion code to: [Probabilistic Fast Finality: Reducing BFT Latency by Up to 46% in the Common Case](https://medium.com/@abdullahiabbaahmad39/probabilistic-fast-finality-reducing-bft-latency-by-up-to-46-in-the-common-case-04895ded843a)
>
> The article describes the **proposed design**. This repository implements **one stage of it**.
> The section below states exactly which.

</div>

---

## ⚠️ Current Status — Read This First

This repository is an **early-stage prototype**, not a finality gadget. Being precise about
that is the point of this section, because the naming invites a stronger reading than the
code supports.

**What the code actually does:**

> One coordinator broadcasts a freshly random 32-byte hash to N validators, each validator
> verifies the coordinator's signature and returns an Ed25519 signature over it, and the
> coordinator measures how long until a configured fraction of signatures arrives.

That is the **vote-collection stage** of the proposed protocol — the dominant cost of a single
BFT round-trip. Measuring it over real WAN links is a real result. It is not, on its own,
finality.

**Why it isn't finality yet.** Finality means *"this block will not be reverted."* For a block
to be final, something else must have been revertible — a competing block, a fork, a conflicting
vote. None of those exist here. Each round hashes fresh random bytes; round 5 has no relationship
to round 4. There is no chain, no parent hash, and no way to express a conflict.

**Why it isn't probabilistic yet.** The word "probabilistic" appears exactly once in the Go
sources — in a usage string. There is no `t_evidence` threshold formula, no `p_func` probability
mapping, and no VRF. The mechanism that makes the design *probabilistic* — its central novel
claim — is not implemented.

### Implementation status

| Component | Status |
|---|---|
| Authenticated networking (Ed25519, nonce handshake, domain-separated signatures) | ✅ Implemented & tested |
| Threshold vote collection with timeout and fallback trigger | ✅ Implemented & tested |
| WAN latency measurement harness (CSV, chaos suites, analysis scripts) | ✅ Implemented |
| Blocks forming a chain (parent hash) | ❌ Not implemented — rounds are independent |
| Conflicting blocks / forks / equivocation handling | ❌ Not implemented |
| Real multi-round BFT baseline | ❌ Not implemented — currently a fixed `time.Sleep` |
| Probabilistic mechanism (`t_evidence`, `p_func`, VRF) | ❌ Not implemented |
| Byzantine validator tests | ❌ Not implemented |
| Formal safety & liveness proofs | 🔄 In progress (on paper) |

### The one-sentence honest summary

> A Go implementation of the vote-collection stage of a proposed fast-path finality gadget,
> with an authenticated Ed25519 protocol and a WAN latency measurement across 5 nodes.
> Single-round signature collection completes in ~177 ms at 4 validators. The comparison
> against multi-round BFT, and the probabilistic finality mechanism itself, are not yet
> implemented.

---

## The Proposed Design

*This section describes the idea being investigated, not what the code currently implements.*

Standard BFT protocols (e.g. HotStuff) require three consecutive voting rounds per block, even
when the network is perfectly healthy. PFF proposes a fast path that short-circuits this when
strong evidence of agreement is observed:

```
  Coordinator broadcasts signed block hash
              │
  ┌───────────▼────────────┐
  │  Collect validator     │
  │  signatures (300 ms)   │   ← this stage is implemented
  └───────────┬────────────┘
              │
  ┌───────────▼────────────┐
  │  ≥ 90% signatures?     │   ← this decision is implemented
  └────┬──────────────┬────┘
       │ YES          │ NO
  ┌────▼────┐   ┌─────▼──────┐
  │FAST_PFF │   │FALLBACK_BFT│   ← this branch is a fixed sleep, not BFT
  └─────────┘   └────────────┘
```

The safety argument for the design: waiting for the tail latency of one 90% quorum round should
be faster than three consecutive 67% quorum round-trips under healthy network conditions.
**This argument is not yet substantiated by the code** — see the next section.

---

## What the Measurements Do and Do Not Show

**Genuinely measured.** The fast path is a true end-to-end measurement: a real Ed25519-signed
proposal broadcast over real TCP to real validators, with every returned signature verified
before it is counted. The ~177 ms figure from the 5-node VPS run is a real WAN round-trip.

**Not measured.** `FALLBACK_BFT` is **not an implementation of HotStuff or any BFT protocol**.
It is `time.Sleep(bft_delay_ms)` standing in for multi-round processing
(`coordinator.go`, `runRound`). A fallback round therefore costs
`fast_timeout_ms + bft_delay_ms` **by construction** — that is arithmetic, not measurement.

Consequently, **the headline "46% faster" figure is not supported by this code.** It compares a
measurement against a constant that was chosen rather than observed. To make it real,
`FALLBACK_BFT` must run an actual three-phase BFT round against the same validator set over the
same links.

**A numerical inconsistency worth noting.** With the documented defaults (300 ms + 150 ms) a
fallback round costs ~450 ms, which does not match the ~331 ms in the original write-up. The
published figure is consistent with a shorter timeout (~180 ms + 150 ms ≈ 330 ms), suggesting
the constants in that run differed from the defaults here. Treat the published fallback number
as unverified by this code.

### Known methodology gaps

These apply even to the fast-path number, and a reviewer will likely raise them:

- **k = 0.90 was not actually tested.** At 4 validators, `ceil(4 × 0.9) = 4` — the run required
  *unanimity*, not a 90% threshold. The threshold is only faithfully representable at ≥10
  validators.
- **No repetitions or confidence intervals.** A single 500-round run yields a mean with no error
  bars. Independent repeated runs, plus p50/p95/p99, would be substantially stronger — tail
  latency is the entire argument for a fast path, and only means are currently reported.
- **Validator-count and throughput sweeps exceed the hardware.** The figures below plot 10–500
  validators and up to 100k TPS; the deployment was 5 nodes. Those curves are **modeled or
  extrapolated, not measured**, and are retained here for illustration only.
- **The safety heatmap plots a formula, not an experiment.** No conflicting-block scenario is
  executed anywhere in the code, so no safety property has been empirically tested.
- **All timing is taken at the coordinator**, so it includes that node's own scheduling delays.

---

## Results (original 5-node VPS run)

> Measured across 500 consensus rounds on 5 live VPS nodes over real WAN links.
> ✅ = measured · ⚠️ = simulated or modeled

| Path | Mean Latency | Condition | Evidence |
|---|---|---|---|
| `FAST_PFF` | ~177 ms | Quorum reached before timeout | ✅ Measured |
| `FALLBACK_BFT` | ~331 ms | Timeout expired | ⚠️ `timeout + fixed sleep` |
| *Improvement* | *~46%* | — | ❌ Not substantiated — no BFT baseline exists |

<details>
<summary>Figures from the original run — click to expand (note the ⚠️ labels)</summary>

<br>

![Latency comparison across validator counts](docs/pff_vs_hotstuff_latency_comparison.png)

⚠️ The "standard HotStuff" line is the fixed-delay stand-in, not a HotStuff implementation.
Points beyond 5 validators are extrapolated, not measured.

<br>

![Latency proof and threshold sweet spot](docs/latency_proof_threshold_sweetspot.png)

**Left — Latency across validator counts.** The fast path holds ~177 ms; the comparison line is
the simulated fallback. ⚠️ Only the 4-validator point is measured.

**Right — Threshold vs latency.** Latency rises gradually from k=0.70 → 0.95, steepening beyond
0.95, which is the basis for choosing k=0.90. ⚠️ Modeled; the deployment could not vary k
meaningfully at 4 validators.

<br>

![Throughput scaling and safety heatmap](docs/throughput_scaling_safety_heatmap.png)

**Left — Throughput scaling.** Latency flat at 182–186 ms up to ~50k TPS, with a spike at 100k
reflecting coordinator saturation. ⚠️ Beyond the tested load on 5 nodes.

**Right — Safety margin heatmap.** Margin across adversarial power `f` and threshold `k`, with
the design point (k=0.90, f≤0.20) marked. ⚠️ Analytical formula, not an executed experiment.

</details>

---

## Architecture

```
pff-sidecar/
├── main.go             # Entry point — -mode flag routing (coordinator | validator | keygen)
├── coordinator.go      # Broadcasts proposals, collects votes, decides fast vs fallback
├── validator.go        # Verifies coordinator signature, signs block hash, votes
├── crypto.go           # Domain-separated signing digests
├── network.go          # TCP wire protocol — length-prefixed JSON framing
├── config.go           # Ed25519 keygen, protocol constants, cluster config loading
├── pff_test.go         # Unit + end-to-end tests
├── analyze_results.py  # Post-experiment graph generation (Python/matplotlib)
└── docs/               # Result graphs and documentation assets
```

No external Go dependencies — pure stdlib.

**Wire protocol** (`network.go`): JSON messages with a 4-byte big-endian length prefix.

| Message Type | Direction | Purpose |
|---|---|---|
| `CONNECT` | Validator → Coordinator | Begin registration |
| `CHALLENGE` | Coordinator → Validator | Random 32-byte auth nonce |
| `AUTH` | Validator → Coordinator | Nonce signed with the validator's key |
| `ACCEPTED` | Coordinator → Validator | Handshake complete |
| `PROPOSE` | Coordinator → Validators | Signed block hash broadcast |
| `VOTE` | Validators → Coordinator | Ed25519-signed vote |

**Concurrency model.** Each authenticated connection has exactly one long-lived reader goroutine
feeding a shared inbox channel, drained by the round loop. No per-round readers and no read
deadlines on the steady-state path — that combination previously allowed a single late vote to
desynchronise a connection permanently.

---

## Protocol Constants

Defined in `config.go`, overridable in `cluster_config.json`:

| Field | Default | Meaning |
|---|---|---|
| `pff_threshold` | `0.90` | Fraction of **configured** validators required for the fast path |
| `bft_threshold` | `0.67` | Standard BFT quorum threshold (currently unused in the round logic) |
| `fast_timeout_ms` | `300` | Deadline for fast-path vote collection |
| `bft_delay_ms` | `150` | Simulated BFT processing delay on the fallback path |

Quorum is `ceil(validators × pff_threshold)`, computed from the validator count in the config —
**not** from how many validators are currently connected. At 4 validators, k=0.90 requires
`ceil(3.6) = 4` votes, i.e. unanimity; use ≥10 validators for 0.90 to be meaningful.

---

## Quick Start

### Prerequisites

- **Go 1.21+**
- **5 nodes** with TCP port `8080` reachable between them
- **Python 3.8+** with `pip install -r requirements.txt` *(analysis only)*

### 1. Generate keypairs — run once, on a trusted machine

```bash
go run . -mode=keygen \
  -nodes=node1:203.0.113.1:8080:coordinator,\
node2:203.0.113.2:8080:validator,\
node3:203.0.113.3:8080:validator,\
node4:203.0.113.4:8080:validator,\
node5:203.0.113.5:8080:validator
```

Substitute your own IPs. This writes:

- `cluster_config.json` — **public keys only**, safe to distribute to every node
- `keys/<node-id>.key` — one private key per node, mode `0600`

> ⚠️ **Give each node only its own key file.** If every node holds every private key, any node
> can forge any other node's vote and signature verification proves nothing about who actually
> voted. Each node loads its key from `-key-dir` (default `keys/`) and refuses to start if that
> key does not match its public key in the config.

### 2. Build

```bash
go build -o pff-sidecar .
```

Copy the binary, `cluster_config.json`, and that node's single `.key` file to each node.

### 3. Start validators — Nodes 2–5

```bash
./pff-sidecar -mode=validator -node-id=node2   # on node 2
./pff-sidecar -mode=validator -node-id=node3   # on node 3
./pff-sidecar -mode=validator -node-id=node4   # on node 4
./pff-sidecar -mode=validator -node-id=node5   # on node 5
```

Validators retry the coordinator up to **40 times** (3 s apart), so they can be started first.

### 4. Start the coordinator — Node 1

```bash
./pff-sidecar -mode=coordinator -rounds=500 -suite=A3_Baseline
```

The coordinator waits for every configured validator to authenticate (5 min limit), runs the
rounds, and streams results to `results.csv`.

---

## CLI Reference

| Flag | Default | Description |
|---|---|---|
| `-mode` | — | `keygen` \| `coordinator` \| `validator` (required) |
| `-node-id` | — | Node ID for validator mode, e.g. `node2` (required in validator mode) |
| `-nodes` | — | Comma-separated `id:ip:port:role` specs (required in keygen mode) |
| `-rounds` | `100` | Number of consensus rounds to run (coordinator only) |
| `-suite` | `A3_Baseline` | Test suite label written to `results.csv` |
| `-config` | `cluster_config.json` | Path to cluster config file |
| `-key-dir` | `keys` | Directory holding this node's private key |

---

## Testing

```bash
go test ./...          # unit + end-to-end
go test -race ./...    # recommended: the coordinator is concurrent
```

### What the tests prove

| Test | Proves |
|---|---|
| `TestQuorumSize`, `TestQuorumIgnoresConnectedPeers` | Threshold arithmetic is correct; quorum cannot silently shrink when validators drop |
| `TestDigestsBindRound`, `TestVoteDigestBindsVoter`, `TestDomainSeparation`, `TestDigestCanonicalEncoding` | Signatures are not replayable across rounds, voters, or roles |
| `TestSendReceiveRoundTrip`, `TestFramesStayAlignedBackToBack`, `TestReceiveRejectsOversizedFrame` | Wire framing is correct, ordered, and bounded |
| `TestConfigValidation`, `TestLoadPrivateKeyDetectsMismatch`, `TestKeygenKeepsPrivateKeysOutOfConfig` | Misconfiguration fails loudly at startup; private keys stay isolated |
| `TestHandshakeRejectsForgedIdentity` | An unauthenticated client cannot impersonate a validator |
| `TestEndToEndFastPath` | The full protocol completes over real TCP and reports the fast path |
| `TestSlowValidatorDoesNotDesyncStream` | A validator that is late once still contributes in later rounds (regression test) |

**Scope: software correctness on loopback.** These tests say the implementation does what it
claims. They deliberately cannot establish any of the following:

- **Latency figures** — loopback RTT is ~0.05 ms; timing claims require the WAN deployment.
- **The speedup ratio** — there is no BFT baseline in the code to compare against.
- **Safety** — no test makes two validators sign conflicting blocks, because the protocol has no
  representation of a conflict.

---

## Roadmap to a Defensible Result

Ordered by how much each step contributes, and each depends roughly on the one before:

1. **Give blocks a parent hash so they form a chain.** Prerequisite for everything else: until
   two blocks can conflict, there is nothing to finalize and no safety property to test.
2. **Implement a real three-phase BFT baseline** (prepare → pre-commit → commit with quorum
   certificates) and alternate PFF and BFT rounds on the same nodes over the same links, so
   network conditions are shared. This is what makes the speedup a measurement. ~200–300 lines.
3. **Implement the probabilistic mechanism** — `t_evidence` and `p_func`. This is the actual
   novel contribution and is currently absent.
4. **Add Byzantine validator tests** — equivocation, conflicting votes, garbage signatures,
   selective silence. Required before any safety claim.
5. **Strengthen the statistics** — ≥3 independent runs, p50/p95/p99, confidence intervals, and
   ≥10 validators so k=0.90 is actually exercised.

---

## Chaos Engineering Suites

| Suite | Label | What It Tests |
|---|---|---|
| A.3 | `A3_Baseline` | Healthy network — no interference |
| C.1 | `C1_Jitter` | Network jitter via `tc netem` (100 ms ± 30 ms) |
| C.3 | `C3_Partition` | Partition simulation via `iptables`, then heal |

---

## Analyzing Results

```bash
pip install -r requirements.txt
python analyze_results.py
```

Outputs to `figures/`:
- `Graph_1_Noise_Impact.png` — boxplot comparing `A3_Baseline` vs `C1_Jitter`
- `Graph_2_Protocol_Resumption.png` — round-by-round latency timeline for `C3_Partition`

### `results.csv` schema

| Column | Type | Description |
|---|---|---|
| `RoundID` | int | Consensus round number |
| `TestSuite` | string | Suite label (e.g. `A3_Baseline`) |
| `LatencyMS` | float | Total round time, **including** the simulated fallback delay |
| `CollectMS` | float | Vote-collection time only, excluding the fallback delay |
| `SignaturesReceived` | int | Valid, distinct validator signatures collected |
| `QuorumNeeded` | int | Votes required for the fast path this round |
| `ValidatorsTotal` | int | Validators defined in the config |
| `PeersConnected` | int | Validators actually connected this round |
| `PathTaken` | string | `FAST_PFF` or `FALLBACK_BFT` |

**Use `CollectMS` for latency analysis.** It is a pure measurement, whereas `LatencyMS` includes
the synthetic `bft_delay_ms` on fallback rounds.

---

## Security Notes

The networking layer is the most mature part of this repository:

- **Key isolation** — each node holds only its own private key; `cluster_config.json` contains
  public keys only.
- **Authenticated handshake** — validators prove key ownership by signing a coordinator-chosen
  nonce. Unknown IDs are rejected, and an already-registered ID cannot be displaced by a second
  connection.
- **Domain-separated signatures** — every signature commits to a domain tag, the round ID, and
  (for votes) the voter's ID, so no signature can be replayed across rounds, roles, or identities.
- **Duplicate votes rejected** — one vote per validator per round, so a single node cannot reach
  quorum alone.
- **Bounded frames** — messages capped at 1 MB; the handshake has a 10-second deadline.

> **Scope note:** these protect the *transport and authentication* layer. They are not consensus
> safety guarantees — the protocol has no fork-choice rule or equivocation handling, so a
> Byzantine validator's misbehaviour is currently out of scope rather than defended against.

<details>
<summary>Bugs fixed in the current revision</summary>

<br>

| Issue | Impact |
|---|---|
| Per-round readers with read deadlines | A vote arriving after the deadline stayed buffered and was consumed by the next round's reader, leaving the connection permanently one message behind — the validator never scored again. Worst under exactly the jitter and partition suites this project measures, so earlier results from those suites are suspect. |
| Concurrent readers on one socket | When quorum was reached early, the previous round's reader was still in `ReadFull` while the next round started another. Two concurrent reads on one connection tear frames. |
| Quorum divided by connected peers | With 2 of 4 validators connected, 2 votes cleared "90%" at 50% of the cluster. |
| Round ID not signed | Signatures covered the bare block hash, so a proposal could be replayed under a different round number. |
| Unauthenticated `CONNECT` | Any TCP client claiming `node2` displaced the real node2. |
| All private keys in the shared config | Any node could forge any other node's vote. |
| Non-atomic frame writes | Length prefix and body were separate writes; a concurrent writer could interleave between them. |
| Hot-spinning accept loop | A closed listener returned the same error instantly forever, pinning a core. |

</details>

---

## Contributing

This is an active research project at an early stage. The roadmap above is the most useful place
to start.

1. Open an [Issue](https://github.com/abdoulaahmad/PFF-Lab/issues) to discuss
2. Fork → branch → PR for code contributions
3. For research discussion, reach out via the Medium article comments

---

<div align="center">

Made with ⚡ by [@abdoulaahmad](https://github.com/abdoulaahmad)

</div>
