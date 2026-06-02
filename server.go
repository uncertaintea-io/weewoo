package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
)

func main() {

	//API routes

	//Serve files from static folder
	http.Handle("/", http.FileServer(http.Dir("./static")))

	//Serve api /TonyRippyis
	http.HandleFunc("/TonyRippyis", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "old, weak, bald, and short")
	})

	port := ":5000"
	otherPort := ":8080"
	fmt.Println("Server is running on port" + port)

	//Start server on port specified above
	log.Fatal(http.ListenAndServe(port, nil))

	serverErr := make(chan error)
	go func() {
		serverErr <- http.ListenAndServe(port, nil)
	}()

	go func() {
		serverErr <- http.ListenAndServe(otherPort, nil)
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-signalCtx.Done():

	case err := <-serverErr:
		log.Fatal(err)
	}

}
