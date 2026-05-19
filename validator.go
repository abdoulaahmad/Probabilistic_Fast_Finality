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
	cfg        *ClusterConfig
	nodeID     string
	privKey    ed25519.PrivateKey
	coordPubKey ed25519.PublicKey
}

// NewValidator loads keys from cfg for the given nodeID.
func NewValidator(cfg *ClusterConfig, nodeID string) (*Validator, error) {
	node := cfg.GetNodeByID(nodeID)
	if node == nil {
		return nil, fmt.Errorf("node %q not found in cluster_config.json", nodeID)
	}
	privKey, err := node.GetPrivateKey()
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
		cfg:        cfg,
		nodeID:     nodeID,
		privKey:    privKey,
		coordPubKey: coordPub,
	}, nil
}

// Run connects to the coordinator and processes proposals until the connection
// drops (i.e., coordinator has finished all rounds).
func (v *Validator) Run() error {
	coord := v.cfg.GetCoordinator()
	if coord == nil {
		return fmt.Errorf("no coordinator in config")
	}
	addr := fmt.Sprintf("%s:%d", coord.IP, coord.Port)

	// Retry loop — coordinator may start slightly after validators
	var conn net.Conn
	var err error
	for attempt := 1; attempt <= 40; attempt++ {
		conn, err = net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			break
		}
		log.Printf("[VALIDATOR:%s] Cannot reach coordinator at %s (attempt %d/40), retrying…",
			v.nodeID, addr, attempt)
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("could not connect to coordinator after 40 attempts: %w", err)
	}
	defer conn.Close()

	log.Printf("[VALIDATOR:%s] Connected to coordinator at %s", v.nodeID, addr)

	// Register
	if err := SendMessage(conn, &Message{
		Type:   MsgConnect,
		NodeID: v.nodeID,
	}); err != nil {
		return fmt.Errorf("sending CONNECT: %w", err)
	}
	log.Printf("[VALIDATOR:%s] Registered — listening for proposals…", v.nodeID)

	// Main signing loop
	for {
		msg, err := ReceiveMessage(conn)
		if err != nil {
			// Connection closed by coordinator after all rounds — normal shutdown
			log.Printf("[VALIDATOR:%s] Connection closed: %v — shutting down.", v.nodeID, err)
			return nil
		}
		if msg.Type != MsgPropose {
			log.Printf("[VALIDATOR:%s] Unexpected message type %q — skipping", v.nodeID, msg.Type)
			continue
		}

		// 1. Decode block hash
		hashBytes, err := hex.DecodeString(msg.BlockHash)
		if err != nil {
			log.Printf("[VALIDATOR:%s] round %d: bad block hash — skipping", v.nodeID, msg.RoundID)
			continue
		}

		// 2. Verify coordinator's proposal signature
		coordSigBytes, err := base64.StdEncoding.DecodeString(msg.Signature)
		if err != nil || !ed25519.Verify(v.coordPubKey, hashBytes, coordSigBytes) {
			log.Printf("[VALIDATOR:%s] round %d: ❌ INVALID coordinator signature — rejecting proposal",
				v.nodeID, msg.RoundID)
			continue
		}

		// 3. Sign and vote
		mySig := ed25519.Sign(v.privKey, hashBytes)
		if err := SendMessage(conn, &Message{
			Type:      MsgVote,
			RoundID:   msg.RoundID,
			NodeID:    v.nodeID,
			Signature: base64.StdEncoding.EncodeToString(mySig),
		}); err != nil {
			log.Printf("[VALIDATOR:%s] round %d: failed to send vote: %v", v.nodeID, msg.RoundID, err)
			continue
		}
		log.Printf("[VALIDATOR:%s] round %d: ✅ voted for block %s…", v.nodeID, msg.RoundID, msg.BlockHash[:16])
	}
}
