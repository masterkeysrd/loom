import { useState, useEffect } from 'react';
import { Network, Server, Play, BrainCircuit, MessageSquare, Code, Terminal, Activity } from 'lucide-react';
import type { Manifest, GraphManifest } from '../types';
import { Mermaid, DetailBadge } from '../components';

export function Playground() {
  const [manifests, setManifests] = useState<Manifest[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedGraph, setSelectedGraph] = useState<GraphManifest | null>(null);

  useEffect(() => {
    fetch('/api/manifests')
      .then(res => res.json())
      .then(data => {
        setManifests(data || []);
        if (data && data.length > 0 && data[0].graphs.length > 0) {
          setSelectedGraph(data[0].graphs[0]);
        }
        setLoading(false);
      });
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin w-8 h-8 border-4 border-indigo-500 border-t-transparent rounded-full" />
      </div>
    );
  }

  return (
    <div className="h-full flex overflow-hidden">
      {/* Sidebar - Discovery Registry */}
      <div className="w-80 border-r border-slate-200 bg-white flex flex-col shrink-0">
        <div className="p-6 border-b border-slate-200">
          <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest mb-1">Worker Discovery</div>
          <h2 className="text-xl font-black text-slate-900 tracking-tight">Active Manifests</h2>
        </div>
        
        <div className="flex-1 overflow-auto p-4 space-y-6">
          {manifests.length === 0 ? (
            <div className="text-center py-12 px-4">
              <Network size={32} className="mx-auto text-slate-300 mb-4" />
              <p className="text-sm font-medium text-slate-500 leading-relaxed">
                No active workers detected. Connect a Go app to begin.
              </p>
            </div>
          ) : (
            manifests.map(manifest => (
              <div key={manifest.worker_id} className="space-y-3">
                <div className="flex items-center gap-2 px-2 py-1 bg-slate-50 rounded-lg border border-slate-100">
                  <Server size={14} className="text-slate-400" />
                  <span className="text-[10px] font-black text-slate-500 uppercase tracking-tighter truncate">
                    Worker {manifest.worker_id.substring(0, 8)}
                  </span>
                </div>
                <div className="space-y-2">
                  {manifest.graphs.map(graph => (
                    <button
                      key={graph.id}
                      onClick={() => setSelectedGraph(graph)}
                      className={`w-full text-left p-4 rounded-2xl border transition-all duration-200 group relative overflow-hidden ${
                        selectedGraph?.id === graph.id
                          ? 'bg-indigo-50 border-indigo-200 ring-1 ring-indigo-100'
                          : 'bg-white border-slate-100 hover:border-slate-200 hover:bg-slate-50'
                      }`}
                    >
                      {selectedGraph?.id === graph.id && (
                        <div className="absolute top-0 left-0 w-1 h-full bg-indigo-500" />
                      )}
                      <div className="flex items-center gap-3">
                        <BrainCircuit className={selectedGraph?.id === graph.id ? 'text-indigo-600' : 'text-slate-500'} size={18} />
                        <div className="min-w-0">
                          <div className={`text-sm font-bold tracking-tight truncate ${selectedGraph?.id === graph.id ? 'text-indigo-900' : 'text-slate-700'}`}>
                            {graph.name}
                          </div>
                          <div className="text-[9px] font-black text-slate-400 uppercase tracking-widest mt-0.5">
                            {graph.commands.length} Commands
                          </div>
                        </div>
                      </div>
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Main Panel - Visualization & Interaction */}
      <div className="flex-1 bg-[#F8FAFC] overflow-hidden flex flex-col">
        {selectedGraph ? (
          <>
            <header className="p-8 bg-white border-b border-slate-200 flex items-center justify-between">
              <div>
                <div className="flex items-center gap-3 mb-1">
                  <BrainCircuit className="text-indigo-600" size={24} />
                  <h1 className="text-3xl font-black text-slate-900 tracking-tight">{selectedGraph.name}</h1>
                </div>
                <p className="text-slate-500 text-sm font-medium">Topology Visualization & Dynamic Controls</p>
              </div>
              <div className="flex gap-4">
                <DetailBadge label="Commands" value={selectedGraph.commands.length} icon={<Terminal size={12} />} color="slate" />
                <DetailBadge label="Type" value="Graph" icon={<Activity size={12} />} color="indigo" />
                <button className="flex items-center gap-2 px-6 py-3 bg-indigo-600 text-white rounded-2xl text-sm font-black shadow-lg shadow-indigo-500/20 hover:bg-indigo-700 transition-all active:scale-95">
                  <Play size={16} fill="currentColor" /> RUN GRAPH
                </button>
              </div>
            </header>

            <div className="flex-1 flex overflow-hidden">
              {/* Topology Panel */}
              <div className="flex-1 overflow-auto p-8 border-r border-slate-200 bg-white">
                <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest mb-6">
                  <Network size={14} />
                  Graph Topology
                </div>
                <div className="flex items-center justify-center min-h-[400px] border-2 border-dashed border-slate-100 rounded-3xl p-8 bg-slate-50/30">
                  <Mermaid chart={selectedGraph.mermaid_diagram} />
                </div>
              </div>

              {/* Controls Panel */}
              <div className="w-[480px] overflow-auto p-8 space-y-8 bg-[#F8FAFC]">
                <div className="space-y-4">
                  <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
                    <MessageSquare size={14} />
                    Schema Discovery
                  </div>
                  <div className="bg-white rounded-3xl border border-slate-200 shadow-sm p-6 space-y-6">
                    <div>
                      <div className="text-xs font-bold text-slate-900 mb-2">Input Schema (State)</div>
                      <pre className="text-[10px] bg-slate-900 text-slate-300 p-4 rounded-2xl font-mono leading-relaxed overflow-auto max-h-48">
                        {JSON.stringify(selectedGraph.input_schema, null, 2)}
                      </pre>
                    </div>
                    {selectedGraph.commands.length > 0 && (
                      <div className="space-y-4 pt-4 border-t border-slate-100">
                        <div className="text-xs font-bold text-slate-900">Available Commands</div>
                        {selectedGraph.commands.map(cmd => (
                          <div key={cmd.name} className="p-4 bg-slate-50 rounded-2xl border border-slate-100">
                            <div className="flex items-center gap-2 mb-2">
                              <Code size={14} className="text-indigo-500" />
                              <span className="text-xs font-black text-slate-700">{cmd.name}</span>
                            </div>
                            <pre className="text-[9px] text-slate-500 font-mono overflow-auto max-h-32">
                              {JSON.stringify(cmd.schema, null, 2)}
                            </pre>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </div>
          </>
        ) : (
          <div className="flex-1 flex flex-col items-center justify-center space-y-4 p-8 text-center">
            <div className="w-24 h-24 bg-slate-100 rounded-full flex items-center justify-center text-slate-300">
              <BrainCircuit size={48} />
            </div>
            <div>
              <h3 className="text-xl font-black text-slate-900">No Graph Selected</h3>
              <p className="text-slate-500 text-sm font-medium mt-1">Select a discovered graph from the registry to begin.</p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
