package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// vote holds a validated signature from one validator for one round.
type vote struct {
	nodeID string
	sig    []byte
}

// Coordinator drives the consensus rounds and owns the TCP server that
// validators connect to.
type Coordinator struct {
	cfg    *ClusterConfig
	rounds int
	suite  string // TestSuite label written to results.csv

	privKey ed25519.PrivateKey
	pubKey  ed25519.PublicKey

	mu    sync.RWMutex
	conns map[string]net.Conn // nodeID → persistent connection

	csvFile   *os.File
	csvWriter *csv.Writer
}

// NewCoordinator constructs a Coordinator from config.
func NewCoordinator(cfg *ClusterConfig, rounds int, suite string) *Coordinator {
	return &Coordinator{
		cfg:    cfg,
		rounds: rounds,
		suite:  suite,
		conns:  make(map[string]net.Conn),
	}
}

// ── Entry point ──────────────────────────────────────────────────────────────

func (c *Coordinator) Run() error {
	node := c.cfg.GetCoordinator()
	if node == nil {
		return fmt.Errorf("no coordinator node defined in cluster_config.json")
	}

	privKey, err := node.GetPrivateKey()
	if err != nil {
		return err
	}
	c.privKey = privKey
	c.pubKey = privKey.Public().(ed25519.PublicKey)

	if err := c.initCSV(); err != nil {
		return err
	}
	defer c.csvFile.Close()

	// Start TCP listener
	addr := fmt.Sprintf("0.0.0.0:%d", node.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("binding %s: %w", addr, err)
	}
	defer ln.Close()

	validatorCount := len(c.cfg.GetValidators())
	log.Printf("[COORDINATOR] Listening on %s — awaiting %d validators…", addr, validatorCount)

	stopAccept := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stopAccept:
					return
				default:
					log.Printf("[COORDINATOR] accept error: %v", err)
				}
				continue
			}
			go c.handleConnect(conn)
		}
	}()

	// Wait for all validators to register
	for {
		c.mu.RLock()
		n := len(c.conns)
		c.mu.RUnlock()
		if n >= validatorCount {
			break
		}
		log.Printf("[COORDINATOR] Registered %d/%d validators — waiting…", n, validatorCount)
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("[COORDINATOR] All %d validators connected. Starting %d rounds (suite=%s)…",
		validatorCount, c.rounds, c.suite)
	time.Sleep(500 * time.Millisecond) // brief stabilisation

	// Run consensus rounds
	for r := 1; r <= c.rounds; r++ {
		if err := c.runRound(r); err != nil {
			log.Printf("[COORDINATOR] round %d error: %v", r, err)
		}
		time.Sleep(50 * time.Millisecond) // inter-round gap
	}

	close(stopAccept)
	log.Printf("[COORDINATOR] ✅ Done. Results → results.csv")
	return nil
}

// ── Validator registration ───────────────────────────────────────────────────

func (c *Coordinator) handleConnect(conn net.Conn) {
	msg, err := ReceiveMessage(conn)
	if err != nil || msg.Type != MsgConnect || msg.NodeID == "" {
		log.Printf("[COORDINATOR] bad handshake from %s: %v", conn.RemoteAddr(), err)
		conn.Close()
		return
	}
	c.mu.Lock()
	c.conns[msg.NodeID] = conn
	c.mu.Unlock()
	log.Printf("[COORDINATOR] ✅ Validator %s registered from %s", msg.NodeID, conn.RemoteAddr())
}

// ── Consensus round ──────────────────────────────────────────────────────────

func (c *Coordinator) runRound(roundID int) error {
	// Generate random block hash
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("rand: %w", err)
	}
	h := sha256.Sum256(raw)
	hashHex := hex.EncodeToString(h[:])
	coordSig := ed25519.Sign(c.privKey, h[:])

	propose := &Message{
		Type:      MsgPropose,
		RoundID:   roundID,
		BlockHash: hashHex,
		Signature: base64.StdEncoding.EncodeToString(coordSig),
		TestSuite: c.suite,
	}

	// Snapshot connections
	c.mu.RLock()
	conns := make(map[string]net.Conn, len(c.conns))
	for id, conn := range c.conns {
		conns[id] = conn
	}
	c.mu.RUnlock()

	total := len(conns)
	pffNeeded := int(float64(total)*c.cfg.PFFThreshold + 0.5) // round-half-up
	if pffNeeded < 1 {
		pffNeeded = 1
	}

	voteCh := make(chan vote, total)
	start := time.Now()

	// Broadcast proposal in parallel; each goroutine waits for one reply
	timeout := time.Duration(c.cfg.FastTimeoutMS) * time.Millisecond
	for nid, conn := range conns {
		go func(id string, nc net.Conn) {
			if err := SendMessage(nc, propose); err != nil {
				log.Printf("[COORDINATOR] round %d: send→%s failed: %v", roundID, id, err)
				return
			}
			nc.SetReadDeadline(time.Now().Add(timeout))
			resp, err := ReceiveMessage(nc)
			nc.SetReadDeadline(time.Time{})
			if err != nil {
				log.Printf("[COORDINATOR] round %d: no reply from %s: %v", roundID, id, err)
				return
			}
			if resp.Type != MsgVote || resp.RoundID != roundID {
				return
			}
			sigBytes, err := base64.StdEncoding.DecodeString(resp.Signature)
			if err != nil {
				return
			}
			// Verify validator signature
			vNode := c.cfg.GetNodeByID(id)
			if vNode == nil {
				return
			}
			pub, err := vNode.GetPublicKey()
			if err != nil {
				return
			}
			hashBytes, _ := hex.DecodeString(hashHex)
			if !ed25519.Verify(pub, hashBytes, sigBytes) {
				log.Printf("[COORDINATOR] round %d: ❌ INVALID sig from %s", roundID, id)
				return
			}
			voteCh <- vote{nodeID: id, sig: sigBytes}
		}(nid, conn)
	}

	// Collect votes until PFF quorum or timeout
	votes := make([]vote, 0, total)
	deadline := time.After(timeout)
collect:
	for {
		select {
		case v := <-voteCh:
			votes = append(votes, v)
			log.Printf("[COORDINATOR] round %d: vote from %-6s (%d/%d)", roundID, v.nodeID, len(votes), total)
			if len(votes) >= pffNeeded {
				break collect
			}
		case <-deadline:
			break collect
		}
	}

	elapsed := time.Since(start)
	latencyMS := float64(elapsed.Microseconds()) / 1000.0
	sigCount := len(votes)
	path := "FALLBACK_BFT"

	if sigCount >= pffNeeded {
		path = "FAST_PFF"
		log.Printf("[COORDINATOR] ✅ Round %4d | FAST_PFF     | %.3f ms | sigs=%d/%d",
			roundID, latencyMS, sigCount, total)
	} else {
		// Simulate BFT multi-round processing delay
		time.Sleep(time.Duration(BFTDelayMS) * time.Millisecond)
		elapsed = time.Since(start)
		latencyMS = float64(elapsed.Microseconds()) / 1000.0
		log.Printf("[COORDINATOR] ⚠️  Round %4d | FALLBACK_BFT | %.3f ms | sigs=%d/%d",
			roundID, latencyMS, sigCount, total)
	}

	c.csvWriter.Write([]string{
		strconv.Itoa(roundID),
		c.suite,
		fmt.Sprintf("%.3f", latencyMS),
		strconv.Itoa(sigCount),
		path,
	})
	c.csvWriter.Flush()
	return nil
}

// ── CSV initialisation ───────────────────────────────────────────────────────

func (c *Coordinator) initCSV() error {
	f, err := os.OpenFile("results.csv", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening results.csv: %w", err)
	}
	c.csvFile = f
	c.csvWriter = csv.NewWriter(f)
	// Write header only for a fresh file
	if info, _ := f.Stat(); info.Size() == 0 {
		c.csvWriter.Write([]string{
			"RoundID", "TestSuite", "LatencyMS", "SignaturesReceived", "PathTaken",
		})
		c.csvWriter.Flush()
	}
	return nil
}
