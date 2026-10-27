// Command demo runs two local HTTP endpoints with a few intentional ingress
// migration differences for varco to find.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	oldPort := flag.Int("old-port", 18081, "port for the ingress-nginx-like server (0 chooses a free port)")
	newPort := flag.Int("new-port", 18082, "port for the new-controller-like server (0 chooses a free port)")
	flag.Parse()

	listenConfig := net.ListenConfig{}
	oldListener, err := listenConfig.Listen(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", *oldPort))
	if err != nil {
		return err
	}
	newListener, err := listenConfig.Listen(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", *newPort))
	if err != nil {
		if closeErr := oldListener.Close(); closeErr != nil {
			return fmt.Errorf("listen on new port: %w (close old listener: %w)", err, closeErr)
		}
		return err
	}

	oldHandler, newHandler := demoHandlers()
	oldServer := &http.Server{Handler: oldHandler, ReadHeaderTimeout: 5 * time.Second}
	newServer := &http.Server{Handler: newHandler, ReadHeaderTimeout: 5 * time.Second}
	serve(oldServer, oldListener)
	serve(newServer, newListener)

	oldAddress, newAddress := oldListener.Addr().String(), newListener.Addr().String()
	fmt.Printf("old ingress-nginx-like server: http://%s\n", oldAddress)
	fmt.Printf("new controller-like server: http://%s\n\n", newAddress)
	fmt.Println("varco plan -f testdata/demo/ingress.yaml --format json > plan.json")
	fmt.Printf("varco run --plan plan.json --old %s --new %s --dry-run\n", oldAddress, newAddress)
	fmt.Printf("varco run --plan plan.json --old %s --new %s\n\n", oldAddress, newAddress)
	fmt.Println("Expected verdict: DIFFERENT (exit code 1)")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	signal.Stop(stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := oldServer.Shutdown(ctx); err != nil {
		log.Printf("stop old server: %v", err)
	}
	if err := newServer.Shutdown(ctx); err != nil {
		log.Printf("stop new server: %v", err)
	}
	return nil
}

func serve(server *http.Server, listener net.Listener) {
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("serve %s: %v", listener.Addr(), err)
		}
	}()
}

func demoHandlers() (http.Handler, http.Handler) {
	return demoHandler(true), demoHandler(false)
}

func demoHandler(old bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if old {
			w.Header().Set("Server", "ingress-nginx")
		} else {
			w.Header().Set("Server", "new-controller")
		}

		requestPath := r.URL.Path
		if old && requestPath == "/app" {
			w.Header().Set("Location", "/app/")
			w.WriteHeader(http.StatusMovedPermanently)
			_, _ = w.Write([]byte("redirecting to /app/\n"))
			return
		}

		matchedPath := path.Clean(requestPath)
		if old {
			matchedPath = strings.ToLower(matchedPath)
		}
		if !demoPath(matchedPath) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("varco demo\n"))
	})
}

func demoPath(value string) bool {
	if strings.HasPrefix(value, "/app/") || strings.HasPrefix(value, "/health/") {
		return true
	}
	switch value {
	case "/app", "/appvarco", "/health", "/healthvarco":
		return true
	default:
		return false
	}
}
