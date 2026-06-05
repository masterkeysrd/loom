import { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { format } from 'date-fns';
import { BrainCircuit, AlertCircle, Clock, RefreshCcw, ChevronRight } from 'lucide-react';
import type { Thread } from '../types';

export function ThreadsList({ searchQuery }: { searchQuery: string }) {
  const [threads, setThreads] = useState<Thread[]>([]);
  const [loading, setLoading] = useState(false);

  const fetchThreads = useCallback(() => {
    setLoading(true);
    const url = searchQuery ? `/api/threads?q=${encodeURIComponent(searchQuery)}` : '/api/threads';
    fetch(url).then(res => res.json()).then(data => {
      if (data) setThreads(data);
      setLoading(false);
    });
  }, [searchQuery]);

  useEffect(() => {
    Promise.resolve().then(() => fetchThreads());
  }, [fetchThreads]);

  return (
    <div className="p-8 max-w-7xl mx-auto space-y-8 animate-in slide-in-from-bottom-4 duration-500">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-3xl font-bold tracking-tight text-slate-900">Trace Explorer</h2>
          <p className="text-slate-500 mt-1">Audit every decision, tool call, and token across your agents.</p>
        </div>
        <div className="flex items-center gap-3">
          <button className="flex items-center gap-2 px-4 py-2 bg-white border border-slate-200 rounded-xl text-sm font-bold text-slate-600 hover:bg-slate-50 transition-colors shadow-sm">
            <Clock size={16} /> Last 24 Hours
          </button>
          <button 
            onClick={fetchThreads}
            className="p-2 bg-white border border-slate-200 rounded-xl hover:bg-slate-50 transition-colors shadow-sm active:scale-95 group"
          >
            <RefreshCcw size={18} className={`text-slate-500 group-hover:text-indigo-600 transition-colors ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </header>

      <div className="bg-white rounded-3xl shadow-sm border border-slate-200 overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="bg-slate-50 border-b border-slate-200 text-left">
              <th className="px-8 py-5 text-[10px] font-black text-slate-500 uppercase tracking-widest">Execution ID</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-500 uppercase tracking-widest">Graph Identity</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-500 uppercase tracking-widest text-center">LLM Calls</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-500 uppercase tracking-widest text-center">Tokens</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-500 uppercase tracking-widest text-right">Timestamp</th>
              <th className="px-8 py-5"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {threads.map(thread => (
              <tr key={thread.thread_id} className="hover:bg-indigo-50/20 transition-colors group">
                <td className="px-8 py-5">
                  <Link to={`/threads/${thread.thread_id}`} className="text-indigo-600 font-bold hover:underline font-mono text-sm tracking-tight">
                    {thread.thread_id.substring(0, 18)}...
                  </Link>
                </td>
                <td className="px-8 py-5">
                  <div className="flex items-center gap-3">
                    <div className={`w-8 h-8 rounded-full flex items-center justify-center ${thread.has_error ? 'bg-rose-100' : 'bg-indigo-100'}`}>
                      {thread.has_error ? <AlertCircle size={16} className="text-rose-600" /> : <BrainCircuit size={16} className="text-indigo-600" />}
                    </div>
                    <div className="flex flex-col">
                      <span className="font-semibold text-slate-700">{thread.graph_name}</span>
                      {thread.has_error && <span className="text-[9px] font-black text-rose-500 uppercase tracking-widest">Execution Failed</span>}
                    </div>
                  </div>
                </td>
                <td className="px-8 py-5 text-center">
                  <span className="px-3 py-1 bg-slate-100 rounded-full text-xs font-bold text-slate-600">{thread.invocation_count}</span>
                </td>
                <td className="px-8 py-5 text-center">
                  <span className="text-sm font-semibold text-slate-500 tracking-tighter">{(thread.total_tokens / 1000).toFixed(1)}k</span>
                </td>
                <td className="px-8 py-5 text-right text-sm text-slate-500 font-medium">
                  {format(new Date(thread.start_time / 1000000), 'MMM d, HH:mm:ss')}
                </td>
                <td className="px-8 py-5 text-right opacity-0 group-hover:opacity-100 transition-opacity">
                  <Link to={`/threads/${thread.thread_id}`} className="p-2 hover:bg-white rounded-lg inline-block shadow-sm border border-slate-200">
                    <ChevronRight size={18} className="text-indigo-600" />
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
