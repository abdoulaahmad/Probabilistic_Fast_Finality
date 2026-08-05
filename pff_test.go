package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// ── Quorum ───────────────────────────────────────────────────────────────────

func TestQuorumSize(t *testing.T) {
	cases := []struct {
		validators int
		threshold  float64
		want       int
	}{
		// The historical 4-validator / k=0.90 case. Ceiling gives 4 here too
		// (ceil(3.6) == 4), but now by intent rather than by rounding accident.
		{4, 0.90, 4},
		{10, 0.90, 9},
		{5, 0.90, 5}, // ceil(4.5) == 5
		{100, 0.90, 90},
		// Float representation: 10*0.7 is 6.999999999999999, must not yield 8.
		{10, 0.70, 7},
		{3, 0.67, 3}, // ceil(2.01) == 3
		{1, 0.90, 1},
		{4, 1.0, 4},
		// Never below 1, never above the validator count.
		{10, 0.01, 1},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("n=%d/k=%.2f", tc.validators, tc.threshold)
		t.Run(name, func(t *testing.T) {
			cfg := &ClusterConfig{PFFThreshold: tc.threshold}
			cfg.Nodes = append(cfg.Nodes, NodeConfig{ID: "node1", Role: "coordinator"})
			for i := 0; i < tc.validators; i++ {
				cfg.Nodes = append(cfg.Nodes, NodeConfig{
					ID:   fmt.Sprintf("v%d", i),
					Role: "validator",
				})
			}
			c := NewCoordinator(cfg, 1, "test")
			if got := c.quorumSize(); got != tc.want {
				t.Errorf("quorumSize() = %d, want %d", got, tc.want)
			}
		})
	}
}

// Quorum must depend on the configured cluster size, not on how many peers are
// currently connected. If it tracked live connections, losing validators would
// silently lower the bar — 2 of 4 remaining would clear "90%" at 50% of the
// cluster.
func TestQuorumIgnoresConnectedPeers(t *testing.T) {
	cfg := &ClusterConfig{PFFThreshold: 0.90}
	cfg.Nodes = append(cfg.Nodes, NodeConfig{ID: "node1", Role: "coordinator"})
	for i := 0; i < 10; i++ {
		cfg.Nodes = append(cfg.Nodes, NodeConfig{ID: fmt.Sprintf("v%d", i), Role: "validator"})
	}

	c := NewCoordinator(cfg, 1, "test")
	before := c.quorumSize()

	// Simulate 8 of 10 validators dropping.
	c.peers["v0"] = &peer{nodeID: "v0"}
	c.peers["v1"] = &peer{nodeID: "v1"}

	if after := c.quorumSize(); after != before {
		t.Errorf("quorum changed with peer count: %d → %d; must stay %d", before, after, before)
	}
	if before != 9 {
		t.Errorf("quorum = %d, want 9 (90%% of the configured 10)", before)
	}
}

// ── Digests ──────────────────────────────────────────────────────────────────

// The whole point of binding the round into the digest: the same block hash in
// a different round must produce a different signing target.
func TestDigestsBindRound(t *testing.T) {
	hash := make([]byte, 32)

	if string(ProposeDigest(1, hash)) == string(ProposeDigest(2, hash)) {
		t.Error("ProposeDigest is identical across rounds — proposals are replayable")
	}
	if string(VoteDigest(1, hash, "node2")) == string(VoteDigest(2, hash, "node2")) {
		t.Error("VoteDigest is identical across rounds — votes are replayable")
	}
}

// A vote must be attributable to exactly one validator.
func TestVoteDigestBindsVoter(t *testing.T) {
	hash := make([]byte, 32)
	if string(VoteDigest(1, hash, "node2")) == string(VoteDigest(1, hash, "node3")) {
		t.Error("VoteDigest ignores node ID — one validator's vote could count as another's")
	}
}

// Domain separation: the same inputs under different tags must not collide, so
// an auth signature can never be replayed as a vote.
func TestDomainSeparation(t *testing.T) {
	hash := make([]byte, 32)
	seen := map[string]string{
		string(ProposeDigest(1, hash)): "propose",
		string(VoteDigest(1, hash, "node2")): "vote",
		string(AuthDigest("node2", hash)):    "auth",
	}
	if len(seen) != 3 {
		t.Errorf("digest collision across domains: got %d distinct values, want 3", len(seen))
	}
}

// Length-prefixing each part must make the encoding unambiguous.
func TestDigestCanonicalEncoding(t *testing.T) {
	a := digest("d", []byte("ab"), []byte("c"))
	b := digest("d", []byte("a"), []byte("bc"))
	if string(a) == string(b) {
		t.Error(`digest("ab","c") == digest("a","bc") — parts are not length-prefixed`)
	}
}

// ── Framing ──────────────────────────────────────────────────────────────────

func TestSendReceiveRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	want := &Message{
		Type:      MsgVote,
		RoundID:   42,
		NodeID:    "node3",
		BlockHash: "deadbeef",
		Signature: "c2ln",
	}

	go func() { SendMessage(client, want) }()

	got, err := ReceiveMessage(server)
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if got.Type != want.Type || got.RoundID != want.RoundID ||
		got.NodeID != want.NodeID || got.BlockHash != want.BlockHash ||
		got.Signature != want.Signature {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestReceiveRejectsOversizedFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Announce a 2 MB body, over the 1 MB cap.
	go func() { client.Write([]byte{0x00, 0x20, 0x00, 0x00}) }()

	if _, err := ReceiveMessage(server); err == nil {
		t.Error("expected an error for an oversized frame, got nil")
	}
}

// Several frames written back to back must be read back individually and in
// order — this is what keeps a peer's stream aligned across rounds.
func TestFramesStayAlignedBackToBack(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	const n = 5
	go func() {
		for i := 1; i <= n; i++ {
			SendMessage(client, &Message{Type: MsgVote, RoundID: i})
		}
	}()

	for i := 1; i <= n; i++ {
		got, err := ReceiveMessage(server)
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got.RoundID != i {
			t.Fatalf("message %d: RoundID = %d, want %d (stream misaligned)", i, got.RoundID, i)
		}
	}
}

// ── Config ───────────────────────────────────────────────────────────────────

func TestConfigValidation(t *testing.T) {
	valid := func() *ClusterConfig {
		return &ClusterConfig{
			Nodes: []NodeConfig{
				{ID: "node1", IP: "127.0.0.1", Port: 8080, Role: "coordinator", PublicKey: testPubKey(t)},
				{ID: "node2", IP: "127.0.0.1", Port: 8080, Role: "validator", PublicKey: testPubKey(t)},
			},
			PFFThreshold:  0.9,
			BFTThreshold:  0.67,
			FastTimeoutMS: 300,
		}
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []struct {
		name  string
		mutate func(*ClusterConfig)
	}{
		{"no coordinator", func(c *ClusterConfig) { c.Nodes[0].Role = "validator" }},
		{"two coordinators", func(c *ClusterConfig) { c.Nodes[1].Role = "coordinator" }},
		{"no validators", func(c *ClusterConfig) { c.Nodes = c.Nodes[:1] }},
		{"duplicate id", func(c *ClusterConfig) { c.Nodes[1].ID = "node1" }},
		{"unknown role", func(c *ClusterConfig) { c.Nodes[1].Role = "observer" }},
		{"bad port", func(c *ClusterConfig) { c.Nodes[1].Port = 0 }},
		{"threshold zero", func(c *ClusterConfig) { c.PFFThreshold = 0 }},
		{"threshold above one", func(c *ClusterConfig) { c.PFFThreshold = 1.5 }},
		{"malformed public key", func(c *ClusterConfig) { c.Nodes[1].PublicKey = "!!!" }},
		{"short public key", func(c *ClusterConfig) {
			c.Nodes[1].PublicKey = base64.StdEncoding.EncodeToString([]byte("too short"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected %s to be rejected, got nil", tc.name)
			}
		})
	}
}

// Deploying the wrong node's key file should fail loudly at startup rather than
// as an unexplained stream of invalid signatures later.
func TestLoadPrivateKeyDetectsMismatch(t *testing.T) {
	dir := t.TempDir()

	pub1, _, _ := ed25519.GenerateKey(nil)
	_, priv2, _ := ed25519.GenerateKey(nil)

	node := &NodeConfig{ID: "node2", PublicKey: base64.StdEncoding.EncodeToString(pub1)}
	writeKey(t, dir, "node2", priv2)

	if _, err := LoadPrivateKey(dir, node); err == nil {
		t.Error("expected a mismatch error when the key file belongs to another node")
	}
}

func TestLoadPrivateKeyAcceptsMatching(t *testing.T) {
	dir := t.TempDir()

	pub, priv, _ := ed25519.GenerateKey(nil)
	node := &NodeConfig{ID: "node2", PublicKey: base64.StdEncoding.EncodeToString(pub)}
	writeKey(t, dir, "node2", priv)

	got, err := LoadPrivateKey(dir, node)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if !got.Equal(priv) {
		t.Error("loaded key does not match the one written")
	}
}

// Keygen must never place a private key inside the shared config.
func TestKeygenKeepsPrivateKeysOutOfConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cluster_config.json")
	keyDir := filepath.Join(dir, "keys")

	specs := []string{
		"node1:127.0.0.1:8080:coordinator",
		"node2:127.0.0.1:8081:validator",
	}
	if err := RunKeygen(specs, keyDir, cfgPath); err != nil {
		t.Fatalf("RunKeygen: %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}

	// No "private" anywhere in the shared file, in any field.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	for _, n := range generic["nodes"].([]any) {
		for k := range n.(map[string]any) {
			if k == "private_key" {
				t.Error("cluster_config.json contains a private_key field")
			}
		}
	}

	// Each node has its own key file, and it matches that node's public key.
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig on generated file: %v", err)
	}
	for i := range cfg.Nodes {
		if _, err := LoadPrivateKey(keyDir, &cfg.Nodes[i]); err != nil {
			t.Errorf("node %s: %v", cfg.Nodes[i].ID, err)
		}
	}
}

// ── End to end ───────────────────────────────────────────────────────────────

// A full run over real TCP on loopback: keygen, coordinator, validators, and a
// results.csv that reports every round on the fast path.
func TestEndToEndFastPath(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	const validators = 4
	cfg := generateCluster(t, dir, validators)

	const rounds = 5
	coord := NewCoordinator(cfg, rounds, "TestSuite")

	errCh := make(chan error, 1)
	go func() { errCh <- coord.Run("keys") }()

	for _, v := range cfg.GetValidators() {
		val, err := NewValidator(cfg, v.ID, "keys")
		if err != nil {
			t.Fatalf("NewValidator(%s): %v", v.ID, err)
		}
		go val.Run()
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("coordinator: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish within 60s")
	}

	rows := readResults(t, filepath.Join(dir, "results.csv"))
	if len(rows) != rounds {
		t.Fatalf("got %d result rows, want %d", len(rows), rounds)
	}

	for _, row := range rows {
		if row["PathTaken"] != "FAST_PFF" {
			t.Errorf("round %s: path = %s, want FAST_PFF (sigs=%s, need=%s)",
				row["RoundID"], row["PathTaken"],
				row["SignaturesReceived"], row["QuorumNeeded"])
		}
		if row["SignaturesReceived"] != strconv.Itoa(validators) {
			t.Errorf("round %s: got %s signatures, want %d",
				row["RoundID"], row["SignaturesReceived"], validators)
		}
	}
}

// Regression test for the desync bug.
//
// The old coordinator started a fresh reader with a 300 ms deadline each round.
// A vote that arrived late stayed in the socket buffer, was consumed by the
// NEXT round's reader, rejected as stale, and left the connection permanently
// one message behind — so a validator that was slow once never scored again.
//
// Here one validator is slow enough to miss the first round's deadline. Round 1
// legitimately falls back, but the later rounds must recover. Under the old
// design every subsequent round stayed stuck at 3 of 4 votes.
func TestSlowValidatorDoesNotDesyncStream(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	const validators = 4
	cfg := generateCluster(t, dir, validators)
	cfg.FastTimeoutMS = 200

	const rounds = 6
	coord := NewCoordinator(cfg, rounds, "SlowPeer")

	errCh := make(chan error, 1)
	go func() { errCh <- coord.Run("keys") }()

	vs := cfg.GetValidators()
	for i, v := range vs {
		val, err := NewValidator(cfg, v.ID, "keys")
		if err != nil {
			t.Fatalf("NewValidator(%s): %v", v.ID, err)
		}
		// The last validator stalls past round 1's deadline, then behaves.
		if i == len(vs)-1 {
			go func() {
				time.Sleep(400 * time.Millisecond)
				val.Run()
			}()
			continue
		}
		go val.Run()
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("coordinator: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish within 60s")
	}

	rows := readResults(t, filepath.Join(dir, "results.csv"))
	if len(rows) != rounds {
		t.Fatalf("got %d result rows, want %d", len(rows), rounds)
	}

	// The final round is the one that matters: the slow validator must be
	// contributing again by then.
	last := rows[len(rows)-1]
	if last["SignaturesReceived"] != strconv.Itoa(validators) {
		t.Errorf("final round collected %s/%d signatures — the slow validator never recovered "+
			"(stream desync regression)", last["SignaturesReceived"], validators)
	}
	if last["PathTaken"] != "FAST_PFF" {
		t.Errorf("final round path = %s, want FAST_PFF after recovery", last["PathTaken"])
	}
}

// An unauthenticated client must not be able to claim a validator's identity.
func TestHandshakeRejectsForgedIdentity(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := generateCluster(t, dir, 2)
	coordNode := cfg.GetCoordinator()

	coord := NewCoordinator(cfg, 1, "Auth")
	privKey, err := LoadPrivateKey("keys", coordNode)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	coord.privKey = privKey

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", coordNode.Port))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go coord.acceptLoop(ln)

	victim := cfg.GetValidators()[0].ID

	t.Run("wrong key", func(t *testing.T) {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		if err := SendMessage(conn, &Message{Type: MsgConnect, NodeID: victim}); err != nil {
			t.Fatalf("send CONNECT: %v", err)
		}
		challenge, err := ReceiveMessage(conn)
		if err != nil {
			t.Fatalf("read CHALLENGE: %v", err)
		}
		nonce, _ := base64.StdEncoding.DecodeString(challenge.Nonce)

		// Attacker signs with a key that is not the victim's.
		_, attackerKey, _ := ed25519.GenerateKey(nil)
		sig := ed25519.Sign(attackerKey, AuthDigest(victim, nonce))
		SendMessage(conn, &Message{
			Type:      MsgAuth,
			NodeID:    victim,
			Signature: base64.StdEncoding.EncodeToString(sig),
		})

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := ReceiveMessage(conn); err == nil {
			t.Error("forged handshake was accepted")
		}

		coord.mu.RLock()
		n := len(coord.peers)
		coord.mu.RUnlock()
		if n != 0 {
			t.Errorf("forged client registered as a peer (%d peers)", n)
		}
	})

	t.Run("unknown node id", func(t *testing.T) {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		SendMessage(conn, &Message{Type: MsgConnect, NodeID: "not-in-config"})
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := ReceiveMessage(conn); err == nil {
			t.Error("unknown node id was accepted")
		}
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func testPubKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pub)
}

func writeKey(t *testing.T, dir, nodeID string, priv ed25519.PrivateKey) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	encoded := base64.StdEncoding.EncodeToString(priv) + "\n"
	if err := os.WriteFile(KeyPath(dir, nodeID), []byte(encoded), 0600); err != nil {
		t.Fatalf("writing key for %s: %v", nodeID, err)
	}
}

// chdir switches to dir for the duration of the test. Coordinator.Run writes
// results.csv relative to the working directory.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// generateCluster creates a coordinator plus n validators on free loopback
// ports, writes the config and key files into dir, and returns the loaded config.
func generateCluster(t *testing.T, dir string, n int) *ClusterConfig {
	t.Helper()

	specs := []string{fmt.Sprintf("node1:127.0.0.1:%d:coordinator", freePort(t))}
	for i := 2; i <= n+1; i++ {
		specs = append(specs, fmt.Sprintf("node%d:127.0.0.1:%d:validator", i, freePort(t)))
	}

	cfgPath := filepath.Join(dir, "cluster_config.json")
	if err := RunKeygen(specs, filepath.Join(dir, "keys"), cfgPath); err != nil {
		t.Fatalf("RunKeygen: %v", err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// freePort asks the OS for an unused port. There is an inherent race between
// closing and rebinding, but it is not a problem in practice for these tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// readResults parses results.csv into a slice of column→value maps.
func readResults(t *testing.T, path string) []map[string]string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if len(records) < 1 {
		t.Fatalf("%s is empty", path)
	}

	header := records[0]
	var out []map[string]string
	for _, rec := range records[1:] {
		row := make(map[string]string, len(header))
		for i, col := range header {
			if i < len(rec) {
				row[col] = rec[i]
			}
		}
		out = append(out, row)
	}
	return out
}
