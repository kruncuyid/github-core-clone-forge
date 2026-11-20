package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	addr := listener.Addr().(*net.TCPAddr)
	fmt.Printf("PORT %d\n", addr.Port)

	server := &http.Server{
		Handler:           http.HandlerFunc(serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
