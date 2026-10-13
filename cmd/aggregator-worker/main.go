package main

import (
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/processing"
	"log"
)

func main() {
	if e := processing.RunWorker("aggregate"); e != nil {
		log.Fatal(e)
	}
}
