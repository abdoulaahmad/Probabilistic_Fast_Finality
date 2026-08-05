package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"
)

// Validator connects to the coordinator, verifies proposals, and votes.
type Validator struct {
	cfg         *ClusterConfig
	nodeID      string
	privKey     ed25519.PrivateKey
	coordPubKey ed25519.PublicKey
}

// NewValidator loads this node's own private key and the coordinator's public
// key. It never has access to any other validator's private key.
func NewValidator(cfg *ClusterConfig, nodeID, keyDir string) (*Validator, error) {
	node := cfg.GetNodeByID(nodeID)
	if node == nil {
		return nil, fmt.Errorf("node %q not found in the cluster config", nodeID)
	}
	if node.Role != "validator" {
		return nil, fmt.Errorf("node %q has role %q, expected validator", nodeID, node.Role)
	}
	privKey, err := LoadPrivateKey(keyDir, node)
	if err != nil {
		return nil, err
	}
	coord := cfg.GetCoordinator()
	if coord == nil {
		return nil, fmt.Errorf("no coordinator node in config")
	}
	coordPub, err := coord.GetPublicKey()
	if err != nil {
		return nil, err
	}
	return &Validator{
		cfg:         cfg,
		nodeID:      nodeID,
		privKey:     privKey,
		coordPubKey: coordPub,
	}, nil
}

// Run connects to the coordinator and processes proposals until the connection
// drops (i.e. the coordinator has finished all rounds).
func (v *Validator) Run() error {
	coord := v.cfg.GetCoordinator()
	if coord == nil {
		return fmt.Errorf("no coordinator in config")
	}
	addr := fmt.Sprintf("%s:%d", coord.IP, coord.Port)

	conn, err := v.dial(addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("[VALIDATOR:%s] Connected to coordinator at %s", v.nodeID, addr)

	if err := v.handshake(conn); err != nil {
		return fmt.Errorf("handshake failed: %w", err)
	}
	log.Printf("[VALIDATOR:%s] Authenticated — listening for proposals…", v.nodeID)

	return v.signLoop(conn)
}

// dial retries the coordinator, which may start after the validators.
func (v *Validator) dial(addr string) (net.Conn, error) {
	const attempts = 40
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		log.Printf("[VALIDATOR:%s] Cannot reach coordinator at %s (attempt %d/%d), retrying…",
			v.nodeID, addr, attempt, attempts)
		time.Sleep(3 * time.Second)
	}
	return nil, fmt.Errorf("could not connect to coordinator after %d attempts: %w", attempts, lastErr)
}

// handshake proves ownership of this node's private key by signing a nonce the
// coordinator picks. See Coordinator.handleConnect for the other half.
func (v *Validator) handshake(conn net.Conn) error {
	// Bound the handshake; cleared once complete.
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("setting deadline: %w", err)
	}

	if err := SendMessage(conn, &Message{Type: MsgConnect, NodeID: v.nodeID}); err != nil {
		return fmt.Errorf("sending CONNECT: %w", err)
	}

	challenge, err := ReceiveMessage(conn)
	if err != nil {
		return fmt.Errorf("reading CHALLENGE: %w", err)
	}
	if challenge.Type != MsgChallenge {
		return fmt.Errorf("expected CHALLENGE, got %q (%s)", challenge.Type, challenge.Reason)
	}
	nonce, err := base64.StdEncoding.DecodeString(challenge.Nonce)
	if err != nil {
		return fmt.Errorf("decoding nonce: %w", err)
	}
	if len(nonce) < 16 {
		return fmt.Errorf("challenge nonce too short: %d bytes", len(nonce))
	}

	sig := ed25519.Sign(v.privKey, AuthDigest(v.nodeID, nonce))
	if err := SendMessage(conn, &Message{
		Type:      MsgAuth,
		NodeID:    v.nodeID,
		Signature: base64.StdEncoding.EncodeToString(sig),
	}); err != nil {
		return fmt.Errorf("sending AUTH: %w", err)
	}

	accepted, err := ReceiveMessage(conn)
	if err != nil {
		return fmt.Errorf("reading ACCEPTED: %w", err)
	}
	if accepted.Type != MsgAccepted {
		return fmt.Errorf("coordinator rejected registration: %q %s", accepted.Type, accepted.Reason)
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clearing deadline: %w", err)
	}
	return nil
}

// signLoop verifies each proposal and votes on it, until the coordinator closes
// the connection at the end of the run.
func (v *Validator) signLoop(conn net.Conn) error {
	for {
		msg, err := ReceiveMessage(conn)
		if err != nil {
			// Coordinator closes the connection after the final round.
			log.Printf("[VALIDATOR:%s] Connection closed: %v — shutting down.", v.nodeID, err)
			return nil
		}
		if msg.Type != MsgPropose {
			log.Printf("[VALIDATOR:%s] Unexpected message type %q — skipping", v.nodeID, msg.Type)
			continue
		}

		hashBytes, err := hex.DecodeString(msg.BlockHash)
		if err != nil || len(hashBytes) != 32 {
			log.Printf("[VALIDATOR:%s] round %d: bad block hash — skipping", v.nodeID, msg.RoundID)
			continue
		}

		// Verify the coordinator signed THIS hash for THIS round. Because the
		// round ID is inside the digest, a proposal captured from an earlier
		// round cannot be replayed at us under a new number.
		coordSig, err := base64.StdEncoding.DecodeString(msg.Signature)
		if err != nil ||
			!ed25519.Verify(v.coordPubKey, ProposeDigest(msg.RoundID, hashBytes), coordSig) {
			log.Printf("[VALIDATOR:%s] round %d: INVALID coordinator signature — rejecting proposal",
				v.nodeID, msg.RoundID)
			continue
		}

		// Our vote commits to the round and our own identity, so it cannot be
		// replayed into another round or attributed to another validator.
		mySig := ed25519.Sign(v.privKey, VoteDigest(msg.RoundID, hashBytes, v.nodeID))
		if err := SendMessage(conn, &Message{
			Type:      MsgVote,
			RoundID:   msg.RoundID,
			NodeID:    v.nodeID,
			Signature: base64.StdEncoding.EncodeToString(mySig),
		}); err != nil {
			log.Printf("[VALIDATOR:%s] round %d: failed to send vote: %v", v.nodeID, msg.RoundID, err)
			continue
		}
		log.Printf("[VALIDATOR:%s] round %d: voted for block %s…", v.nodeID, msg.RoundID, msg.BlockHash[:16])
	}
}
