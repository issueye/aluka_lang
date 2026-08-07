// M6 O-6 回归：数组高阶方法回调语义（原生直调 NativeCallback 快路径后必须
// 与 node 22.23.1 逐行一致）。
//
// 覆盖面：
//   - 简单箭头回调（命中 O-6 Go 侧直执行）：恒等/字面量/二元算术位/比较/
//     单层属性读/嵌套组合（x => x.v % 3 === 0）
//   - 复杂回调（回退完整调用链）：多语句体/闭包捕获/this 使用/命名函数
//   - 参数序：(value, index, array) 三参完整传递；reduce 的 (acc, x, i, arr)
//   - 边界：空数组、单元素 reduce、无初始值 reduce、BigInt 回调、字符串强转
//   - 错误传播：回调抛异常 → 调用方收到异常（不是被吞掉/误转 undefined）
//   - 方法覆盖面：map/filter/forEach/reduce/reduceRight/find/findIndex/
//     some/every/flatMap/findLast/findLastIndex/sort/toSorted/Array.from
//
// 已知既有差异（非 O-6 引入，未在此覆盖）：稀疏数组 holes 与自定义
// Symbol.iterator 的数组子类化走通用算法——aluka 的 ArrayValue 稠密模型
// 与 node 不同，属另一独立缺陷面。

const src = [
  { v: 1, tag: "a" }, { v: 2, tag: "b" }, { v: 3, tag: "c" },
  { v: 4, tag: "d" }, { v: 5, tag: "e" }, { v: 6, tag: "f" },
];
const nums = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];

// --- 简单回调（命中原生直执行） ---
console.log("R1>" + nums.map(x => x * 2).join(","));
console.log("R2>" + nums.map(x => x + 1).join(","));
console.log("R3>" + nums.filter(x => x % 2 === 0).join(","));
console.log("R4>" + nums.filter(x => x > 5).join(","));
console.log("R5>" + nums.reduce((a, b) => a + b, 0));
console.log("R6>" + nums.reduce((a, b) => a * b, 1));
console.log("R7>" + nums.reduceRight((a, b) => a - b, 0));

// 属性读 + 嵌套组合
console.log("R8>" + src.map(x => x.v).join(","));
console.log("R9>" + src.filter(x => x.v % 3 === 0).map(x => x.tag).join(""));
console.log("R10>" + src.map(x => x.v * 10 + 1).join(","));

// 恒等 / 字面量 / 一元负
console.log("R11>" + nums.map(x => x).join(","));
console.log("R12>" + nums.map(x => 7).join(","));
console.log("R13>" + nums.map(x => -x).join(","));

// 比较回调（find/some/every 布尔谓词）
console.log("R14>" + nums.find(x => x > 3));
console.log("R15>" + nums.findIndex(x => x === 7));
console.log("R16>" + nums.some(x => x > 9));
console.log("R17>" + nums.every(x => x > 0));
console.log("R18>" + nums.findLast(x => x % 2 === 1));
console.log("R19>" + nums.findLastIndex(x => x % 2 === 0));

// forEach 副作用
let sum = 0;
nums.forEach(x => { sum += x; });
console.log("R20>" + sum);

// flatMap 展开
console.log("R21>" + nums.slice(0, 3).flatMap(x => [x, x * 10]).join(","));

// 位运算 / 位移
console.log("R22>" + nums.map(x => x << 1).join(","));
console.log("R23>" + nums.map(x => x & 3).join(","));
console.log("R24>" + nums.map(x => x | 8).join(","));

// 字符串 + 数字（binAdd 拼接语义）
console.log("R25>" + nums.slice(0, 3).map(x => "n" + x).join(","));
console.log("R26>" + nums.slice(0, 3).map(x => x + "").join(","));

// 双参数回调（value, index）
console.log("R27>" + nums.slice(0, 5).map((x, i) => i).join(","));
console.log("R28>" + nums.slice(0, 5).map((x, i) => x + i).join(","));

// --- 复杂回调（回退完整调用链，语义必须一致） ---
// 多语句体
console.log("R29>" + nums.map(x => { const y = x * 2; return y + 1; }).join(","));
// 闭包捕获
let k = 3;
console.log("R30>" + nums.slice(0, 3).map(x => x * k).join(","));
// this 使用（非箭头）+ thisArg
const obj = { mul: 10 };
console.log("R31>" + nums.slice(0, 3).map(function (x) { return x * this.mul; }, obj).join(","));
// 命名函数
console.log("R32>" + nums.slice(0, 3).filter(function isEven(x) { return x % 2 === 0; }).join(","));
// reduce 无初始值 / 单元素数组
console.log("R33>" + [5].reduce((a, b) => a + b));
console.log("R34>" + [].reduce((a, b) => a + b, 100));
console.log("R35>" + [7, 3, 9].reduce((a, b) => (a > b ? a : b)));

// 排序比较器（sort / toSorted）
console.log("R36>" + [5, 1, 4, 2, 3].sort((a, b) => a - b).join(","));
console.log("R37>" + [5, 1, 4, 2, 3].toSorted((a, b) => b - a).join(","));
console.log("R38>" + src.slice().sort((a, b) => b.v - a.v).map(x => x.tag).join(""));

// Array.from mapFn
console.log("R39>" + Array.from(nums.slice(0, 3), x => x * x).join(","));
console.log("R40>" + Array.from("abc", (c, i) => c + i).join(","));

// 三参回调：第三个参数是数组本身
console.log("R41>" + nums.slice(0, 3).map((x, i, arr) => arr.length).join(","));

// 字符串/数字混合比较回调
console.log("R42>" + ["10", "2", "1"].sort((a, b) => a - b).join(","));

// --- BigInt 回调 ---
console.log("R43>" + nums.slice(0, 3).map(x => BigInt(x) + 1n).join(","));

// --- 错误传播：回调抛异常须冒泡（原生直执行不能吞错） ---
try {
  nums.map(x => { throw new Error("boom-" + x); });
} catch (e) {
  console.log("R44>" + e.message);
}
// 原生快路径表达式中除法/取模错误值（不抛，走 NaN/Infinity）
console.log("R45>" + [0, 1, -1].map(x => 1 / x).join(","));
console.log("R46>" + [0, 3].map(x => 5 % x).join(","));

// reduce 空数组无初始值 → TypeError 消息
try {
  [].reduce((a, b) => a + b);
} catch (e) {
  console.log("R47>" + e.name);
}
