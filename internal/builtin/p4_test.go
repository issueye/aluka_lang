package builtin

// Phase 3 第四批测试：node:child_process / node:worker_threads。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChildProcessSpawn 验证 spawn + stdout 收集 + exit。
func TestChildProcessSpawn(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var cp = require('node:child_process');
var child = cp.spawn('node', ['-e', 'console.log("child-out")']);
var out = '';
child.stdout.on('data', function(d) { out += d; });
child.on('exit', function(code) { globalThis.__r = code + ':' + out.trim(); });
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "0:child-out" {
		t.Errorf("spawn = %q, want 0:child-out", got)
	}
}

// TestChildProcessExec 验证 exec 回调（用平台内建命令 echo）。
func TestChildProcessExec(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var cp = require('node:child_process');
cp.exec('echo hello exec', function(err, stdout, stderr) {
  globalThis.__r = (err === null) + ':' + stdout.trim();
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "true:hello exec" {
		t.Errorf("exec = %q, want true:hello exec", got)
	}
}

// TestWorkerThreads 验证 worker 消息往返与 workerData。
func TestWorkerThreads(t *testing.T) {
	env := newHTTPEnv(t)
	dir := t.TempDir()
	childPath := filepath.Join(dir, "child.js")
	childSrc := `
var { parentPort, workerData } = require('node:worker_threads');
parentPort.on('message', function(m) {
  parentPort.postMessage(m + ':' + workerData.n);
});
`
	if err := os.WriteFile(childPath, []byte(childSrc), 0644); err != nil {
		t.Fatal(err)
	}
	childPathJS := strings.ReplaceAll(childPath, "\\", "/")

	code := fmt.Sprintf(`
var wt = require('node:worker_threads');
var w = new wt.Worker('%s', { workerData: { n: 42 } });
w.on('message', function(m) { globalThis.__msg = m; w.terminate(); });
w.on('error', function(e) { globalThis.__err = e; });
w.postMessage('ping');
`, childPathJS)
	if err := env.runWithLoop(t, code); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__msg"); got != "ping:42" {
		t.Errorf("worker message = %q, want ping:42", got)
	}
}

func TestWorkerThreadsCloneMarkers(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var wt = require('node:worker_threads');
var value = {};
globalThis.__r = typeof wt.markAsUncloneable + ':' +
  typeof wt.markAsUntransferable + ':' + (wt.markAsUncloneable(value) === undefined);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "function:function:true" {
		t.Errorf("worker clone markers = %q", got)
	}
}
