package main

import (
	"fmt"
	"log"

	// "net/http"
	"context"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

func main() {

	// API routes

	// Serve files from static folder
	// http.Handle("/", http.FileServer(http.Dir("./static")))

	// // Serve api /TonyRippyis
	// http.HandleFunc("/TonyRippyis", func(w http.ResponseWriter, r *http.Request) {
	// 	fmt.Fprintf(w, "old, weak, bald, and short")
	// })

	// port := ":5000"
	// fmt.Println("Server is running on port" + port)

	// // Start server on port specified above
	// log.Fatal(http.ListenAndServe(port, nil))
	config := api.Config{
		Address: "http://localhost:9090",
	}
	client, err := api.NewClient(config)
	if err != nil {
		log.Fatal(err)
	}
	API := v1.NewAPI(client)
	targets, err := API.Targets(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, at := range targets.Active {
		fmt.Println(at.ScrapeURL)
	}
}
