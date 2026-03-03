package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	netbird "github.com/netbirdio/netbird/client/embed"
	"github.com/pires/go-proxyproto"
)

func main() {
	// Configuration from environment variables
	setupKey := os.Getenv("NB_SETUP_KEY")
	if setupKey == "" {
		log.Fatal("NB_SETUP_KEY environment variable is required")
	}

	listenPort := os.Getenv("PROXY_LISTEN_PORT")
	if listenPort == "" {
		log.Fatal("LISTEN_PORT environment variable is required")
	}

	targetAddr := os.Getenv("PROXY_TARGET_ADDR")
	if targetAddr == "" {
		log.Fatal("TARGET_ADDR environment variable is required")
	}

	managementURL := os.Getenv("NB_MANAGEMENT_URL")
	useProxyProto := os.Getenv("PROXY_USE_PROXY_PROTOCOL") == "true"

	labelsEnv := os.Getenv("NB_EXTRA_DNS_LABELS")
	var dnsLabels []string
	if labelsEnv != "" {
		dnsLabels = strings.Split(labelsEnv, ",")
	}

	hostname := os.Getenv("HOSTNAME")
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	// Initialize the embedded Netbird client
	client, err := netbird.New(netbird.Options{
		DeviceName:    hostname,
		SetupKey:      setupKey,
		ManagementURL: managementURL,
		LogOutput:     os.Stdout,
		DNSLabels:     dnsLabels,
		LogLevel:      "info",
	})
	if err != nil {
		log.Fatalf("Failed to create Netbird client: %v", err)
	}

	// Start the Netbird client
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Println("Starting Netbird client...")
	if err := client.Start(ctx); err != nil {
		log.Fatalf("Failed to start Netbird client: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Listen for incoming traffic on the Netbird network interface
	listenStr := fmt.Sprintf(":%s", listenPort)
	listener, err := client.ListenTCP(listenStr)
	if err != nil {
		log.Fatalf("Failed to listen on Netbird network %s: %v", listenStr, err)
	}
	log.Printf("Listening on Netbird network port %s, forwarding to %s (Proxy Protocol: %v)\n", listenPort, targetAddr, useProxyProto)

	// Channel to signal the accept loop to stop cleanly
	done := make(chan struct{})

	// Handle connection acceptance in a goroutine
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) ||
					strings.Contains(err.Error(), "invalid state") ||
					strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				log.Printf("Failed to accept connection: %v\n", err)
				continue
			}

			go handleConnection(conn, targetAddr, useProxyProto)
		}
	}()

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")

	if err := listener.Close(); err != nil {
		log.Printf("Error closing listener: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := client.Stop(shutdownCtx); err != nil {
		log.Printf("Netbird shutdown error: %v", err)
	}

	<-done // Wait for the accept loop to exit fully
	log.Println("Shutdown complete.")
}

func handleConnection(src net.Conn, targetAddr string, sendProxyHeader bool) {
	defer src.Close()

	log.Printf("Accepted connection from %s, dialing target %s", src.RemoteAddr(), targetAddr)

	dst, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		log.Printf("Failed to dial target %s: %v\n", targetAddr, err)
		return
	}
	defer dst.Close()

	if sendProxyHeader {
		header := &proxyproto.Header{
			Version:           1,
			Command:           proxyproto.PROXY,
			TransportProtocol: proxyproto.TCPv4,
			SourceAddr:        src.RemoteAddr(),
			DestinationAddr:   src.LocalAddr(),
		}

		if srcTCP, ok := src.RemoteAddr().(*net.TCPAddr); ok && srcTCP.IP.To4() == nil {
			header.TransportProtocol = proxyproto.TCPv6
		}

		if _, err := header.WriteTo(dst); err != nil {
			log.Printf("Failed to write PROXY protocol header to target: %v", err)
			return
		}
	}

	done := make(chan struct{})

	go func() {
		_, _ = io.Copy(src, dst)
		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}()

	<-done
	log.Printf("Connection from %s closed", src.RemoteAddr())
}
