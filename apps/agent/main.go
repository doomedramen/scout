package main

import (
	"encoding/json"
	"log"
	"os"
	"scout.local/scout/internal/collector"
)

func main() {
	snapshot, err := collector.Collect()
	if err != nil {
		log.Fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		log.Fatal(err)
	}
}
