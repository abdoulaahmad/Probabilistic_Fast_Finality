package main

import (
	"crypto/sha256"
	"encoding/binary"
)

// Domain separation tags. Every signature in the protocol commits to exactly
// one of these, so a signature produced for one purpose can never be replayed
// as a signature for another (e.g. an auth response reused as a vote).
const (
	domainPropose = "PFF-v1:propose"
	domainVote    = "PFF-v1:vote"
	domainAuth    = "PFF-v1:auth"
)

// digest builds an unambiguous SHA-256 over a domain tag plus a list of parts.
// Each part is length-prefixed so that no two distinct part lists can produce
// the same byte stream (canonical encoding — prevents concatenation attacks
// such as ("ab","c") colliding with ("a","bc")).
func digest(domain string, parts ...[]byte) []byte {
	h := sha256.New()

	var lb [4]byte
	writePart := func(b []byte) {
		binary.BigEndian.PutUint32(lb[:], uint32(len(b)))
		h.Write(lb[:])
		h.Write(b)
	}

	writePart([]byte(domain))
	for _, p := range parts {
		writePart(p)
	}
	return h.Sum(nil)
}

// roundBytes encodes a round ID as 8 big-endian bytes.
func roundBytes(roundID int) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(roundID))
	return b[:]
}

// ProposeDigest is what the coordinator signs when proposing a block, and what
// each validator verifies before voting. Binding roundID into the digest means
// a captured proposal cannot be replayed under a different round number.
func ProposeDigest(roundID int, blockHash []byte) []byte {
	return digest(domainPropose, roundBytes(roundID), blockHash)
}

// VoteDigest is what a validator signs when voting, and what the coordinator
// verifies before counting the vote. It binds both the round and the voter, so
// one validator's vote cannot be replayed as another's, or into another round.
func VoteDigest(roundID int, blockHash []byte, nodeID string) []byte {
	return digest(domainVote, roundBytes(roundID), blockHash, []byte(nodeID))
}

// AuthDigest is what a validator signs to prove key ownership during the
// handshake. The nonce is chosen freshly by the coordinator for each attempt,
// so a recorded auth response cannot be replayed by an impostor.
func AuthDigest(nodeID string, nonce []byte) []byte {
	return digest(domainAuth, []byte(nodeID), nonce)
}
