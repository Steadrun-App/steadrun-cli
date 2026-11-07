package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

type request struct {
	method, path, body string
}

// fakeSteadrun records pings; STEADRUN_BASE_URL points at it for the duration of the test.
func fakeSteadrun(t *testing.T) func() []request {
	t.Helper()

	var mu sync.Mutex
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, request{r.Method, r.URL.Path, string(body)})
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	t.Setenv("STEADRUN_BASE_URL", srv.URL+"/")

	return func() []request {
		mu.Lock()
		defer mu.Unlock()
		return append([]request(nil), got...)
	}
}

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = cli(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestSuccessSendsStartAndSuccess(t *testing.T) {
	pings := fakeSteadrun(t)

	code, stdout, stderr := runCLI("run", "abc", "--", "sh", "-c", "echo hello; echo oops >&2")

	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if stdout != "hello\n" || stderr != "oops\n" {
		t.Fatalf("output changed: stdout %q, stderr %q", stdout, stderr)
	}
	want := []request{{"GET", "/ping/abc/start", ""}, {"GET", "/ping/abc", ""}}
	if got := pings(); !equal(got, want) {
		t.Fatalf("pings %v, want %v", got, want)
	}
}

func TestFailureSendsExitCodeAndOutput(t *testing.T) {
	pings := fakeSteadrun(t)

	code, _, _ := runCLI("run", "abc", "--", "sh", "-c", "echo step 1; echo 'disk full' >&2; exit 3")

	if code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	got := pings()
	if len(got) != 2 || got[1].method != "POST" || got[1].path != "/ping/abc/fail" {
		t.Fatalf("pings %v, want start + POST fail", got)
	}
	// stdout and stderr are separate pipes, so their relative order in the tail isn't guaranteed.
	b := got[1].body
	if !strings.HasPrefix(b, "Exit code: 3\n\n") || !strings.Contains(b, "step 1\n") || !strings.Contains(b, "disk full\n") {
		t.Fatalf("fail body %q", got[1].body)
	}
}

func TestLongOutputKeepsTailWithoutBreakingUTF8(t *testing.T) {
	pings := fakeSteadrun(t)

	// 3000 × «ж» (2 bytes each) = 6000 bytes, twice — the cut lands inside the first half.
	script := "printf 'начало'; for i in 1 2; do printf '%3000s' | tr ' ' 'x' | sed 's/x/ж/g'; done; printf 'КОНЕЦ'; exit 1"
	runCLI("run", "abc", "--", "sh", "-c", script)

	body := pings()[1].body
	tail := strings.TrimPrefix(body, "Exit code: 1\n\n")
	if len(tail) > tailBytes || len(tail) < tailBytes-3 {
		t.Fatalf("tail is %d bytes, want about %d", len(tail), tailBytes)
	}
	if !utf8.ValidString(tail) || !strings.HasSuffix(tail, "КОНЕЦ") || strings.Contains(tail, "начало") {
		t.Fatalf("tail must be valid UTF-8 and end of output, got prefix %q", tail[:20])
	}
}

func TestUnreachableSteadrunChangesNothing(t *testing.T) {
	t.Setenv("STEADRUN_BASE_URL", "http://127.0.0.1:1")

	code, stdout, stderr := runCLI("run", "abc", "--", "sh", "-c", "echo hello; exit 5")

	if code != 5 || stdout != "hello\n" {
		t.Fatalf("exit code %d, stdout %q", code, stdout)
	}
	if lines := strings.Count(stderr, "\n"); lines != 2 || !strings.HasPrefix(stderr, "steadrun: ping/start:") {
		t.Fatalf("want one stderr line per failed ping, got %q", stderr)
	}
}

func TestHTTPErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	t.Setenv("STEADRUN_BASE_URL", srv.URL)

	code, _, stderr := runCLI("run", "wrong-uuid", "true")

	if code != 0 || !strings.Contains(stderr, "steadrun: ping/start: HTTP 404") {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
}

func TestCommandNotFound(t *testing.T) {
	pings := fakeSteadrun(t)

	code, _, stderr := runCLI("run", "abc", "--", "no-such-command-steadrun")

	if code != 127 || !strings.Contains(stderr, "no-such-command-steadrun") {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	if got := pings(); len(got) != 2 || !strings.HasPrefix(got[1].body, "Exit code: 127") {
		t.Fatalf("pings %v", got)
	}
}

func TestSignalIsForwardedAndCountsAsFailure(t *testing.T) {
	pings := fakeSteadrun(t)

	result := make(chan int)
	go func() {
		code, _, _ := runCLI("run", "abc", "--", "sleep", "10")
		result <- code
	}()

	// Wait for /start, then give the child a moment to spawn.
	for deadline := time.Now().Add(5 * time.Second); len(pings()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no /start ping")
		}
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-result:
		if code != 128+int(syscall.SIGTERM) {
			t.Fatalf("exit code %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child was not stopped by the forwarded signal")
	}

	if got := pings(); len(got) != 2 || !strings.Contains(got[1].body, "killed by signal: terminated") {
		t.Fatalf("pings %v", got)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"run"}, {"run", "abc"}, {"run", "abc", "--"}, {"start", "abc", "true"}} {
		if code, _, stderr := runCLI(args...); code != 2 || !strings.Contains(stderr, "usage:") {
			t.Errorf("%v: exit code %d, stderr %q", args, code, stderr)
		}
	}
}

func equal(a, b []request) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
