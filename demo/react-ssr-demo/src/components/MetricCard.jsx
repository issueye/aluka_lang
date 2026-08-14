import { React } from '../react.js';

export function MetricCard({ label, value, trend, icon, theme }) {
  const themeColors = {
    sky: 'border-sky-500 bg-sky-950 text-sky-400',
    indigo: 'border-indigo-500 bg-indigo-950 text-indigo-400',
    emerald: 'border-emerald-500 bg-emerald-950 text-emerald-400',
    purple: 'border-purple-500 bg-purple-950 text-purple-400'
  };

  const badgeClass = themeColors[theme] || themeColors.sky;

  return (
    <div className="p-6 bg-slate-900 border border-slate-800 rounded-2xl shadow-lg">
      <div className="flex items-center justify-between mb-3">
        <span className="text-xs font-mono text-slate-400">{label}</span>
        <span className="text-xl">{icon}</span>
      </div>
      <div className="text-3xl font-bold text-slate-100 font-mono mb-2">{value}</div>
      <div className="flex items-center justify-between text-xs pt-2 border-t border-slate-800">
        <span className="text-slate-400">Status</span>
        <span className={"px-2 py-0.5 rounded-full font-mono border " + badgeClass}>{trend}</span>
      </div>
    </div>
  );
}
