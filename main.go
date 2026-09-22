package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	netbird "github.com/netbirdio/netbird/client/embed"
	mgm "github.com/netbirdio/netbird/shared/management/client"
	"github.com/pires/go-proxyproto"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func main() {
	// Configuration from environment variables
	setupKey := mustEnv("NB_SETUP_KEY")
	listenPort := mustEnv("PROXY_LISTEN_PORT")
	targetAddr := mustEnv("PROXY_TARGET_ADDR")

	managementURL := os.Getenv("NB_MANAGEMENT_URL")
	useProxyProto := os.Getenv("PROXY_USE_PROXY_PROTOCOL") == "true"

	var dnsLabels []string
	if labelsEnv := os.Getenv("NB_EXTRA_DNS_LABELS"); labelsEnv != "" {
		dnsLabels = strings.Split(labelsEnv, ",")
	}

	// Robustness tunables (see README).
	healthAddr := ":" + envDefault("HEALTH_LISTEN_PORT", "8081")
	watchdogInterval := envDuration("WATCHDOG_INTERVAL", 30*time.Second)
	watchdogGrace := envDuration("WATCHDOG_GRACE", 3*time.Minute)

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

	// Start the Netbird client. The context bounds only the startup wait; the
	// engine keeps running on its own context afterwards (see embed.Start).
	startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startCancel()

	log.Println("Starting Netbird client...")
	if err := client.Start(startCtx); err != nil {
		log.Fatalf("Failed to start Netbird client: %v", err)
	}

	// Wait until the management connection is up before listening, instead of a
	// fixed sleep, so startup is deterministic.
	if err := waitReady(client, 20*time.Second); err != nil {
		log.Printf("Netbird not fully ready after startup: %v (continuing anyway)", err)
	}

	// Listen for incoming traffic on the Netbird network interface. Retry briefly
	// because the userspace net stack can lag just behind Start returning.
	listenStr := fmt.Sprintf(":%s", listenPort)
	listener, err := listenWithRetry(client, listenStr, 15*time.Second)
	if err != nil {
		log.Fatalf("Failed to listen on Netbird network %s: %v", listenStr, err)
	}
	log.Printf("Listening on Netbird network port %s, forwarding to %s (Proxy Protocol: %v)\n", listenPort, targetAddr, useProxyProto)

	// Shared health state, maintained by the watchdog and read by the HTTP probe.
	var listenerBroken atomic.Bool
	h := &health{healthy: true}

	// Accept connections until the listener is closed. Started before the
	// watchdog so that the self-probe below is served.
	done := make(chan struct{})
	go acceptLoop(listener, targetAddr, useProxyProto, addrHost(listener.Addr()), &listenerBroken, done)

	opts := watchdogOpts{
		interval:   watchdogInterval,
		grace:      watchdogGrace,
		listenPort: listenPort,
		selfProbe:  verifySelfProbe(client, listenPort, 10*time.Second),
	}

	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	healthSrv := startHealthServer(healthAddr, h, watchdogGrace)
	go runWatchdog(watchdogCtx, client, h, &listenerBroken, opts)
	log.Printf("Health endpoint on %s/healthz (watchdog-grace=%s)\n", healthAddr, watchdogGrace)

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")

	watchdogCancel()
	if err := healthSrv.Close(); err != nil {
		log.Printf("Error closing health server: %v", err)
	}

	if err := listener.Close(); err != nil {
		log.Printf("Error closing listener: %v", err)
	}

	// Deregister from Management so the peer (and its DNS label + routes) is
	// removed immediately, rather than lingering until Management's ephemeral
	// cleanup (~10m offline). Best-effort: on failure we fall back to that
	// cleanup, so this never blocks shutdown beyond its short timeout.
	deregisterCtx, deregisterCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := deregisterPeer(deregisterCtx, client); err != nil {
		log.Printf("Peer deregistration failed (falling back to management ephemeral cleanup): %v", err)
	} else {
		log.Println("Deregistered peer from management")
	}
	deregisterCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := client.Stop(shutdownCtx); err != nil {
		log.Printf("Netbird shutdown error: %v", err)
	}

	<-done // Wait for the accept loop to exit fully
	log.Println("Shutdown complete.")
}

// health holds the liveness state shared between the watchdog goroutine (writer)
// and the HTTP probe handler (reader).
type health struct {
	mu             sync.Mutex
	healthy        bool
	reason         string
	unhealthySince time.Time
}

func (h *health) set(ok bool, reason string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthy = ok
	h.reason = reason
	switch {
	case ok:
		h.unhealthySince = time.Time{}
	case h.unhealthySince.IsZero():
		h.unhealthySince = now
	}
}

// downFor reports how long the peer has been continuously unhealthy (0 when
// healthy) and the most recent reason.
func (h *health) downFor(now time.Time) (time.Duration, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.healthy || h.unhealthySince.IsZero() {
		return 0, h.reason
	}
	return now.Sub(h.unhealthySince), h.reason
}

// evaluateHealth inspects the NetBird client and classifies its state:
//
//   - terminal=true: the embedded engine has stopped for good. NetBird exits its
//     connection retry loop on unrecoverable errors (e.g. the peer was
//     deregistered -> PermissionDenied, connect.go returns backoff.Permanent),
//     which clears the engine and it never comes back in-process. The watchdog
//     exits straight away rather than waiting out the grace window.
//   - ok=false (recoverable/degraded): the status call errors, or management and
//     signal are both disconnected. Subject to the grace window, since these can
//     be transient.
//
// Every condition here is about this peer's own machinery: nothing another
// device does, or fails to do, can recycle this pod. The watchdog pairs this
// with the self-probe, which tests the data path itself.
func evaluateHealth(client *netbird.Client) (ok bool, terminal bool, reason string) {
	if stopped, err := engineStopped(client); stopped {
		return false, true, "NetBird engine stopped (" + err.Error() + ")"
	}

	st, err := client.Status()
	if err != nil {
		return false, false, "status error: " + err.Error()
	}

	if !st.ManagementState.Connected && !st.SignalState.Connected {
		return false, false, "management and signal both disconnected"
	}

	return true, false, "ok"
}

// watchdogOpts carries the watchdog's tunables, which are fixed for the life of
// the process.
type watchdogOpts struct {
	interval   time.Duration
	grace      time.Duration
	listenPort string
	selfProbe  bool
}

// selfProbeTimeout bounds a single self-probe dial. The netstack answers from
// memory, so anything slower than this is already a wedge.
const selfProbeTimeout = 5 * time.Second

// verifySelfProbe reports whether the self-probe works at all here, retrying
// until the timeout because the local address and the netstack both settle a
// moment after the listener exists. A probe that never succeeds is switched off
// rather than trusted, so a netstack that will not loop back to itself costs a
// log line instead of an endless restart loop.
func verifySelfProbe(client *netbird.Client, listenPort string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		last := errors.New("no local peer address yet")
		if addr := selfProbeAddr(client, listenPort); addr != "" {
			err := probeSelf(client, addr, selfProbeTimeout)
			if err == nil {
				log.Printf("Self-probe to %s verified", addr)
				return true
			}
			last = err
		}

		if time.Now().After(deadline) {
			log.Printf("Self-probe unavailable (%v); liveness will not use it", last)
			return false
		}
		time.Sleep(time.Second)
	}
}

// selfProbeAddr resolves this peer's own address on the NetBird network, or ""
// when the engine cannot state it yet. It is re-resolved per probe so that an
// address change is not papered over: the listener stays bound to the old
// address, so the probe to the new one fails, which is the correct verdict.
func selfProbeAddr(client *netbird.Client, listenPort string) string {
	st, err := client.Status()
	if err != nil {
		return ""
	}

	ip := st.LocalPeerState.IP
	if ip == "" {
		return ""
	}
	if pfx, err := netip.ParsePrefix(ip); err == nil {
		ip = pfx.Addr().String()
	}

	return net.JoinHostPort(ip, listenPort)
}

// probeSelf dials the proxy port over the NetBird network and expects to be
// accepted.
//
// This is the health signal that needs no remote device to be awake, and it is a
// direct test of the wedge this watchdog exists for. Dial resolves the engine's
// current netstack, while the listener stays bound to the netstack it was
// created on, so once the engine rebuilds its net the listener is orphaned: it
// sits there accepting nothing while the live stack refuses clients on that
// port. The probe is refused in exactly that state, and so is a listener that
// has quietly closed, which today only ends the accept loop and is otherwise
// unnoticed.
func probeSelf(client *netbird.Client, addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := client.Dial(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// addrHost returns the address without its port.
func addrHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	return host
}

// engineStopped reports whether the embedded NetBird engine is gone. Status()
// never errors and returns stale data after a stop, so probe a getEngine-backed
// call instead: it returns ErrEngineNotStarted / ErrClientNotStarted exactly
// when the engine goroutine has exited. The returned error carries the reason.
func engineStopped(client *netbird.Client) (bool, error) {
	_, err := client.GetLatestSyncResponse()
	if errors.Is(err, netbird.ErrEngineNotStarted) || errors.Is(err, netbird.ErrClientNotStarted) {
		return true, err
	}
	return false, nil
}

// confirmEngineStopped re-probes a few times so a brief engine restart (the
// engine is momentarily nil between reconnect attempts) is not mistaken for a
// terminal stop. Returns true only if the engine stays gone throughout.
func confirmEngineStopped(client *netbird.Client) bool {
	for i := 0; i < 3; i++ {
		time.Sleep(2 * time.Second)
		if stopped, _ := engineStopped(client); !stopped {
			return false
		}
	}
	return true
}

// runWatchdog re-evaluates health on a ticker. After sustained failure past
// grace it exits the process so Kubernetes recreates the pod, which is exactly
// what a manual pod delete does: a fresh NetBird registration and fresh peer
// handshakes. Recreate is preferred over an in-process engine restart because
// the listener is bound to the engine's net stack.
func runWatchdog(ctx context.Context, client *netbird.Client, h *health, listenerBroken *atomic.Bool, opts watchdogOpts) {
	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, terminal, reason := evaluateHealth(client)

			// Terminal states will not recover in-process, so skip the grace
			// window and exit as soon as a quick re-check confirms it.
			if terminal && confirmEngineStopped(client) {
				log.Printf("watchdog: %s; not recoverable in-process, exiting immediately to trigger pod recreation", reason)
				os.Exit(1)
			}

			if listenerBroken.Load() {
				ok, reason = false, "accept loop persistently failing"
			}

			// Only probe an otherwise healthy peer: once something else is
			// already wrong the probe adds noise, not information.
			if ok && opts.selfProbe {
				if addr := selfProbeAddr(client, opts.listenPort); addr != "" {
					if err := probeSelf(client, addr, selfProbeTimeout); err != nil {
						ok, reason = false, fmt.Sprintf("self-probe to %s failed: %v", addr, err)
					}
				}
			}

			now := time.Now()
			h.set(ok, reason, now)

			down, r := h.downFor(now)
			switch {
			case down >= opts.grace:
				log.Printf("watchdog: unhealthy for %s (%s); exiting to trigger pod recreation", down.Round(time.Second), r)
				os.Exit(1)
			case !ok:
				log.Printf("watchdog: unhealthy (%s); within grace %s", reason, opts.grace)
			}
		}
	}
}

// startHealthServer serves /healthz on a pod-local address. It returns 500 only
// once the peer has been unhealthy longer than grace, so the ~per-minute
// management blips do not cause restart loops.
func startHealthServer(addr string, h *health, grace time.Duration) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		down, reason := h.downFor(time.Now())
		if down >= grace {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "unhealthy: %s (for %s)\n", reason, down.Round(time.Second))
			return
		}
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Health server error: %v", err)
		}
	}()
	return srv
}

// acceptLoop accepts connections until the listener is closed. Unknown accept
// errors are backed off (instead of a tight spin) and, if they persist, mark the
// listener broken so the watchdog recycles the pod.
func acceptLoop(listener net.Listener, targetAddr string, useProxyProto bool, selfIP string, listenerBroken *atomic.Bool, done chan struct{}) {
	defer close(done)

	const brokenThreshold = 10
	var consecutive int

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) ||
				strings.Contains(err.Error(), "invalid state") ||
				strings.Contains(err.Error(), "use of closed network connection") {
				return
			}

			consecutive++
			log.Printf("Failed to accept connection (%d consecutive): %v\n", consecutive, err)
			if consecutive >= brokenThreshold {
				listenerBroken.Store(true)
			}

			backoff := time.Duration(consecutive) * 100 * time.Millisecond
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			time.Sleep(backoff)
			continue
		}

		consecutive = 0
		listenerBroken.Store(false)

		// The watchdog's self-probe arrives from this peer's own address, which
		// no client can have. Accepting it is the whole test, so it never
		// reaches the target.
		if selfIP != "" && addrHost(conn.RemoteAddr()) == selfIP {
			_ = conn.Close()
			continue
		}

		go handleConnection(conn, targetAddr, useProxyProto)
	}
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

	// Buffered so the second copy's send never blocks after the first direction
	// closes and the deferred Close calls unblock it: without the buffer that
	// goroutine leaks for the lifetime of the process, one per connection.
	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(src, dst)
		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}()

	// Tear down both directions as soon as either side closes, promptly freeing
	// the backend (database) connection.
	<-done
	log.Printf("Connection from %s closed", src.RemoteAddr())
}

// deregisterPeer deletes this peer from Management via the peer-authenticated
// Logout RPC (Management runs DeletePeer), so the peer is removed at once
// instead of waiting out the ~10m ephemeral offline cleanup. It reuses the
// client's own credentials (private key + management URL) over a fresh
// connection, so no API token is needed. Requires the setup key to be reusable
// so the next pod can re-register.
func deregisterPeer(ctx context.Context, client *netbird.Client) error {
	cfg, err := client.GetConfig()
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}
	if cfg.ManagementURL == nil {
		return errors.New("no management URL in config")
	}

	key, err := wgtypes.ParseKey(cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}

	mgmClient, err := mgm.NewClient(ctx, cfg.ManagementURL.Host, key, cfg.ManagementURL.Scheme == "https")
	if err != nil {
		return fmt.Errorf("connect to management: %w", err)
	}
	defer func() { _ = mgmClient.Close() }()

	return mgmClient.Logout()
}

// waitReady blocks until the management connection is up or the timeout elapses.
func waitReady(client *netbird.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := client.Status()
		if err == nil && st.ManagementState.Connected {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return errors.New("management connection not established")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// listenWithRetry retries ListenTCP until it succeeds or the timeout elapses.
func listenWithRetry(client *netbird.Client, address string, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, err := client.ListenTCP(address)
		if err == nil {
			return l, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		log.Printf("ListenTCP not ready yet (%v); retrying", err)
		time.Sleep(500 * time.Millisecond)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s environment variable is required", key)
	}
	return v
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("Invalid %s=%q (want a Go duration like 5m); using default %s", key, v, def)
	}
	return def
}
