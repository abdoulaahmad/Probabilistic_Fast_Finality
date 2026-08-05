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
	MsgConnect   MessageType = "CONNECT"   // validator → coordinator: register
	MsgChallenge MessageType = "CHALLENGE" // coordinator → validator: auth nonce
	MsgAuth      MessageType = "AUTH"      // validator → coordinator: signed nonce
	MsgAccepted  MessageType = "ACCEPTED"  // coordinator → validator: handshake ok
	MsgPropose   MessageType = "PROPOSE"   // coordinator → validator: new block
	MsgVote      MessageType = "VOTE"      // validator → coordinator: signed vote
)

// maxMessageBytes bounds a single frame to prevent memory exhaustion.
const maxMessageBytes = 1 << 20 // 1 MB

// Message is the shared wire format for all PFF protocol messages.
// Unused fields are omitted from JSON.
type Message struct {
	Type      MessageType `json:"type"`
	RoundID   int         `json:"round_id,omitempty"`
	NodeID    string      `json:"node_id,omitempty"`
	BlockHash string      `json:"block_hash,omitempty"` // hex-encoded SHA-256
	Signature string      `json:"signature,omitempty"`  // base64 Ed25519 signature
	Nonce     string      `json:"nonce,omitempty"`      // base64 handshake challenge
	TestSuite string      `json:"test_suite,omitempty"` // e.g. "A3_Baseline", "C1_Jitter"
	Reason    string      `json:"reason,omitempty"`     // rejection detail, if any
}

// SendMessage serialises msg as JSON and writes it with a 4-byte big-endian
// length prefix so the receiver knows exactly how many bytes to read.
//
// The prefix and body are written in a single conn.Write call. A short frame
// followed by a separate body write can interleave with a concurrent writer on
// the same connection and corrupt the stream; one write keeps each frame
// atomic with respect to other writers.
func SendMessage(conn net.Conn, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	if len(data) > maxMessageBytes {
		return fmt.Errorf("message too large: %d bytes", len(data))
	}

	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)

	if _, err := conn.Write(frame); err != nil {
		return fmt.Errorf("writing message: %w", err)
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
	if length == 0 || length > maxMessageBytes {
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
