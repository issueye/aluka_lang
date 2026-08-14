import { React } from '../react.js';

export function Header({ title, subtitle, runtimeVersion }) {
  return (
    <header className="border-b border-slate-800 pb-6 mb-8 flex items-center justify-between">
      <div>
        <div className="flex items-center space-x-3">
          <div className="w-10 h-10 rounded-xl bg-sky-500 flex items-center justify-center font-bold text-slate-950 text-xl shadow-lg">
            ⚛
          </div>
          <div>
            <h1 className="text-3xl font-bold text-sky-400">{title}</h1>
            <p className="text-sm text-slate-400 font-sans mt-1">{subtitle}</p>
          </div>
        </div>
      </div>
      <div className="flex items-center space-x-3">
        <span className="px-3 py-1 bg-sky-950 border border-sky-500 text-sky-300 text-xs font-mono rounded-full">
          React 18 JSX
        </span>
        <span className="px-3 py-1 bg-emerald-950 border border-emerald-500 text-emerald-300 text-xs font-mono rounded-full">
          {runtimeVersion}
        </span>
      </div>
    </header>
  );
}
