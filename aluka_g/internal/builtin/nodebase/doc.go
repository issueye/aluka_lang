// Package nodebase 是 Node 内置模块各领域子包的共享基座。
//
// 这里只放被多个领域包共用的、无业务归属的底层 helper：参数取值、值比较、
// 错误码载体、JSON 互转、Promise 驱动、事件触发。它不 import 任何
// builtin 子包，因此始终位于依赖图的最底层（无环由构造保证）。
//
// 判断某个 helper 该不该进 nodebase：若它只被单一领域使用，留在该领域包内；
// 只有跨两个及以上领域复用时才提到这里。
package nodebase
