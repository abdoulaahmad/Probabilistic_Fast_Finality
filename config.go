package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
)

// ── Protocol constants ──────────────────────────────────────────────────────

const (
	PFFThresholdDefault = 0.90 // 90% super-majority
	BFTThresholdDefault = 0.67 // 67% BFT threshold
	FastTimeoutMS       = 300  // PFF express-lane timeout (ms)
	BFTDelayMS          = 150  // Simulated BFT fallback processing delay (ms)
)

// ── Config structs ──────────────────────────────────────────────────────────

type NodeConfig struct {
	ID         string `json:"id"`
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	Role       string `json:"role"` // "coordinator" | "validator"
	PublicKey  string `json:"public_key"`            // base64 Ed25519 public key
	PrivateKey string `json:"private_key,omitempty"` // base64 Ed25519 private key
}

type ClusterConfig struct {
	Nodes         []NodeConfig `json:"nodes"`
	PFFThreshold  float64      `json:"pff_threshold"`
	BFTThreshold  float64      `json:"bft_threshold"`
	FastTimeoutMS int          `json:"fast_timeout_ms"`
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
	return &cfg, nil
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
	return ed25519.PublicKey(b), nil
}

func (n *NodeConfig) GetPrivateKey() (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(n.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decoding private key for %s: %w", n.ID, err)
	}
	return ed25519.PrivateKey(b), nil
}

// ── Keygen ──────────────────────────────────────────────────────────────────

// RunKeygen generates Ed25519 keypairs for all 5 nodes and writes cluster_config.json.
// Replace the placeholder IPs with your actual VPS public IPs before deploying.
func RunKeygen() error {
	type nodeDef struct {
		id   string
		ip   string
		port int
		role string
	}

	nodes := []nodeDef{
		{"node1", "170.64.167.115", 8080, "coordinator"},
		{"node2", "178.128.30.62", 8080, "validator"},
		{"node3", "64.227.72.253", 8080, "validator"},
		{"node4", "104.248.123.80", 8080, "validator"},
		{"node5", "46.101.48.181", 8080, "validator"},
	}

	cfg := ClusterConfig{
		PFFThreshold:  PFFThresholdDefault,
		BFTThreshold:  BFTThresholdDefault,
		FastTimeoutMS: FastTimeoutMS,
	}

	for _, n := range nodes {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return fmt.Errorf("generating key for %s: %w", n.id, err)
		}
		cfg.Nodes = append(cfg.Nodes, NodeConfig{
			ID:         n.id,
			IP:         n.ip,
			Port:       n.port,
			Role:       n.role,
			PublicKey:  base64.StdEncoding.EncodeToString(pub),
			PrivateKey: base64.StdEncoding.EncodeToString(priv),
		})
		log.Printf("[KEYGEN] ✅ Keypair generated for %-8s (%s)", n.id, n.role)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile("cluster_config.json", data, 0600); err != nil {
		return fmt.Errorf("writing cluster_config.json: %w", err)
	}
	log.Println("[KEYGEN] 📄 cluster_config.json written.")
	log.Println("[KEYGEN] ⚠️  IMPORTANT: Replace placeholder IPs with your real VPS IPs before deploying!")
	return nil
}
