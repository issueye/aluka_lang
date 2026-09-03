// M3-5 fixture：fork 子模块——输出一行后退出。
// （fork 的 IPC send/message 通道未实现，这里用 stdout 验证子进程运行。）
process.stdout.write('fork-child-ok');
