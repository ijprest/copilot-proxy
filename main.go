// Command copilot-proxy runs a minimal-dependency HTTP proxy that authenticates
// against GitHub Copilot and forwards a local coding agent's OpenAI-style
// requests to the Copilot API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"syscall"
	"time"

	"copilot-proxy/internal/config"
	"copilot-proxy/internal/copilot"
	"copilot-proxy/internal/proxy"
)

func main() {
	log.SetFlags(0)

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "login":
		err = runLogin(args)
	case "logout":
		err = runLogout(args)
	case "status":
		err = runStatus(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func usage() {
	fmt.Print(`copilot-proxy - HTTP proxy between a local coding agent and GitHub Copilot

Usage:
  copilot-proxy [serve] [flags]   Start the proxy (default command)
  copilot-proxy login             Authenticate with GitHub via the device flow
  copilot-proxy logout            Remove the stored GitHub token
  copilot-proxy status            Show authentication status

Serve flags:
  -p, --port <port>   Port to listen on (default 8080)
      --host <host>   Host/interface to bind (default 127.0.0.1)
  -v, --verbose       Log each proxied request

Point your agent's OpenAI base URL at the proxy, e.g. http://127.0.0.1:8080
The GitHub token is read from the ` + config.EnvGitHubToken + ` environment
variable if set, otherwise from the stored config file.
`)
}

// newHTTPClient returns a client with a sane timeout for auth/token calls.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// dumpModels fetches the Copilot /models list and prints it to stdout. It is a
// startup diagnostic: it never aborts serving, only reporting problems.
func dumpModels(tokens *copilot.Manager) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := copilot.FetchModels(ctx, tokens, newHTTPClient())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not fetch /models: %v\n", err)
		if len(data) > 0 {
			fmt.Fprintln(os.Stderr, string(data))
		}
		return
	}

	fmt.Println("=== GitHub Copilot /models ===")
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(data))
	}

	// A concise, sorted list of model ids for quick scanning.
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &parsed) == nil && len(parsed.Data) > 0 {
		ids := make([]string, 0, len(parsed.Data))
		for _, m := range parsed.Data {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
		sort.Strings(ids)
		fmt.Printf("=== %d model id(s) ===\n", len(ids))
		for _, id := range ids {
			fmt.Println("  " + id)
		}
	}
	fmt.Println("==============================")
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var port int
	var host string
	var verbose bool
	fs.IntVar(&port, "port", 8080, "port to listen on")
	fs.IntVar(&port, "p", 8080, "port to listen on (shorthand)")
	fs.StringVar(&host, "host", "127.0.0.1", "host/interface to bind")
	fs.BoolVar(&verbose, "verbose", false, "log each proxied request")
	fs.BoolVar(&verbose, "v", false, "log each proxied request (shorthand)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.GitHubToken == "" {
		return fmt.Errorf("not authenticated; run 'copilot-proxy login' first (or set %s)", config.EnvGitHubToken)
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)
	accessLog := log.New(os.Stdout, "", log.LstdFlags)
	tokens := copilot.NewManager(cfg.GitHubToken, newHTTPClient())

	// Validate the credentials up front so misconfiguration surfaces immediately
	// rather than on the first agent request.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := tokens.Validate(ctx); err != nil {
		cancel()
		return err
	}
	cancel()

	// Dump the models Copilot actually offers, so model-name mismatches are
	// easy to diagnose before any traffic is proxied.
	dumpModels(tokens)

	srv := proxy.New(tokens, accessLog, logger, verbose)
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	fmt.Printf("copilot-proxy listening on http://%s\n", addr)
	fmt.Printf("  Set your agent's OpenAI base URL to http://%s (or http://%s/v1)\n", addr, addr)
	fmt.Printf("  Forwarding to %s\n", copilot.CopilotBaseURL)

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		fmt.Println("\nshutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := newHTTPClient()
	ctx := context.Background()

	dc, err := copilot.RequestDeviceCode(ctx, client)
	if err != nil {
		return err
	}

	fmt.Println("To authenticate, open the following URL in your browser:")
	fmt.Printf("  %s\n", dc.VerificationURI)
	fmt.Printf("and enter the code: %s\n\n", dc.UserCode)
	openBrowser(dc.VerificationURI)
	fmt.Println("Waiting for authorization...")

	githubToken, err := copilot.PollAccessToken(ctx, client, dc)
	if err != nil {
		return err
	}

	// Confirm the token actually grants Copilot access before saving it.
	tokens := copilot.NewManager(githubToken, client)
	if err := tokens.Validate(ctx); err != nil {
		return fmt.Errorf("authenticated with GitHub, but Copilot access check failed: %w", err)
	}

	if err := config.Save(&config.Config{GitHubToken: githubToken}); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	path, _ := config.Path()
	fmt.Printf("\nLogin successful. Credentials saved to %s\n", path)
	fmt.Println("You can now run 'copilot-proxy' to start the proxy.")
	return nil
}

func runLogout(args []string) error {
	if err := config.Clear(); err != nil {
		return err
	}
	fmt.Println("Logged out; stored credentials removed.")
	return nil
}

func runStatus(args []string) error {
	path, _ := config.Path()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.GitHubToken == "" {
		fmt.Println("Not authenticated. Run 'copilot-proxy login'.")
		fmt.Printf("Config file: %s\n", path)
		return nil
	}

	fmt.Printf("GitHub token: present (config: %s)\n", path)

	tokens := copilot.NewManager(cfg.GitHubToken, newHTTPClient())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tokens.Validate(ctx); err != nil {
		fmt.Printf("Copilot access: FAILED (%v)\n", err)
		return nil
	}
	fmt.Println("Copilot access: OK")
	return nil
}

// openBrowser makes a best-effort attempt to open the given URL. Failures are
// silent because the user can always open the URL manually.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
