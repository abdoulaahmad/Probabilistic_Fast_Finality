package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// inbound carries one message up from a peer's reader goroutine, tagged with
// the authenticated node ID it arrived from.
type inbound struct {
	nodeID string
	msg    *Message
}

// peer is one authenticated validator connection.
type peer struct {
	nodeID string
	conn   net.Conn
	pubKey ed25519.PublicKey

	writeMu sync.Mutex // serialises writes to conn
}

func (p *peer) send(msg *Message) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return SendMessage(p.conn, msg)
}

// Coordinator drives the consensus rounds and owns the TCP server that
// validators connect to.
type Coordinator struct {
	cfg    *ClusterConfig
	rounds int
	suite  string // TestSuite label written to results.csv

	privKey ed25519.PrivateKey

	mu    sync.RWMutex
	peers map[string]*peer // nodeID → authenticated connection

	// inbox receives every message from every peer reader. A single consumer
	// (the round loop) drains it, so the socket is never read from two places.
	inbox chan inbound

	csvFile   *os.File
	csvWriter *csv.Writer

	shutdown chan struct{}
	wg       sync.WaitGroup
}

// NewCoordinator constructs a Coordinator from config.
func NewCoordinator(cfg *ClusterConfig, rounds int, suite string) *Coordinator {
	return &Coordinator{
		cfg:      cfg,
		rounds:   rounds,
		suite:    suite,
		peers:    make(map[string]*peer),
		inbox:    make(chan inbound, 1024),
		shutdown: make(chan struct{}),
	}
}

// ── Entry point ──────────────────────────────────────────────────────────────

func (c *Coordinator) Run(keyDir string) error {
	node := c.cfg.GetCoordinator()
	if node == nil {
		return fmt.Errorf("no coordinator node defined in the cluster config")
	}

	privKey, err := LoadPrivateKey(keyDir, node)
	if err != nil {
		return err
	}
	c.privKey = privKey

	if err := c.initCSV(); err != nil {
		return err
	}
	defer func() {
		c.csvWriter.Flush()
		c.csvFile.Close()
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", node.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("binding %s: %w", addr, err)
	}
	defer ln.Close()

	validatorCount := len(c.cfg.GetValidators())
	quorum := c.quorumSize()
	log.Printf("[COORDINATOR] Listening on %s — awaiting %d validators…", addr, validatorCount)
	log.Printf("[COORDINATOR] Fast path needs %d/%d votes (threshold %.2f) within %d ms",
		quorum, validatorCount, c.cfg.PFFThreshold, c.cfg.FastTimeoutMS)

	go c.acceptLoop(ln)

	if err := c.awaitValidators(validatorCount); err != nil {
		return err
	}
	log.Printf("[COORDINATOR] All %d validators connected. Starting %d rounds (suite=%s)…",
		validatorCount, c.rounds, c.suite)
	time.Sleep(500 * time.Millisecond) // brief stabilisation

	for r := 1; r <= c.rounds; r++ {
		if err := c.runRound(r); err != nil {
			log.Printf("[COORDINATOR] round %d error: %v", r, err)
		}
		time.Sleep(50 * time.Millisecond) // inter-round gap
	}

	c.stop(ln)
	log.Printf("[COORDINATOR] Done. Results → results.csv")
	return nil
}

// awaitValidators blocks until every configured validator has authenticated.
func (c *Coordinator) awaitValidators(want int) error {
	const registrationTimeout = 5 * time.Minute
	deadline := time.After(registrationTimeout)

	for {
		c.mu.RLock()
		have := len(c.peers)
		c.mu.RUnlock()
		if have >= want {
			return nil
		}

		select {
		case <-deadline:
			return fmt.Errorf("only %d/%d validators registered within %s",
				have, want, registrationTimeout)
		case <-time.After(500 * time.Millisecond):
			log.Printf("[COORDINATOR] Registered %d/%d validators — waiting…", have, want)
		}
	}
}

func (c *Coordinator) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-c.shutdown:
				return
			default:
			}
			// A closed listener returns the same error immediately on every
			// call, so continuing here would spin the CPU. Only transient
			// errors are worth retrying.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("[COORDINATOR] accept error: %v", err)
			continue
		}
		go c.handleConnect(conn)
	}
}

// stop closes the listener and every peer connection, then waits for the reader
// goroutines to exit. Closing the connections is what tells validators the run
// is over so they can shut down cleanly.
func (c *Coordinator) stop(ln net.Listener) {
	close(c.shutdown)
	ln.Close()

	c.mu.Lock()
	for _, p := range c.peers {
		p.conn.Close()
	}
	c.peers = make(map[string]*peer)
	c.mu.Unlock()

	c.wg.Wait()
}

// ── Validator registration ───────────────────────────────────────────────────

// handleConnect performs the authenticated handshake:
//
//	validator → CONNECT{node_id}
//	coordinator → CHALLENGE{nonce}
//	validator → AUTH{signature over AuthDigest(node_id, nonce)}
//	coordinator → ACCEPTED
//
// The nonce is fresh per attempt, so a recorded handshake cannot be replayed,
// and only the holder of that node's private key can complete it. Without this
// step any TCP client could claim to be node2 and displace the real one.
func (c *Coordinator) handleConnect(conn net.Conn) {
	remote := conn.RemoteAddr()

	reject := func(format string, args ...any) {
		log.Printf("[COORDINATOR] handshake rejected from %s: "+format,
			append([]any{remote}, args...)...)
		conn.Close()
	}

	// Bound the handshake so a silent client cannot hold a slot open forever.
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		reject("setting deadline: %v", err)
		return
	}

	msg, err := ReceiveMessage(conn)
	if err != nil {
		reject("reading CONNECT: %v", err)
		return
	}
	if msg.Type != MsgConnect || msg.NodeID == "" {
		reject("expected CONNECT with node_id, got %q", msg.Type)
		return
	}

	node := c.cfg.GetNodeByID(msg.NodeID)
	if node == nil || node.Role != "validator" {
		reject("unknown validator id %q", msg.NodeID)
		return
	}
	pubKey, err := node.GetPublicKey()
	if err != nil {
		reject("bad public key for %s: %v", msg.NodeID, err)
		return
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		reject("generating nonce: %v", err)
		return
	}
	if err := SendMessage(conn, &Message{
		Type:  MsgChallenge,
		Nonce: base64.StdEncoding.EncodeToString(nonce),
	}); err != nil {
		reject("sending CHALLENGE: %v", err)
		return
	}

	resp, err := ReceiveMessage(conn)
	if err != nil {
		reject("reading AUTH: %v", err)
		return
	}
	if resp.Type != MsgAuth || resp.NodeID != msg.NodeID {
		reject("expected AUTH from %s, got %q from %q", msg.NodeID, resp.Type, resp.NodeID)
		return
	}
	sig, err := base64.StdEncoding.DecodeString(resp.Signature)
	if err != nil {
		reject("decoding AUTH signature: %v", err)
		return
	}
	if !ed25519.Verify(pubKey, AuthDigest(msg.NodeID, nonce), sig) {
		reject("INVALID auth signature for %s", msg.NodeID)
		return
	}

	// Clear the handshake deadline before registering. All handshake reads are
	// done, and the deadline set earlier covers writes too — leaving it in
	// place would make a later PROPOSE write fail once the wall-clock deadline
	// passed, even on a perfectly healthy connection.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		reject("clearing deadline: %v", err)
		return
	}

	// Reserve the ID. A second connection for an already-registered ID is
	// rejected rather than silently replacing it — an overwrite would strand
	// the original connection's reader and drop a live validator from the round.
	p := &peer{nodeID: msg.NodeID, conn: conn, pubKey: pubKey}

	// Hold the peer's write lock across registration and the ACCEPTED send.
	// Once the peer is in the map a concurrent round broadcast can call
	// p.send(PROPOSE); taking the lock first guarantees ACCEPTED is written
	// first, since every write to this connection goes through the same mutex.
	// A validator that saw PROPOSE while awaiting ACCEPTED would fail its
	// handshake and drop out of the run.
	p.writeMu.Lock()

	c.mu.Lock()
	if _, exists := c.peers[msg.NodeID]; exists {
		c.mu.Unlock()
		p.writeMu.Unlock()
		reject("%s is already registered", msg.NodeID)
		return
	}
	c.peers[msg.NodeID] = p
	c.mu.Unlock()

	err = SendMessage(conn, &Message{Type: MsgAccepted})
	p.writeMu.Unlock()
	if err != nil {
		log.Printf("[COORDINATOR] %s: sending ACCEPTED: %v", msg.NodeID, err)
		c.dropPeer(msg.NodeID)
		return
	}

	log.Printf("[COORDINATOR] Validator %s authenticated from %s", msg.NodeID, remote)

	c.wg.Add(1)
	go c.readLoop(p)
}

func (c *Coordinator) dropPeer(nodeID string) {
	c.mu.Lock()
	p, ok := c.peers[nodeID]
	if ok {
		delete(c.peers, nodeID)
	}
	c.mu.Unlock()
	if ok {
		p.conn.Close()
	}
}

// readLoop is the ONLY reader for a peer's connection, for the lifetime of that
// connection.
//
// The previous design started a fresh reader per round with a 300 ms read
// deadline. That had two defects. First, a vote arriving after the deadline
// stayed buffered in the socket: the next round's reader consumed it, saw a
// stale round ID, and discarded it — leaving that connection permanently one
// message behind, so the validator never scored again. Second, when quorum was
// reached early the previous round's reader was still blocked in ReadFull while
// the next round started another, and two concurrent ReadFull calls on one
// socket tear frames apart.
//
// One long-lived reader per connection fixes both: every frame is consumed
// exactly once, in order, by exactly one goroutine. Late votes are read and
// then dropped at the round layer, which keeps the stream aligned.
func (c *Coordinator) readLoop(p *peer) {
	defer c.wg.Done()
	defer c.dropPeer(p.nodeID)

	for {
		msg, err := ReceiveMessage(p.conn)
		if err != nil {
			select {
			case <-c.shutdown:
				// Expected: we closed the connection at end of run.
			default:
				if !errors.Is(err, net.ErrClosed) {
					log.Printf("[COORDINATOR] %s: connection lost: %v", p.nodeID, err)
				}
			}
			return
		}

		select {
		case c.inbox <- inbound{nodeID: p.nodeID, msg: msg}:
		case <-c.shutdown:
			return
		}
	}
}

// ── Quorum ───────────────────────────────────────────────────────────────────

// quorumSize is the number of votes the fast path requires.
//
// It is computed from the number of validators in the cluster config, NOT from
// how many happen to be connected. Dividing by the live connection count would
// let the threshold shrink with the cluster: if 2 of 4 validators dropped, 2
// votes would be "90% of those present" but only 50% of the cluster — exactly
// the situation the threshold exists to rule out.
//
// Ceiling, not round-half-up: with 4 validators, round-half-up made k=0.90
// require int(3.6+0.5)=4 votes, i.e. unanimity, which is stricter than
// configured but only by accident. Ceiling states the intent — never fewer than
// the threshold demands. The epsilon absorbs float representation error so that
// e.g. 0.7*10 = 6.999999999999999 still yields 7 rather than 8.
func (c *Coordinator) quorumSize() int {
	total := len(c.cfg.GetValidators())
	const epsilon = 1e-9

	needed := int(math.Ceil(float64(total)*c.cfg.PFFThreshold - epsilon))
	if needed < 1 {
		needed = 1
	}
	if needed > total {
		needed = total
	}
	return needed
}

// ── Consensus round ──────────────────────────────────────────────────────────

func (c *Coordinator) runRound(roundID int) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("rand: %w", err)
	}
	h := sha256.Sum256(raw)
	blockHash := h[:]
	hashHex := hex.EncodeToString(blockHash)

	// Sign over (domain, roundID, blockHash) so a captured proposal cannot be
	// replayed under a different round number.
	coordSig := ed25519.Sign(c.privKey, ProposeDigest(roundID, blockHash))

	propose := &Message{
		Type:      MsgPropose,
		RoundID:   roundID,
		BlockHash: hashHex,
		Signature: base64.StdEncoding.EncodeToString(coordSig),
		TestSuite: c.suite,
	}

	c.mu.RLock()
	peers := make([]*peer, 0, len(c.peers))
	for _, p := range c.peers {
		peers = append(peers, p)
	}
	c.mu.RUnlock()

	validatorTotal := len(c.cfg.GetValidators())
	quorum := c.quorumSize()
	timeout := time.Duration(c.cfg.FastTimeoutMS) * time.Millisecond

	start := time.Now()

	// Broadcast. Writes are serialised per peer, and each peer's reader is
	// already running, so no reader is started or stopped here.
	for _, p := range peers {
		go func(p *peer) {
			if err := p.send(propose); err != nil {
				log.Printf("[COORDINATOR] round %d: send→%s failed: %v", roundID, p.nodeID, err)
			}
		}(p)
	}

	// Collect votes for THIS round until quorum or the deadline. Messages for
	// other rounds are drained and discarded — they have already been removed
	// from the socket by the peer's reader, so dropping them here costs nothing
	// and cannot desynchronise the stream.
	voted := make(map[string]bool, validatorTotal)
	deadline := time.After(timeout)

collect:
	for len(voted) < quorum {
		select {
		case in := <-c.inbox:
			if !c.validVote(in, roundID, blockHash, voted) {
				continue
			}
			voted[in.nodeID] = true
			log.Printf("[COORDINATOR] round %d: vote from %-6s (%d/%d)",
				roundID, in.nodeID, len(voted), quorum)
		case <-deadline:
			break collect
		}
	}

	collectMS := float64(time.Since(start).Microseconds()) / 1000.0
	sigCount := len(voted)

	path := "FAST_PFF"
	if sigCount < quorum {
		path = "FALLBACK_BFT"
		// Stand-in for the multi-round BFT protocol this prototype does not
		// implement. See the README: fallback latency is therefore
		// fast_timeout_ms + bft_delay_ms by construction, not a measurement of
		// a real BFT protocol.
		time.Sleep(time.Duration(c.cfg.BFTDelayMS) * time.Millisecond)
	}

	latencyMS := float64(time.Since(start).Microseconds()) / 1000.0

	if path == "FAST_PFF" {
		log.Printf("[COORDINATOR] Round %4d | FAST_PFF     | %8.3f ms | sigs=%d/%d",
			roundID, latencyMS, sigCount, validatorTotal)
	} else {
		log.Printf("[COORDINATOR] Round %4d | FALLBACK_BFT | %8.3f ms | sigs=%d/%d (need %d)",
			roundID, latencyMS, sigCount, validatorTotal, quorum)
	}

	return c.writeRow([]string{
		strconv.Itoa(roundID),
		c.suite,
		fmt.Sprintf("%.3f", latencyMS),
		fmt.Sprintf("%.3f", collectMS),
		strconv.Itoa(sigCount),
		strconv.Itoa(quorum),
		strconv.Itoa(validatorTotal),
		strconv.Itoa(len(peers)),
		path,
	})
}

// validVote reports whether an inbound message is a countable vote for this
// round: right type, right round, not a duplicate, and a valid signature from
// the node that sent it.
func (c *Coordinator) validVote(in inbound, roundID int, blockHash []byte, voted map[string]bool) bool {
	msg := in.msg

	if msg.Type != MsgVote || msg.RoundID != roundID {
		return false // stale or unexpected — already drained, safe to drop
	}
	// One vote per validator per round. Without this, a single misbehaving
	// validator could reach quorum on its own by sending the same vote N times.
	if voted[in.nodeID] {
		log.Printf("[COORDINATOR] round %d: duplicate vote from %s — ignored", roundID, in.nodeID)
		return false
	}

	c.mu.RLock()
	p, ok := c.peers[in.nodeID]
	c.mu.RUnlock()
	if !ok {
		return false
	}

	sig, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		return false
	}
	// The digest binds the round and the voter's ID, so this signature is not
	// replayable into another round or attributable to another node.
	if !ed25519.Verify(p.pubKey, VoteDigest(roundID, blockHash, in.nodeID), sig) {
		log.Printf("[COORDINATOR] round %d: INVALID sig from %s", roundID, in.nodeID)
		return false
	}
	return true
}

// ── CSV ──────────────────────────────────────────────────────────────────────

var csvHeader = []string{
	"RoundID", "TestSuite", "LatencyMS", "CollectMS",
	"SignaturesReceived", "QuorumNeeded", "ValidatorsTotal", "PeersConnected", "PathTaken",
}

func (c *Coordinator) writeRow(row []string) error {
	if err := c.csvWriter.Write(row); err != nil {
		return fmt.Errorf("writing csv row: %w", err)
	}
	c.csvWriter.Flush()
	return c.csvWriter.Error()
}

func (c *Coordinator) initCSV() error {
	f, err := os.OpenFile("results.csv", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening results.csv: %w", err)
	}
	c.csvFile = f
	c.csvWriter = csv.NewWriter(f)

	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		if err := c.writeRow(csvHeader); err != nil {
			return err
		}
	}
	return nil
}
