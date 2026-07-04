# PFF Go Codebase Guide

Welcome to Go! Go is a fantastic language for distributed systems because it was built specifically for high concurrency (doing many things at once) and networking. 

This guide breaks down exactly how your PFF codebase works, file by file.

---

## 1. `main.go` (The Entry Point)
In Go, execution always starts in the `main()` function inside `package main`. 

```go
func main() {
    mode := flag.String("mode", "", "coordinator | validator | keygen")
    // ... other flags ...
    flag.Parse()
```
We use the `flag` standard library to read command-line arguments (like `-mode=coordinator`). 
Based on what mode you type in the terminal, the `switch *mode` statement decides which part of the program to run:
*   **`keygen`**: Calls `RunKeygen()` to generate keys.
*   **`coordinator`**: Creates a new `Coordinator` object and runs it.
*   **`validator`**: Creates a new `Validator` object and runs it.

---

## 2. `network.go` (How they talk to each other)
When nodes send data over TCP, it's just a raw stream of bytes. If the Coordinator sends two messages instantly, they can blur together. We prevent this using a technique called **Length-Prefixed Framing**.

```go
func SendMessage(conn net.Conn, msg *Message) error {
	data, err := json.Marshal(msg)
    // 1. We turn the Go Message struct into JSON text.
    
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
    // 2. We calculate exactly how many bytes the JSON is, and write that number into a tiny 4-byte header.
    
	conn.Write(lenBuf[:]) // Send the 4-byte size first
	conn.Write(data)      // Send the actual JSON payload next
}
```
When `ReceiveMessage` reads from the network, it reads exactly 4 bytes first. It looks at that number (e.g., 250 bytes), and then knows exactly how many remaining bytes to read to get a complete message. This prevents "chopped" or "merged" packets.

---

## 3. `coordinator.go` (The Brain)
This is where the magic of PFF happens. Go uses "Goroutines" (lightweight threads created by typing `go func()`) and "Channels" (pipes to send data between threads) to handle concurrency.

### The Fast-Path Race
In `runRound()`, the Coordinator needs to broadcast a block to all 4 validators at the exact same time, and race to collect 90% of the signatures before 300ms.

```go
	voteCh := make(chan vote, total) // Create a pipe for votes
	start := time.Now() // Start the stopwatch

	// For each validator connection...
	for nid, conn := range conns {
        // ...spin up a background thread (Goroutine)
		go func(id string, nc net.Conn) {
			SendMessage(nc, propose)    // Send the block
			resp, _ := ReceiveMessage(nc) // Wait for their signature
            // Verify signature math...
			voteCh <- vote{nodeID: id, sig: sigBytes} // Send valid vote down the pipe
		}(nid, conn)
	}
```

Now, the main thread waits at the finish line using a `select` statement. A `select` statement is like a race condition where whichever case happens first wins:

```go
	deadline := time.After(timeout) // A timer that fires after 300ms

    // An infinite loop watching the race
	for {
		select {
		case v := <-voteCh:
            // Someone sent a vote down the pipe! 
			votes = append(votes, v)
			if len(votes) >= pffNeeded {
                // We hit 90%! Break the loop, trigger FAST_PFF
				break collect 
			}
		case <-deadline:
            // 300ms passed! The timeout pipe triggered first.
            // Break the loop, trigger FALLBACK_BFT
			break collect 
		}
	}
```
This is why PFF is so fast. It's not waiting for Validator 1, then Validator 2. It asks them all at once, and the moment 90% of the votes drop into the bucket, it instantly moves on.

---

## 4. `validator.go` (The Listener)
The Validator code is much simpler. It just sits in an infinite `for {}` loop waiting for the Coordinator to ask it something.

```go
	for {
        // 1. Wait for a message (sleeps until data arrives)
		msg, err := ReceiveMessage(conn)

		// 2. Decode the block hash
		hashBytes, _ := hex.DecodeString(msg.BlockHash)

		// 3. Verify the Coordinator actually sent this (Cryptography)
		if !ed25519.Verify(v.coordPubKey, hashBytes, coordSigBytes) {
			continue // Fake message, ignore it
		}

		// 4. Sign the block hash with our private key
		mySig := ed25519.Sign(v.privKey, hashBytes)

        // 5. Send it back!
		SendMessage(conn, &Message{ Type: MsgVote, Signature: mySig })
	}
```

---

## 5. `config.go` (Cryptography)
This file just loads the `cluster_config.json`. We use the `crypto/ed25519` library for signing. Ed25519 is an incredibly fast, modern elliptical curve cryptography standard used heavily in modern blockchains. We store the keys as `base64` text in the JSON so they are easy to read and copy/paste.
