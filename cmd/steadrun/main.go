// Command steadrun wraps a cron job and reports it to Steadrun (https://steadrun.ru):
// /start before the command, a success ping on exit code 0, and /fail with the exit
// code and the tail of the output otherwise.
//
//	steadrun run <uuid> -- <command> [args...]
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const (
	usage          = "usage: steadrun run <uuid> -- <command> [args...]"
	defaultBaseURL = "https://steadrun.ru"
	// Same as withOutput in the Laravel package; the server keeps the first 10 KB of a body.
	tailBytes = 8192
)

func main() {
	os.Exit(cli(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func cli(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(stdout, "steadrun", version)
		return 0
	}

	if len(args) >= 3 && args[0] == "run" && args[2] == "--" {
		args = append(args[:2:2], args[3:]...)
	}

	if len(args) < 3 || args[0] != "run" || args[1] == "" {
		fmt.Fprintln(stderr, usage)
		return 2
	}

	baseURL := os.Getenv("STEADRUN_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	p := pinger{
		url:    strings.TrimRight(baseURL, "/") + "/ping/" + args[1],
		client: &http.Client{Timeout: 5 * time.Second},
		stderr: stderr,
	}

	return run(p, args[2:], stdin, stdout, stderr)
}

func run(p pinger, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// Subscribe before starting the command so a signal is never lost in between.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	p.ping("/start", nil)

	tail := &tailBuffer{}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = io.MultiWriter(stdout, tail)
	cmd.Stderr = io.MultiWriter(stderr, tail)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "steadrun: %v\n", err)
		p.ping("/fail", []byte(fmt.Sprintf("Exit code: 127\n\n%v", err)))
		return 127
	}

	// The child is in our process group, so Ctrl+C in a terminal reaches it directly;
	// forwarding matters for `kill` and for Docker/Kubernetes, which signal only PID 1.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-signals:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	close(done)

	code, status := exitCode(cmd, err)
	if code == 0 {
		p.ping("", nil)
		return 0
	}

	body := "Exit code: " + status
	if out := tail.String(); out != "" {
		body += "\n\n" + out
	}
	p.ping("/fail", []byte(body))

	return code
}

// exitCode returns the code to exit with and how to describe it in the /fail body.
// A command killed by a signal exits as a shell would report it: 128 + signal number.
func exitCode(cmd *exec.Cmd, err error) (int, string) {
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return 1, fmt.Sprintf("1 (%v)", err)
	}

	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code := 128 + int(ws.Signal())
		return code, fmt.Sprintf("%d (killed by signal: %v)", code, ws.Signal())
	}

	code := cmd.ProcessState.ExitCode()
	return code, fmt.Sprint(code)
}

type pinger struct {
	url    string
	client *http.Client
	stderr io.Writer
}

// ping never fails the job: a problem reaching Steadrun is one line in stderr.
func (p pinger) ping(suffix string, body []byte) {
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}

	req, err := http.NewRequest(method, p.url+suffix, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(p.stderr, "steadrun: ping%s: %v\n", suffix, err)
		return
	}
	req.Header.Set("User-Agent", "steadrun-cli/"+version)
	if body != nil {
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		fmt.Fprintf(p.stderr, "steadrun: ping%s: %v\n", suffix, err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		fmt.Fprintf(p.stderr, "steadrun: ping%s: HTTP %d\n", suffix, resp.StatusCode)
	}
}

// tailBuffer keeps the last tailBytes of everything written to it. stdout and stderr
// are copied by separate goroutines, hence the mutex.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*tailBytes {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailBytes:]...)
	}

	return len(p), nil
}

// String returns the last tailBytes without a broken UTF-8 character at the start.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := t.buf
	if len(out) > tailBytes {
		out = out[len(out)-tailBytes:]
		for i := 0; i < 3 && len(out) > 0 && out[0]&0xC0 == 0x80; i++ {
			out = out[1:]
		}
	}

	return string(out)
}
