package builtin

// Phase 3 第四批测试：node:child_process / node:worker_threads。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// TestChildProcessSpawnWindowsHide covers the lifecycle used by Pi's bash
// tool. Its close handler destroys both stdio streams before resolving the
// tool promise, so missing stream methods leave the tool pending forever.
func TestChildProcessSpawnWindowsHide(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows conhost regression")
	}
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var cp = require('node:child_process');
var child = cp.spawn('powershell.exe', ['-NoProfile', '-Command', 'Write-Output aluka-child'], {
  windowsHide: true,
  stdio: ['ignore', 'pipe', 'pipe']
});
var out = '';
var isBuffer = true;
var stdoutEnded = false;
child.stdout.on('data', function(d) {
  out += d.toString();
  if (!Buffer.isBuffer(d)) isBuffer = false;
});
child.stdout.on('end', function() { stdoutEnded = true; });
child.on('close', function(code) {
	child.stdout.destroy();
	child.stderr.destroy();
  globalThis.__r = code + ':' + out.trim() + ':' + isBuffer + ':' + stdoutEnded;
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "0:aluka-child:true:true" {
		t.Errorf("spawn Windows lifecycle = %q", got)
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

// TestChildProcessExecSequential verifies that multiple async tool-style
// commands can complete in the same event loop turn.
func TestChildProcessExecSequential(t *testing.T) {
	env := newHTTPEnv(t)
	err := env.runWithLoop(t, `
var cp = require('node:child_process');
cp.exec('echo first', function(firstErr, firstOut) {
  if (firstErr) throw firstErr;
  cp.exec('echo second', function(secondErr, secondOut) {
    if (secondErr) throw secondErr;
    globalThis.__r = firstOut.trim() + ':' + secondOut.trim();
  });
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := env.globalGet("__r"); got != "first:second" {
		t.Errorf("sequential exec = %q, want first:second", got)
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
