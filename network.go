package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// MessageType identifies the type of a PFF protocol message.
type MessageType string

const (
	MsgConnect MessageType = "CONNECT" // validator → coordinator: register
	MsgPropose MessageType = "PROPOSE" // coordinator → validator: new block
	MsgVote    MessageType = "VOTE"    // validator → coordinator: signed vote
)

// Message is the shared wire format for all PFF protocol messages.
// Unused fields are omitted from JSON.
type Message struct {
	Type      MessageType `json:"type"`
	RoundID   int         `json:"round_id,omitempty"`
	NodeID    string      `json:"node_id,omitempty"`
	BlockHash string      `json:"block_hash,omitempty"` // hex-encoded SHA-256
	Signature string      `json:"signature,omitempty"`  // base64 Ed25519 signature
	TestSuite string      `json:"test_suite,omitempty"` // e.g. "A3_Baseline", "C1_Jitter"
}

// SendMessage serialises msg as JSON and writes it with a 4-byte big-endian
// length prefix so the receiver knows exactly how many bytes to read.
func SendMessage(conn net.Conn, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("writing length prefix: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("writing message body: %w", err)
	}
	return nil
}

// ReceiveMessage reads a length-prefixed JSON message from conn.
func ReceiveMessage(conn net.Conn) (*Message, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("reading length prefix: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 || length > 1<<20 { // guard: 0 B – 1 MB
		return nil, fmt.Errorf("invalid message length: %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, fmt.Errorf("reading message body: %w", err)
	}
	var msg Message
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, fmt.Errorf("unmarshaling message: %w", err)
	}
	return &msg, nil
}
