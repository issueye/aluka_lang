// 动态 import 目标：构建期拆为独立 chunk，浏览器按需加载。
const loadedAt = new Date().toISOString();

export function summary(source) {
  return 'chunk 加载成功：heavy-data @ ' + loadedAt + '\n来源：' + source;
}
