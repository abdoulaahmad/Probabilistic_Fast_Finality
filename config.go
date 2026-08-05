package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ── Protocol constants ──────────────────────────────────────────────────────

const (
	PFFThresholdDefault = 0.90 // 90% super-majority
	BFTThresholdDefault = 0.67 // 67% BFT threshold
	FastTimeoutMS       = 300  // PFF express-lane timeout (ms)
	BFTDelayMS          = 150  // Simulated BFT fallback processing delay (ms)
)

// KeyDirDefault is where per-node private keys live. Each node keeps only its
// own file; the shared cluster_config.json holds public keys exclusively.
const KeyDirDefault = "keys"

// ── Config structs ──────────────────────────────────────────────────────────

type NodeConfig struct {
	ID        string `json:"id"`
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	Role      string `json:"role"`       // "coordinator" | "validator"
	PublicKey string `json:"public_key"` // base64 Ed25519 public key
}

type ClusterConfig struct {
	Nodes         []NodeConfig `json:"nodes"`
	PFFThreshold  float64      `json:"pff_threshold"`
	BFTThreshold  float64      `json:"bft_threshold"`
	FastTimeoutMS int          `json:"fast_timeout_ms"`
	BFTDelayMS    int          `json:"bft_delay_ms"`
}

// ── Config helpers ──────────────────────────────────────────────────────────

func LoadConfig(path string) (*ClusterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}
	var cfg ClusterConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	// Apply defaults when zero-valued
	if cfg.PFFThreshold == 0 {
		cfg.PFFThreshold = PFFThresholdDefault
	}
	if cfg.BFTThreshold == 0 {
		cfg.BFTThreshold = BFTThresholdDefault
	}
	if cfg.FastTimeoutMS == 0 {
		cfg.FastTimeoutMS = FastTimeoutMS
	}
	if cfg.BFTDelayMS == 0 {
		cfg.BFTDelayMS = BFTDelayMS
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate rejects configs that would misbehave at runtime rather than letting
// the error surface mid-experiment as a confusing consensus failure.
func (cfg *ClusterConfig) Validate() error {
	if len(cfg.Nodes) == 0 {
		return fmt.Errorf("config contains no nodes")
	}

	seen := make(map[string]bool, len(cfg.Nodes))
	coordinators := 0

	for _, n := range cfg.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node with empty id")
		}
		if seen[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		seen[n.ID] = true

		switch n.Role {
		case "coordinator":
			coordinators++
		case "validator":
		default:
			return fmt.Errorf("node %s: unknown role %q", n.ID, n.Role)
		}

		if n.Port <= 0 || n.Port > 65535 {
			return fmt.Errorf("node %s: invalid port %d", n.ID, n.Port)
		}

		pub, err := n.GetPublicKey()
		if err != nil {
			return err
		}
		if len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("node %s: public key is %d bytes, want %d",
				n.ID, len(pub), ed25519.PublicKeySize)
		}
	}

	if coordinators != 1 {
		return fmt.Errorf("config must define exactly 1 coordinator, found %d", coordinators)
	}
	if len(cfg.GetValidators()) == 0 {
		return fmt.Errorf("config must define at least 1 validator")
	}
	if cfg.PFFThreshold <= 0 || cfg.PFFThreshold > 1 {
		return fmt.Errorf("pff_threshold must be in (0, 1], got %g", cfg.PFFThreshold)
	}
	if cfg.BFTThreshold <= 0 || cfg.BFTThreshold > 1 {
		return fmt.Errorf("bft_threshold must be in (0, 1], got %g", cfg.BFTThreshold)
	}
	if cfg.FastTimeoutMS <= 0 {
		return fmt.Errorf("fast_timeout_ms must be positive, got %d", cfg.FastTimeoutMS)
	}
	if cfg.BFTDelayMS < 0 {
		return fmt.Errorf("bft_delay_ms must not be negative, got %d", cfg.BFTDelayMS)
	}
	return nil
}

func (cfg *ClusterConfig) GetCoordinator() *NodeConfig {
	for i := range cfg.Nodes {
		if cfg.Nodes[i].Role == "coordinator" {
			return &cfg.Nodes[i]
		}
	}
	return nil
}

func (cfg *ClusterConfig) GetValidators() []NodeConfig {
	var out []NodeConfig
	for _, n := range cfg.Nodes {
		if n.Role == "validator" {
			out = append(out, n)
		}
	}
	return out
}

func (cfg *ClusterConfig) GetNodeByID(id string) *NodeConfig {
	for i := range cfg.Nodes {
		if cfg.Nodes[i].ID == id {
			return &cfg.Nodes[i]
		}
	}
	return nil
}

// ── Key helpers ─────────────────────────────────────────────────────────────

func (n *NodeConfig) GetPublicKey() (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(n.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decoding public key for %s: %w", n.ID, err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key for %s is %d bytes, want %d",
			n.ID, len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// KeyPath returns the conventional private-key location for a node.
func KeyPath(keyDir, nodeID string) string {
	return filepath.Join(keyDir, nodeID+".key")
}

// LoadPrivateKey reads a node's own private key from disk and checks it against
// the public key in the shared config. The cross-check catches the common
// deployment mistake of copying the wrong node's key file, which would
// otherwise fail later as an unexplained stream of invalid signatures.
func LoadPrivateKey(keyDir string, node *NodeConfig) (ed25519.PrivateKey, error) {
	path := KeyPath(keyDir, node.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading private key %q for %s: %w", path, node.ID, err)
	}

	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("decoding private key %q: %w", path, err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key %q is %d bytes, want %d",
			path, len(b), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(b)

	wantPub, err := node.GetPublicKey()
	if err != nil {
		return nil, err
	}
	if !priv.Public().(ed25519.PublicKey).Equal(wantPub) {
		return nil, fmt.Errorf(
			"private key %q does not match the public key for %s in the cluster config "+
				"(wrong key file deployed to this node?)", path, node.ID)
	}
	return priv, nil
}

// ── Keygen ──────────────────────────────────────────────────────────────────

// RunKeygen generates an Ed25519 keypair per node. It writes a shared
// cluster_config.json containing only public keys, plus one private key file
// per node under keyDir.
//
// Distribute cluster_config.json to every node, but give each node only its own
// keys/<id>.key. No node should ever hold another node's private key — otherwise
// any node can forge any other node's vote and signature verification proves
// nothing about who actually voted.
func RunKeygen(specs []string, keyDir, outPath string) error {
	nodes, err := parseNodeSpecs(specs)
	if err != nil {
		return err
	}

	cfg := ClusterConfig{
		PFFThreshold:  PFFThresholdDefault,
		BFTThreshold:  BFTThresholdDefault,
		FastTimeoutMS: FastTimeoutMS,
		BFTDelayMS:    BFTDelayMS,
	}

	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("creating key dir %q: %w", keyDir, err)
	}

	for _, n := range nodes {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return fmt.Errorf("generating key for %s: %w", n.ID, err)
		}

		n.PublicKey = base64.StdEncoding.EncodeToString(pub)
		cfg.Nodes = append(cfg.Nodes, n)

		keyPath := KeyPath(keyDir, n.ID)
		encoded := base64.StdEncoding.EncodeToString(priv) + "\n"
		if err := os.WriteFile(keyPath, []byte(encoded), 0600); err != nil {
			return fmt.Errorf("writing private key %q: %w", keyPath, err)
		}
		log.Printf("[KEYGEN] Keypair generated for %-8s (%-11s) → %s", n.ID, n.Role, keyPath)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("generated config is invalid: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	log.Printf("[KEYGEN] %s written (public keys only — safe to distribute).", outPath)
	log.Printf("[KEYGEN] Private keys are in %s/ — copy ONLY <node-id>.key to its own node.", keyDir)
	return nil
}

// parseNodeSpecs converts "id:ip:port:role" strings into NodeConfigs.
func parseNodeSpecs(specs []string) ([]NodeConfig, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("no nodes specified: pass -nodes id:ip:port:role,...")
	}

	var out []NodeConfig
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		parts := strings.Split(spec, ":")
		if len(parts) != 4 {
			return nil, fmt.Errorf("bad node spec %q: want id:ip:port:role", spec)
		}
		port := 0
		if _, err := fmt.Sscanf(parts[2], "%d", &port); err != nil {
			return nil, fmt.Errorf("bad port in node spec %q: %w", spec, err)
		}
		out = append(out, NodeConfig{
			ID:   strings.TrimSpace(parts[0]),
			IP:   strings.TrimSpace(parts[1]),
			Port: port,
			Role: strings.TrimSpace(parts[3]),
		})
	}
	return out, nil
}
