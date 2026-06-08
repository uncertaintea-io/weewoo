package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

func main() {

	//Serve files from static folder
	http.Handle("/", http.FileServer(http.Dir("./static")))

	//Serve api /Test
	http.HandleFunc("/Test", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello world!")
	})

	monitorPort := ":5000"
	appPort := ":8080"
	fmt.Println("Server is running on port" + appPort)

	appServer := &http.Server{
		Addr:           appPort,
		Handler:        nil,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	serverErr := make(chan error)
	go func() {
		serverErr <- http.ListenAndServe(monitorPort, nil)
	}()

	go func() {
		serverErr <- appServer.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-signalCtx.Done():

	case err := <-serverErr:
		log.Fatal(err)
	}

	appServer.Shutdown(context.Background())
}
