package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	mode := flag.String("mode", "", "coordinator | validator | keygen")
	nodeID := flag.String("node-id", "", "Node ID for validator mode (e.g. node2)")
	rounds := flag.Int("rounds", 100, "Number of consensus rounds (coordinator)")
	suite := flag.String("suite", "A3_Baseline", "TestSuite label written to results.csv")
	cfgPath := flag.String("config", "cluster_config.json", "Path to cluster config")
	keyDir := flag.String("key-dir", KeyDirDefault, "Directory holding this node's private key")
	nodes := flag.String("nodes", "", "keygen: comma-separated id:ip:port:role specs")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	switch *mode {

	case "keygen":
		if *nodes == "" {
			fmt.Fprintln(os.Stderr, "Error: -nodes is required in keygen mode.")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  pff-sidecar -mode=keygen \\")
			fmt.Fprintln(os.Stderr, "    -nodes=node1:10.0.0.1:8080:coordinator,node2:10.0.0.2:8080:validator")
			os.Exit(1)
		}
		if err := RunKeygen(strings.Split(*nodes, ","), *keyDir, *cfgPath); err != nil {
			log.Fatalf("[KEYGEN] Error: %v", err)
		}

	case "coordinator":
		c, err := LoadConfig(*cfgPath)
		if err != nil {
			log.Fatalf("[COORDINATOR] Config error: %v", err)
		}
		if *rounds < 1 {
			log.Fatalf("[COORDINATOR] -rounds must be at least 1, got %d", *rounds)
		}
		coord := NewCoordinator(c, *rounds, *suite)
		if err := coord.Run(*keyDir); err != nil {
			log.Fatalf("[COORDINATOR] Fatal: %v", err)
		}

	case "validator":
		if *nodeID == "" {
			fmt.Fprintln(os.Stderr, "Error: -node-id is required in validator mode")
			flag.Usage()
			os.Exit(1)
		}
		c, err := LoadConfig(*cfgPath)
		if err != nil {
			log.Fatalf("[VALIDATOR] Config error: %v", err)
		}
		v, err := NewValidator(c, *nodeID, *keyDir)
		if err != nil {
			log.Fatalf("[VALIDATOR] Init error: %v", err)
		}
		if err := v.Run(); err != nil {
			log.Fatalf("[VALIDATOR] Fatal: %v", err)
		}

	default:
		fmt.Println("PFF Sidecar — Probabilistic Fast Finality consensus middleware")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  pff-sidecar -mode=keygen -nodes=id:ip:port:role,...")
		fmt.Println("      Generate Ed25519 keypairs, write cluster_config.json (public keys)")
		fmt.Println("      and keys/<id>.key (one private key per node)")
		fmt.Println()
		fmt.Println("  pff-sidecar -mode=coordinator -rounds=200 -suite=A3_Baseline")
		fmt.Println("      Run coordinator for N consensus rounds")
		fmt.Println()
		fmt.Println("  pff-sidecar -mode=validator -node-id=node2")
		fmt.Println("      Run as a validator node")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}
}
