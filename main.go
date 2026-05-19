package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	mode   := flag.String("mode",    "",           "coordinator | validator | keygen")
	nodeID := flag.String("node-id", "",           "Node ID for validator mode (e.g. node2)")
	rounds := flag.Int("rounds",     100,          "Number of consensus rounds (coordinator)")
	suite  := flag.String("suite",   "A3_Baseline","TestSuite label written to results.csv")
	cfg    := flag.String("config",  "cluster_config.json", "Path to cluster config")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	switch *mode {

	case "keygen":
		if err := RunKeygen(); err != nil {
			log.Fatalf("[KEYGEN] Error: %v", err)
		}

	case "coordinator":
		c, err := LoadConfig(*cfg)
		if err != nil {
			log.Fatalf("[COORDINATOR] Config error: %v", err)
		}
		coord := NewCoordinator(c, *rounds, *suite)
		if err := coord.Run(); err != nil {
			log.Fatalf("[COORDINATOR] Fatal: %v", err)
		}

	case "validator":
		if *nodeID == "" {
			fmt.Fprintln(os.Stderr, "Error: -node-id is required in validator mode")
			flag.Usage()
			os.Exit(1)
		}
		c, err := LoadConfig(*cfg)
		if err != nil {
			log.Fatalf("[VALIDATOR] Config error: %v", err)
		}
		v, err := NewValidator(c, *nodeID)
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
		fmt.Println("  pff-sidecar -mode=keygen")
		fmt.Println("      Generate Ed25519 keypairs and write cluster_config.json")
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
