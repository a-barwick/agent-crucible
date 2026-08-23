//go:build unix

package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spawnEnv makes the test binary re-invoke itself as a runner that starts a
// sidecar and then dies without cleaning up, which is what a SIGKILL or a panic
// looks like from the sidecar's side.
const spawnEnv = "CRUCIBLE_TEST_SPAWN_ORPHAN"

// TestSidecarDiesWithItsParent covers the leak that Stop() cannot: nothing runs
// on the way out of a killed process, so a sidecar that only trusts its parent
// to kill it survives as a child of init, holding its port and answering with
// whatever code it was started with.
//
// It watches the sidecar's pid rather than polling /health, because polling is
// itself enough to kill the Python sidecar once its parent is gone: the request
// log goes to a pipe nobody is reading any more. A sidecar nobody pokes has to
// leave on its own.
func TestSidecarDiesWithItsParent(t *testing.T) {
	if kind := os.Getenv(spawnEnv); kind != "" {
		spawnAndAbandon(kind)
		return
	}
	for _, tc := range []struct {
		kind string
		skip func() bool
	}{
		{"python", func() bool { return !HaveLangGraph() || FindDir() == "" }},
		{"node", func() bool { return !HaveNode() || FindJSDir() == "" }},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if tc.skip() {
				t.Skipf("%s runtime not available", tc.kind)
			}
			pid := startOrphan(t, tc.kind)
			// The sidecar polls for its parent, so allow a few beats. Still
			// running at the deadline is the bug.
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				if !alive(pid) {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("%s sidecar (pid %d) outlived the process that started it", tc.kind, pid)
		})
	}
}

// startOrphan runs the helper, checks the sidecar is serving while the helper is
// still alive, then lets the helper exit, so that by the time it returns the
// sidecar is known-good and parentless. It returns the sidecar's pid.
func startOrphan(t *testing.T, kind string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestSidecarDiesWithItsParent", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), spawnEnv+"="+kind)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Whatever happens below, do not leave the helper or its sidecar running.
	done := false
	defer func() {
		if !done {
			_ = in.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var url string
	var pid int
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "orphan ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 {
			t.Fatalf("helper said %q", rest)
		}
		url = fields[0]
		if pid, err = strconv.Atoi(fields[1]); err != nil {
			t.Fatalf("helper pid %q: %v", fields[1], err)
		}
		break
	}
	if url == "" {
		t.Fatal("helper did not report a sidecar")
	}
	// Health has to be established while the helper lives, or a sidecar that
	// never came up at all would look like one that shut down politely.
	if !healthy(url) {
		t.Fatalf("sidecar at %s was not serving before its parent died", url)
	}
	// Drain whatever the test framework prints after us, or the helper blocks on
	// a full pipe instead of exiting.
	go func() {
		for sc.Scan() {
		}
	}()
	// Closing stdin is the helper's cue that we have seen the sidecar answer.
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	done = true
	return pid
}

// spawnAndAbandon is the helper side: start a sidecar, say where it is, and
// leave without calling StopAll.
func spawnAndAbandon(kind string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var (
		p   *Proc
		err error
	)
	switch kind {
	case "python":
		p, err = EnsureLocal(ctx)
	case "node":
		p, err = EnsureNode(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(1)
	}
	fmt.Println("orphan", p.URL, p.cmd.Process.Pid)
	_ = os.Stdout.Sync()
	_, _ = io.Copy(io.Discard, os.Stdin)
	// Deliberately no StopAll: the sidecar has to notice on its own.
	os.Exit(0)
}

// alive reports whether pid is a running process. A zombie counts as gone: the
// sidecar has exited and is only waiting to be reaped, which on some hosts is
// all init ever does for it.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // no procfs to consult; the signal is all we have
	}
	// "pid (comm) state ...", and comm can contain spaces and parens.
	i := strings.LastIndex(string(stat), ") ")
	if i < 0 || len(stat) < i+3 {
		return true
	}
	return stat[i+2] != 'Z'
}
