// M7-5 diff：package 生态 —— exports 条件（require/import/node/default）、
// 子路径、通配、package.json imports（精确 + 通配）。fixtures 见 m7-fixtures/pkgapp。
const path = require('node:path');
// 加载 fixture 包应用（其内部 require('condpkg') / require('#alias') 从
// m7-fixtures/pkgapp 目录解析）。
require('./m7-fixtures/pkgapp/main.cjs');
