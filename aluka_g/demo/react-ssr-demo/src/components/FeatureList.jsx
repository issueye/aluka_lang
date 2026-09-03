import { React } from '../react.js';

export function FeatureList({ features }) {
  return (
    <div className="p-6 bg-slate-900 border border-slate-800 rounded-2xl shadow-lg mt-8">
      <div className="flex items-center justify-between pb-4 border-b border-slate-800 mb-4">
        <h2 className="text-xl font-bold text-slate-100 flex items-center space-x-2">
          <span>🚀</span>
          <span>React 18 & SSR 特性支持清单</span>
        </h2>
        <span className="text-xs font-mono text-emerald-400 bg-emerald-950 px-2.5 py-1 rounded-full border border-emerald-500">
          Pure Go Native VM
        </span>
      </div>

      <div className="space-y-3">
        {features.map(f => (
          <div key={f.id} className="p-4 bg-slate-950 border border-slate-800 rounded-xl flex items-start justify-between">
            <div>
              <div className="font-semibold text-sky-400 text-sm">{f.name}</div>
              <p className="text-xs text-slate-400 mt-1">{f.desc}</p>
            </div>
            <span className="px-2 py-0.5 text-xs font-mono rounded bg-slate-900 text-slate-300 border border-slate-700">
              {f.tag}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
