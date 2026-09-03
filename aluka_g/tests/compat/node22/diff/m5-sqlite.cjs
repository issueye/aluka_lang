// M5-4 diff：node:sqlite DatabaseSync CRUD/事务/参数绑定。
// 用 :memory: 数据库，避免文件残留。
// Node 首次加载 node:sqlite 会向 stderr 打印 ExperimentalWarning + 提示行
// （run-diff.sh 的 norm_output 过滤不掉 `(Use node --trace-warnings...)` 行），
// 这里在 require 之前静默该警告，保证两侧输出只有一行 JSON。
try {
  const origWrite = process.stderr.write.bind(process.stderr);
  process.stderr.write = (chunk, ...args) => {
    if (String(chunk).includes('ExperimentalWarning')) return true;
    return origWrite(chunk, ...args);
  };
} catch (e) { /* ignore */ }

const { DatabaseSync } = require('node:sqlite');
const results = {};

const db = new DatabaseSync(':memory:');

db.exec('CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, age INTEGER, score REAL, tags BLOB, active INTEGER)');
db.exec("INSERT INTO users (name, age, score, tags, active) VALUES ('alice', 30, 1.5, x'0102ff', 1)");

// get + 列类型。
const stmt = db.prepare('SELECT * FROM users WHERE name = ?');
const row = stmt.get('alice');
results.row = row.name + ':' + row.age + ':' + row.score + ':' + row.id;
results.tagsHex = Buffer.from(row.tags).toString('hex');

// run 的 changes / lastInsertRowid。
const ins = db.prepare('INSERT INTO users (name, age) VALUES (?, ?)');
const r1 = ins.run('bob', 25);
results.insert = r1.changes + ':' + r1.lastInsertRowid;
const r2 = ins.run('bob', 26);
results.insert2 = r2.lastInsertRowid;

// all。
const allStmt = db.prepare('SELECT name FROM users ORDER BY id');
results.names = allStmt.all().map((r) => r.name);

// 命名参数（@name / :name 两种前缀）。
results.namedAt = db.prepare('SELECT age FROM users WHERE name = @name').get({ name: 'bob' }).age;
results.namedColon = db.prepare('SELECT age FROM users WHERE name = :name').get({ name: 'bob' }).age;

// null 绑定。
db.prepare('INSERT INTO users (name, age) VALUES (?, ?)').run('carol', null);
results.nullAge = db.prepare('SELECT age FROM users WHERE name = ?').get('carol').age;

// boolean 绑定：Node 22 实测抛 TypeError（"cannot be bound"）。
try {
  db.prepare('INSERT INTO users (name, active) VALUES (?, ?)').run('zoe', true);
  results.boolBind = 'no-throw';
} catch (e) {
  results.boolBind = e.name;
}

// bigint 绑定与读取。
db.prepare('INSERT INTO users (name, age) VALUES (?, ?)').run('dave', 123n);
const daveStmt = db.prepare('SELECT age FROM users WHERE name = ?');
results.bigRead = daveStmt.get('dave').age;
daveStmt.setReadBigInts(true);
results.bigType = typeof daveStmt.get('dave').age;
results.bigVal = String(daveStmt.get('dave').age);

// 事务（COMMIT 生效 / ROLLBACK 回退）。
db.exec('BEGIN');
db.prepare('INSERT INTO users (name) VALUES (?)').run('eve');
db.exec('COMMIT');
results.txCommit = db.prepare('SELECT count(*) AS n FROM users').get().n;

db.exec('BEGIN');
db.prepare('INSERT INTO users (name) VALUES (?)').run('frank');
db.exec('ROLLBACK');
results.txRollback = db.prepare('SELECT count(*) AS n FROM users').get().n;

// iterate。
const it = db.prepare('SELECT name FROM users ORDER BY id').iterate();
const iterated = [];
let step = it.next();
while (!step.done) {
  iterated.push(step.value.name);
  step = it.next();
}
results.iterate = iterated.join(',');

// columns。
results.columns = db.prepare('SELECT id, name, age FROM users').columns().map((c) => c.name).join(',');

// isOpen / close。
results.isOpen = db.isOpen;
db.close();
results.isOpenAfter = db.isOpen;

process.stdout.write(JSON.stringify(results) + '\n');
