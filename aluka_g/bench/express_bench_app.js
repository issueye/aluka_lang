// express 并发性能基准 app
const express = require('express');
const app = express();
app.use(express.json());

// 简单 JSON 响应
app.get('/', (req, res) => {
	res.end(JSON.stringify({ hello: 'world' }));
});

// 路径参数
app.get('/echo/:word', (req, res) => {
	res.end('echo:' + req.params.word);
});

// 计算型（JSON 解析 + 循环）
app.post('/calc', (req, res) => {
	const n = req.body && req.body.n || 1000;
	let sum = 0;
	for (let i = 0; i < n; i++) sum += i;
	res.end(JSON.stringify({ sum }));
});

// 异步 Promise 聚合
app.get('/async', (req, res) => {
	let done = 0;
	const n = 50;
	const results = [];
	for (let i = 0; i < n; i++) {
		Promise.resolve(i).then((v) => {
			results.push(v);
			done++;
			if (done === n) res.end(JSON.stringify({ total: results.length }));
		});
	}
});

const port = Number(process.env.PORT || 3100);
const server = app.listen(port, () => console.log('bench app on ' + port));
