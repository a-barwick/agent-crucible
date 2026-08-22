package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	procMu sync.Mutex
	live   *Proc
)

type Proc struct {
	URL    string
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

func (p *Proc) Stop() {
	if p == nil {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func HaveLangGraph() bool {
	cmd := exec.Command("python3", "-c", "import langgraph, langchain_core")
	return cmd.Run() == nil
}

func FindDir() string {
	var cands []string
	if env := os.Getenv("CRUCIBLE_RUNTIME"); env != "" {
		cands = append(cands, env)
	}
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for i := 0; i < 8; i++ {
			cands = append(cands, filepath.Join(dir, "runtime"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "runtime"))
	}
	for _, c := range cands {
		if st, err := os.Stat(filepath.Join(c, "crucible_rt")); err == nil && st.IsDir() {
			return c
		}
	}
	return ""
}

func EnsureLocal(ctx context.Context) (*Proc, error) {
	procMu.Lock()
	defer procMu.Unlock()
	if live != nil && healthy(live.URL) {
		return live, nil
	}
	if live != nil {
		live.Stop()
		live = nil
	}
	dir := FindDir()
	if dir == "" {
		return nil, fmt.Errorf("python runtime not found (set CRUCIBLE_RUNTIME)")
	}
	if !HaveLangGraph() {
		return nil, fmt.Errorf("langgraph is not installed (pip install -r runtime/requirements.txt)")
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	pctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(pctx, "python3", "-m", "crucible_rt", "serve", "--addr", addr)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+dir)
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
			live = &Proc{URL: url, cmd: cmd, cancel: cancel}
			return live, nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	cancel()
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("python runtime did not become healthy on %s", url)
}

func CurrentStatus() Status {
	procMu.Lock()
	p := live
	procMu.Unlock()
	st := Status{LangGraph: HaveLangGraph()}
	if p != nil && healthy(p.URL) {
		st.Ready = true
		st.URL = p.URL
		st.ADK = probeADK(p.URL)
	}
	st.JS = HaveNode() && FindJSDir() != ""
	if !st.LangGraph {
		st.Error = "langgraph not installed"
	}
	return st
}

func probeADK(url string) bool {
	res, err := http.Get(url + "/v1/meta")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var body map[string]any
	if json.NewDecoder(res.Body).Decode(&body) != nil {
		return false
	}
	ok, _ := body["adk"].(bool)
	return ok
}

func healthy(url string) bool {
	if url == "" {
		return false
	}
	cli := &http.Client{Timeout: 200 * time.Millisecond}
	res, err := cli.Get(url + "/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == 200
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func drain(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			fmt.Fprintln(os.Stderr, "crucible-rt:", line)
		}
	}
}
