package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	nodeMu   sync.Mutex
	liveNode *Proc
)

func HaveNode() bool {
	cmd := exec.Command("node", "-v")
	return cmd.Run() == nil
}

func FindJSDir() string {
	root := FindDir()
	if root == "" {
		return ""
	}
	js := filepath.Join(root, "js")
	if fileExists(filepath.Join(js, "server.mjs")) {
		return js
	}
	return ""
}

func EnsureNode(ctx context.Context) (*Proc, error) {
	nodeMu.Lock()
	defer nodeMu.Unlock()
	if liveNode != nil && healthy(liveNode.URL) {
		return liveNode, nil
	}
	if liveNode != nil {
		liveNode.Stop()
		liveNode = nil
	}
	dir := FindJSDir()
	if dir == "" {
		return nil, fmt.Errorf("js runtime not found (runtime/js/server.mjs)")
	}
	if !HaveNode() {
		return nil, fmt.Errorf("node is not installed")
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	pctx, cancel := context.WithCancel(context.Background())
	// --parent-pid: see EnsureLocal. A sidecar that outlives the runner holds
	// its port and answers with whatever code it started with.
	cmd := exec.CommandContext(pctx, "node", filepath.Join(dir, "server.mjs"),
		"--addr", addr, "--parent-pid", strconv.Itoa(os.Getpid()))
	cmd.Dir = dir
	cmd.Env = os.Environ()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go drain(stdout)
	go drain(stderr)
	url := "http://" + addr
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			cancel()
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		}
		if healthy(url) {
			liveNode = &Proc{URL: url, cmd: cmd, cancel: cancel}
			return liveNode, nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	cancel()
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("js runtime did not become healthy on %s", addr)
}
