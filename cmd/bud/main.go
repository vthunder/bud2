package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	fmt.Println("bud2 - subsumption-inspired agent")
	fmt.Println("==================================")

	// TODO: Initialize subsystems
	// - Load percept pool
	// - Load thread pool
	// - Start Discord sense (goroutine)
	// - Start attention loop (goroutine)
	// - Start Discord effector (goroutine)

	log.Println("Starting... (not implemented yet)")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}
