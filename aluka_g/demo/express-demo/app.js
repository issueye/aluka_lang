// 真实 express 稳定性验证 demo
const express = require('express');

const app = express();
app.use(express.json());

// 记录中间件调用次数，验证闭包/状态保持
let reqCount = 0;

app.get('/', (req, res) => {
  reqCount++;
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({ hello: 'aluka', count: reqCount, path: req.path }));
});

app.get('/echo/:word', (req, res) => {
  res.end('echo:' + req.params.word);
});

app.post('/json', (req, res) => {
  res.end(JSON.stringify({ received: req.body, n: req.body ? req.body.x : null }));
});

// 500 次并发请求的稳定性测试
app.get('/load', (req, res) => {
  const n = 500;
  let done = 0;
  const results = [];
  for (let i = 0; i < n; i++) {
    Promise.resolve(i).then((v) => {
      results.push(v);
      done++;
      if (done === n) {
        res.end(JSON.stringify({ total: results.length, sum: results.reduce((a, b) => a + b, 0) }));
      }
    });
  }
});

const server = app.listen(3000, () => {
  console.log('express demo listening on http://127.0.0.1:3000');
});

// 5 分钟后自动关闭，方便自动化验证进程能正常退出
setTimeout(() => {
  server.close(() => {
    console.log('server closed gracefully');
    process.exit(0);
  });
}, 300000);