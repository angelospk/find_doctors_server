package main

import (
	"log"
	"net/http"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/api"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

func main() {
	log.Println("Initializing Ministry API Client...")
	client := ministry.NewClient("https://www.finddoctors.gov.gr")
	
	log.Println("Initializing Concurrent Aggregator Engine...")
	agg := aggregator.New(client)
	
	server := api.NewServer(agg)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/nationwide", server.HandleNationwideSearch)
	mux.HandleFunc("/api/emergency", server.HandleEmergency)
	mux.HandleFunc("/api/specialties", server.HandleGetSpecialties)
	mux.HandleFunc("GET /api/hospitals/{hunitId}/capacity", server.HandleHospitalCapacity)

	log.Println("Aggregator backend server listening on http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
