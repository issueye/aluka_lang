import { React } from '../react.js';
import { Header } from './Header.jsx';
import { MetricCard } from './MetricCard.jsx';
import { FeatureList } from './FeatureList.jsx';

export function App() {
  const metrics = [
    { label: 'ENGINE ARCHITECTURE', value: 'Pure Go VM', trend: '100% Native', icon: '⚡', theme: 'sky' },
    { label: 'JSX / TSX LOWERING', value: 'Ast-Transpile', trend: 'Registry Based', icon: '🧩', theme: 'indigo' },
    { label: 'SSR SPEED (VNODE)', value: '< 0.8 ms', trend: 'Ultra Fast', icon: '🔥', theme: 'emerald' },
    { label: 'CSS STYLING JIT', value: 'Tailwind JIT', trend: 'Instant Scan', icon: '🎨', theme: 'purple' }
  ];

  const features = [
    { id: 'f1', name: 'JSX Element & Fragment Parsing', desc: '原生源码级 JSX 标签与 <>...</> 片段解析，无额外预构建编译开销', tag: 'Parser Plugin' },
    { id: 'f2', name: 'React 18 Component & Props Lowering', desc: '自动 Lowering 为 React.createElement，完美支持大写自定义组件与嵌套传参', tag: 'Compiler AST' },
    { id: 'f3', name: 'Destructuring & Spread Attributes', desc: '属性解构、命名属性、布尔属性与 {...props} 展开运算', tag: 'Object Spread' },
    { id: 'f4', name: 'Server-Side Rendering (SSR)', desc: '高性能递归 HTML 序列化，支持样式对象转换与 HTML 实体转义', tag: 'ReactDOMServer' }
  ];

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100 font-sans p-8">
      <div className="max-w-5xl mx-auto">
        <Header 
          title="Aluka React SSR & Tailwind JIT"
          subtitle="A Pure Go JavaScript Runtime with Native JSX/TSX & Server-Side Rendering"
          runtimeVersion="v0.1.0-purego"
        />

        <div className="grid grid-cols-4 gap-4">
          {metrics.map(m => (
            <MetricCard
              key={m.label}
              label={m.label}
              value={m.value}
              trend={m.trend}
              icon={m.icon}
              theme={m.theme}
            />
          ))}
        </div>

        <FeatureList features={features} />

        <footer className="mt-8 pt-6 border-t border-slate-800 text-xs text-slate-500 flex items-center justify-between">
          <span>Powered by Aluka Lang • Pure Go Runtime</span>
          <span className="font-mono text-sky-400">Node / Bun API Compatible</span>
        </footer>
      </div>
    </div>
  );
}

export default App;
