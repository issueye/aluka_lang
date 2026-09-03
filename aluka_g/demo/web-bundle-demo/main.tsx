import * as React from './react.ts';
import { Card } from './components/Card.tsx';
import { formatTime } from './lib/format.ts';

export const version = '1.0.0';

export function render() {
  const tree = <Card title={'Aluka Web Bundle ' + version} tag="demo">
    <p>纯 Go 打包器：TSX 组件 + 静态依赖单文件拼接</p>
    <p>构建时间：{formatTime()}</p>
  </Card>;
  return React.renderToString(tree);
}

export async function loadStats() {
  const stats = await import('./lib/heavy-stats.ts');
  return stats.summary();
}

// 浏览器环境自动挂载（Node 导入产物时跳过 DOM）。
if (typeof document !== 'undefined') {
  const app = document.getElementById('app');
  app.innerHTML = render();
  const btn = document.getElementById('load-stats');
  btn.onclick = async () => {
    const out = document.getElementById('stats');
    out.textContent = await loadStats();
  };
}
