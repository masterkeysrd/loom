import { useState, useEffect, useMemo } from 'react';
import { BrowserRouter, Routes, Route, Link, useParams, useLocation } from 'react-router-dom';
import { 
  LayoutDashboard, 
  List, 
  Activity, 
  Cpu, 
  Coins, 
  Clock, 
  AlertCircle, 
  ChevronRight, 
  Code,
  Terminal,
  BrainCircuit,
  Settings,
  Search,
  Globe,
  Wrench
} from 'lucide-react';
import { 
  XAxis, 
  YAxis, 
  CartesianGrid, 
  Tooltip, 
  ResponsiveContainer,
  AreaChart,
  Area
} from 'recharts';
import { format } from 'date-fns';

function App() {
  const [searchQuery, setSearchQuery] = useState('');

  return (
    <BrowserRouter>
      <div className="flex h-screen bg-[#F8FAFC] text-slate-900 w-full overflow-hidden font-sans selection:bg-indigo-100 selection:text-indigo-700">
        {/* Sidebar */}
        <div className="w-64 bg-slate-900 border-r border-slate-800 flex flex-col shrink-0">
          <div className="p-6 border-b border-slate-800 mb-4">
            <h1 className="text-xl font-black text-white flex items-center gap-3 tracking-tight">
              <div className="bg-indigo-500 p-1.5 rounded-lg shadow-lg shadow-indigo-500/20">
                <BrainCircuit className="w-6 h-6 text-white" />
              </div>
              LOOM <span className="text-indigo-400 font-light italic">Studio</span>
            </h1>
          </div>
          
          <nav className="flex-1 px-4 space-y-1">
            <SidebarLink to="/" icon={<LayoutDashboard size={18} />} label="Overview" />
            <SidebarLink to="/threads" icon={<List size={18} />} label="Trace Explorer" />
          </nav>

          <div className="p-4 mt-auto border-t border-slate-800">
            <div className="flex items-center gap-3 px-3 py-2 text-slate-400 text-xs">
              <Settings size={14} />
              <span>Version 1.0.0</span>
            </div>
          </div>
        </div>

        {/* Main Content */}
        <div className="flex-1 flex flex-col overflow-hidden">
          <header className="h-16 bg-white border-b border-slate-200 flex items-center px-8 shrink-0">
            <div className="relative w-96 group">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400 group-focus-within:text-indigo-500 transition-colors" size={16} />
              <input 
                type="text" 
                placeholder="Quick search traces..." 
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full bg-slate-50 border border-slate-200 rounded-full py-2 pl-10 pr-4 text-sm outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500 transition-all"
              />
            </div>
          </header>

          <main className="flex-1 overflow-auto">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/threads" element={<ThreadsList searchQuery={searchQuery} />} />
              <Route path="/threads/:threadId" element={<ThreadDetail />} />
            </Routes>
          </main>
        </div>
      </div>
    </BrowserRouter>
  );
}

function SidebarLink({ to, icon, label }: { to: string, icon: React.ReactNode, label: string }) {
  const location = useLocation();
  const isActive = location.pathname === to || (to !== '/' && location.pathname.startsWith(to));
  
  return (
    <Link 
      to={to} 
      className={`flex items-center gap-3 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all duration-200 ${
        isActive 
          ? 'bg-indigo-500/10 text-indigo-400' 
          : 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200'
      }`}
    >
      {icon}
      {label}
    </Link>
  );
}

function Dashboard() {
  const [stats, setStats] = useState<any>(null);
  const [metrics, setMetrics] = useState<any[]>([]);

  useEffect(() => {
    fetch('/api/stats').then(res => res.json()).then(setStats);
    
    const importantMetrics = [
      'gen_ai.client.token.usage', 
      'gen_ai.client.operation.duration',
      'loom.graph.execution.duration',
      'loom.graph.node.duration'
    ];
    Promise.all(
      importantMetrics.map(name =>
        fetch(`/api/metrics/${name}`).then(res => {
          if (!res.ok) return null;
          return res.json().then(points => ({ name, points }));
        })
      )
    ).then(results => {
      setMetrics(results.filter((r: any) => r && r.points && r.points.length > 0));
    });
  }, []);

  return (
    <div className="p-8 max-w-7xl mx-auto space-y-8 animate-in fade-in duration-500">
      <header>
        <h2 className="text-3xl font-bold tracking-tight text-slate-900">System Overview</h2>
        <p className="text-slate-500 mt-1">Real-time health and performance across your agent swarm.</p>
      </header>

      {/* Summary Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        <StatCard 
          icon={<Cpu className="text-indigo-600" />} 
          label="Total Threads" 
          value={stats?.total_threads || 0} 
          trend="Active" 
          color="indigo"
        />
        <StatCard 
          icon={<Activity className="text-emerald-600" />} 
          label="Total Spans" 
          value={stats?.total_spans || 0} 
          trend="Live" 
          color="emerald"
        />
        <StatCard 
          icon={<Coins className="text-amber-600" />} 
          label="Token Consumption" 
          value={stats?.total_tokens ? stats.total_tokens.toLocaleString() : 0} 
          trend="Total" 
          color="amber"
        />
        <StatCard 
          icon={<AlertCircle className="text-rose-600" />} 
          label="Errors" 
          value={stats?.error_count || 0} 
          trend={stats?.error_count > 0 ? "Issues" : "Healthy"} 
          color="rose"
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {metrics.map(metric => {
          // Group data by attributes (model or node name)
          const seriesKey = metric.name.includes('gen_ai') ? 'gen_ai.request.model' : 'loom.node.name';
          const timeMap = new Map<string, any>();
          const seriesNames = new Set<string>();

          metric.points.forEach((p: any) => {
            const time = format(new Date(p.timestamp_nano / 1000000), 'HH:mm:ss');
            const name = p.attributes[seriesKey] || 'default';
            seriesNames.add(name);
            
            if (!timeMap.has(time)) {
              timeMap.set(time, { time });
            }
            const entry = timeMap.get(time);
            entry[name] = (entry[name] || 0) + p.value;
          });

          const chartData = Array.from(timeMap.values()).sort((a, b) => a.time.localeCompare(b.time));
          const sortedSeries = Array.from(seriesNames).sort();
          const colors = ['#6366F1', '#EC4899', '#10B981', '#F59E0B', '#64748B'];

          return (
            <div key={metric.name} className="bg-white p-8 rounded-2xl shadow-sm border border-slate-200 hover:border-indigo-100 transition-colors group">
              <div className="flex items-center justify-between mb-8">
                <div>
                  <h3 className="text-sm font-bold text-slate-500 uppercase tracking-widest group-hover:text-indigo-600 transition-colors">
                    {metric.name.replace(/\./g, ' ')}
                  </h3>
                  <div className="flex gap-2 mt-2">
                    {sortedSeries.map((name, i) => (
                      <span key={name} className="flex items-center gap-1 text-[10px] font-bold text-slate-400">
                        <div className="w-2 h-2 rounded-full" style={{ backgroundColor: colors[i % colors.length] }} />
                        {name}
                      </span>
                    ))}
                  </div>
                </div>
                <div className="p-2 bg-slate-50 rounded-lg group-hover:bg-indigo-50 transition-colors">
                  <Activity size={16} className="text-slate-400 group-hover:text-indigo-500" />
                </div>
              </div>
              <div className="h-72">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={chartData}>
                    <defs>
                      {sortedSeries.map((name, i) => (
                        <linearGradient key={name} id={`color-${name}`} x1="0" y1="0" x2="0" y2="1">
                          <stop offset="5%" stopColor={colors[i % colors.length]} stopOpacity={0.1}/>
                          <stop offset="95%" stopColor={colors[i % colors.length]} stopOpacity={0}/>
                        </linearGradient>
                      ))}
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#F1F5F9" />
                    <XAxis dataKey="time" stroke="#94A3B8" fontSize={11} tickLine={false} axisLine={false} />
                    <YAxis stroke="#94A3B8" fontSize={11} tickLine={false} axisLine={false} />
                    <Tooltip 
                      contentStyle={{ borderRadius: '16px', border: 'none', boxShadow: '0 20px 25px -5px rgb(0 0 0 / 0.1)', padding: '12px' }}
                    />
                    {sortedSeries.map((name, i) => (
                      <Area 
                        key={name}
                        type="monotone" 
                        dataKey={name} 
                        stroke={colors[i % colors.length]} 
                        strokeWidth={3} 
                        fillOpacity={1} 
                        fill={`url(#color-${name})`} 
                        stackId={metric.name.includes('usage') ? "1" : undefined}
                      />
                    ))}
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function StatCard({ icon, label, value, trend, color }: any) {
  return (
    <div className="bg-white p-6 rounded-2xl shadow-sm border border-slate-200 relative overflow-hidden group hover:shadow-md transition-all duration-300">
      <div className={`absolute top-0 right-0 w-24 h-24 bg-${color}-50 rounded-full -mr-8 -mt-8 opacity-50 group-hover:scale-110 transition-transform`}></div>
      <div className="relative z-10 space-y-4">
        <div className="p-2 bg-slate-50 w-fit rounded-xl group-hover:scale-110 transition-transform">
          {icon}
        </div>
        <div>
          <p className="text-sm font-medium text-slate-500 tracking-tight">{label}</p>
          <div className="flex items-baseline gap-2 mt-1">
            <h4 className="text-2xl font-black text-slate-900">{value}</h4>
            <span className={`text-xs font-bold px-1.5 py-0.5 rounded bg-${color}-50 text-${color}-600`}>{trend}</span>
          </div>
        </div>
      </div>
    </div>
  );
}

function ThreadsList({ searchQuery }: { searchQuery: string }) {
  const [threads, setThreads] = useState<any[]>([]);

  useEffect(() => {
    const url = searchQuery ? `/api/threads?q=${encodeURIComponent(searchQuery)}` : '/api/threads';
    fetch(url).then(res => res.json()).then(data => {
      if (data) setThreads(data);
    });
  }, [searchQuery]);

  return (
    <div className="p-8 max-w-7xl mx-auto space-y-8 animate-in slide-in-from-bottom-4 duration-500">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-3xl font-bold tracking-tight text-slate-900">Trace Explorer</h2>
          <p className="text-slate-500 mt-1">Audit every decision, tool call, and token across your agents.</p>
        </div>
        <button className="flex items-center gap-2 px-4 py-2 bg-white border border-slate-200 rounded-xl text-sm font-bold text-slate-600 hover:bg-slate-50 transition-colors">
          <Clock size={16} /> Last 24 Hours
        </button>
      </header>

      <div className="bg-white rounded-3xl shadow-sm border border-slate-200 overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="bg-slate-50 border-b border-slate-200 text-left">
              <th className="px-8 py-5 text-[10px] font-black text-slate-400 uppercase tracking-widest">Execution ID</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-400 uppercase tracking-widest">Graph Identity</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-400 uppercase tracking-widest text-center">Invocations</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-400 uppercase tracking-widest text-center">Tokens</th>
              <th className="px-8 py-5 text-[10px] font-black text-slate-400 uppercase tracking-widest text-right">Timestamp</th>
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
                    <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center">
                      <BrainCircuit size={16} className="text-indigo-600" />
                    </div>
                    <span className="font-semibold text-slate-700">{thread.graph_name}</span>
                  </div>
                </td>
                <td className="px-8 py-5 text-center">
                  <span className="px-3 py-1 bg-slate-100 rounded-full text-xs font-bold text-slate-600">{thread.trace_count}</span>
                </td>
                <td className="px-8 py-5 text-center">
                  <span className="text-sm font-semibold text-slate-500 tracking-tighter">{(thread.total_tokens / 1000).toFixed(1)}k</span>
                </td>
                <td className="px-8 py-5 text-right text-sm text-slate-400 font-medium">
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

function ThreadDetail() {
  const { threadId } = useParams();
  const [spans, setSpans] = useState<any[]>([]);
  const [selectedSpanId, setSelectedSpanId] = useState<string | null>(null);

  useEffect(() => {
    fetch(`/api/threads/${threadId}`).then(res => res.json()).then(data => {
      if (data) {
        setSpans(data);
        if (data.length > 0) setSelectedSpanId(data[0].span_id);
      }
    });
  }, [threadId]);

  const selectedSpan = useMemo(() => spans.find(s => s.span_id === selectedSpanId), [spans, selectedSpanId]);

  if (spans.length === 0) {
    return <div className="p-12 text-center space-y-4">
      <div className="animate-spin w-8 h-8 border-4 border-indigo-500 border-t-transparent rounded-full mx-auto" />
      <p className="text-slate-500 font-medium">Assembling trace waterfall...</p>
    </div>;
  }

  const minTime = Math.min(...spans.map(s => s.start_time_nano));
  const maxTime = Math.max(...spans.map(s => s.end_time_nano));
  const totalDuration = maxTime - minTime;

  const childrenMap = new Map<string, any[]>();
  spans.forEach(s => {
    if (s.parent_span_id && s.parent_span_id !== "0000000000000000") {
      if (!childrenMap.has(s.parent_span_id)) childrenMap.set(s.parent_span_id, []);
      childrenMap.get(s.parent_span_id)!.push(s);
    }
  });

  const renderSpanLine = (span: any, depth: number) => {
    const startOffset = span.start_time_nano - minTime;
    const duration = span.end_time_nano - span.start_time_nano;
    const leftPercent = totalDuration > 0 ? (startOffset / totalDuration) * 100 : 0;
    const widthPercent = totalDuration > 0 ? (duration / totalDuration) * 100 : 100;
    const isSelected = selectedSpanId === span.span_id;

    let colorClass = "bg-indigo-500";
    let icon = <ChevronRight size={14} />;
    
    const opName = span.attributes['gen_ai.operation.name'];
    
    if (opName === 'execute_tool' || span.name.includes("execute_tool") || span.attributes['gen_ai.tool.name']) {
      colorClass = "bg-violet-600";
      icon = <Wrench size={14} />;
    } else if (opName) {
      colorClass = "bg-fuchsia-500";
      icon = <BrainCircuit size={14} />;
    } else if (span.name.startsWith("HTTP") || span.attributes['http.method']) {
      colorClass = "bg-blue-500";
      icon = <Globe size={14} />;
    } else if (span.name.includes("memory")) {
      colorClass = "bg-amber-500";
      icon = <Activity size={14} />;
    } else if (span.status_code === "Error") {
      colorClass = "bg-rose-500";
      icon = <AlertCircle size={14} />;
    }

    const children = childrenMap.get(span.span_id) || [];

    return (
      <div key={span.span_id}>
        <div 
          onClick={() => setSelectedSpanId(span.span_id)}
          className={`flex items-center cursor-pointer border-l-4 transition-all duration-200 ${
            isSelected ? 'bg-indigo-50 border-indigo-600' : 'border-transparent hover:bg-slate-50'
          }`}
          style={{ paddingLeft: `${depth * 1.2}rem` }}
        >
          <div className="w-[40%] flex items-center gap-3 py-3 px-4 truncate">
            <div className={`p-1.5 rounded-lg ${colorClass} text-white shrink-0`}>
              {icon}
            </div>
            <span className={`text-sm tracking-tight ${isSelected ? 'font-black text-slate-900' : 'font-semibold text-slate-600'}`}>
              {span.name}
            </span>
          </div>
          <div className="w-[60%] px-8 relative h-10 flex items-center">
            <div className="absolute left-0 right-0 h-px bg-slate-100"></div>
            <div 
              className={`absolute h-4 rounded-full ${colorClass} opacity-80 shadow-sm shadow-black/5`}
              style={{ 
                left: `${leftPercent}%`, 
                width: `${Math.max(0.5, widthPercent)}%`,
                minWidth: '6px'
              }}
            >
              {widthPercent > 10 && (
                <div className="absolute top-full mt-1 left-0 text-[9px] font-black text-slate-400 whitespace-nowrap">
                  {(duration / 1000000).toFixed(1)}ms
                </div>
              )}
            </div>
          </div>
        </div>
        {children.sort((a,b) => a.start_time_nano - b.start_time_nano).map(child => renderSpanLine(child, depth + 1))}
      </div>
    );
  };

  const topLevelSpans = spans.filter(s => !s.parent_span_id || s.parent_span_id === "0000000000000000" || !spans.find(p => p.span_id === s.parent_span_id));

  return (
    <div className="h-full flex flex-col overflow-hidden animate-in fade-in duration-700">
      <div className="p-8 border-b border-slate-200 bg-white shadow-sm shrink-0 flex justify-between items-end">
        <div>
          <nav className="flex text-[10px] font-black text-slate-400 mb-2 gap-2 uppercase tracking-widest">
            <Link to="/threads" className="hover:text-indigo-600 transition-colors">Explorer</Link>
            <ChevronRight size={10} className="mt-0.5" />
            <span className="text-slate-900">{threadId}</span>
          </nav>
          <h2 className="text-4xl font-black text-slate-900 tracking-tighter">Trace Waterfall</h2>
        </div>
        <div className="flex gap-4">
          <DetailBadge label="Total Time" value={`${(totalDuration / 1000000).toFixed(2)}ms`} icon={<Clock size={12} />} color="indigo" />
          <DetailBadge label="Node Depth" value={Math.max(...spans.map(s => s.name.split('.').length))} icon={<List size={12} />} color="slate" />
        </div>
      </div>

      <div className="flex-1 flex overflow-hidden">
        {/* Waterfall Pane */}
        <div className="flex-1 overflow-auto border-r border-slate-200 bg-white scrollbar-thin scrollbar-thumb-slate-200">
          <div className="sticky top-0 bg-slate-50/80 backdrop-blur-sm z-20 flex py-2 px-4 border-b border-slate-200 text-[10px] font-black text-slate-400 uppercase tracking-widest">
            <div className="w-[40%]">Decision Tree</div>
            <div className="w-[60%] pl-8">Temporal Alignment</div>
          </div>
          <div className="pb-24">
            {topLevelSpans.sort((a,b) => a.start_time_nano - b.start_time_nano).map(s => renderSpanLine(s, 0))}
          </div>
        </div>

        {/* Inspector Pane */}
        <div className="w-[450px] bg-[#F8FAFC] overflow-auto scrollbar-thin p-6 space-y-6">
          {selectedSpan ? (
            <>
              <header className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="px-2 py-0.5 bg-indigo-500 text-white text-[10px] font-black rounded uppercase tracking-wider">
                    {selectedSpan.kind}
                  </span>
                  <span className="text-[10px] font-mono text-slate-400">{selectedSpan.span_id}</span>
                </div>
                <h3 className="text-xl font-bold text-slate-900 leading-tight">{selectedSpan.name}</h3>
              </header>

              {/* Payload Preview */}
              <InspectorCard title="Payload & Arguments" icon={<Code size={16} />}>
                {selectedSpan.attributes['gen_ai.input.messages'] || selectedSpan.attributes['gen_ai.output.messages'] ? (
                  <div className="space-y-4">
                    {selectedSpan.attributes['gen_ai.input.messages'] && (
                      <div className="space-y-2">
                        <div className="text-[9px] font-black text-slate-400 uppercase tracking-widest pl-1">Input Messages</div>
                        <pre className="text-xs bg-slate-900 text-slate-100 p-4 rounded-xl overflow-x-auto font-mono leading-relaxed max-h-64">
                          {JSON.stringify(JSON.parse(selectedSpan.attributes['gen_ai.input.messages']), null, 2)}
                        </pre>
                      </div>
                    )}
                    {selectedSpan.attributes['gen_ai.output.messages'] && (
                      <div className="space-y-2">
                        <div className="text-[9px] font-black text-slate-400 uppercase tracking-widest pl-1">Output Messages</div>
                        <pre className="text-xs bg-indigo-900 text-indigo-100 p-4 rounded-xl overflow-x-auto font-mono leading-relaxed max-h-64">
                          {JSON.stringify(JSON.parse(selectedSpan.attributes['gen_ai.output.messages']), null, 2)}
                        </pre>
                      </div>
                    )}
                  </div>
                ) : selectedSpan.attributes['gen_ai.tool.call.arguments'] || selectedSpan.attributes['gen_ai.tool.call.result'] ? (
                  <div className="space-y-4">
                    {selectedSpan.attributes['gen_ai.tool.call.arguments'] && (
                      <div className="space-y-2">
                        <div className="text-[9px] font-black text-slate-400 uppercase tracking-widest pl-1">Arguments</div>
                        <pre className="text-xs bg-slate-900 text-slate-100 p-4 rounded-xl overflow-x-auto font-mono">
                          {JSON.stringify(JSON.parse(selectedSpan.attributes['gen_ai.tool.call.arguments']), null, 2)}
                        </pre>
                      </div>
                    )}
                    {selectedSpan.attributes['gen_ai.tool.call.result'] && (
                      <div className="space-y-2">
                        <div className="text-[9px] font-black text-slate-400 uppercase tracking-widest pl-1">Result</div>
                        <pre className="text-xs bg-emerald-900 text-emerald-100 p-4 rounded-xl overflow-x-auto font-mono">
                          {JSON.stringify(JSON.parse(selectedSpan.attributes['gen_ai.tool.call.result']), null, 2)}
                        </pre>
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="text-xs text-slate-400 italic py-4 text-center border-2 border-dashed border-slate-200 rounded-xl">
                    No rich payload recorded for this span.
                  </div>
                )}
              </InspectorCard>

              {/* Full Attributes */}
              <InspectorCard title="Attributes Metadata" icon={<Terminal size={16} />}>
                <div className="space-y-1">
                  {Object.entries(selectedSpan.attributes).map(([k, v]) => (
                    <div key={k} className="flex justify-between items-start py-2 border-b border-slate-100 last:border-0 group">
                      <span className="text-[10px] font-bold text-slate-400 uppercase tracking-tighter group-hover:text-indigo-400 transition-colors shrink-0">{k}</span>
                      <span className="text-xs text-slate-700 font-mono break-all text-right ml-4">{String(v)}</span>
                    </div>
                  ))}
                </div>
              </InspectorCard>
              
              {selectedSpan.status_code === 'Error' && (
                <div className="bg-rose-50 border border-rose-100 rounded-2xl p-4 space-y-2">
                  <div className="flex items-center gap-2 text-rose-600 font-bold text-sm">
                    <AlertCircle size={16} /> Execution Error
                  </div>
                  <p className="text-xs text-rose-700 leading-relaxed font-medium">
                    {selectedSpan.status_message}
                  </p>
                </div>
              )}
            </>
          ) : (
            <div className="h-full flex items-center justify-center text-slate-400 text-sm font-medium">
              Select a span to inspect its internal state.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function DetailBadge({ label, value, icon, color }: any) {
  return (
    <div className={`px-4 py-2 bg-${color}-50 border border-${color}-100 rounded-2xl flex items-center gap-3 shadow-sm`}>
      <div className={`p-1.5 bg-${color}-500/10 text-${color}-600 rounded-lg`}>{icon}</div>
      <div>
        <div className="text-[9px] font-black text-slate-400 uppercase tracking-widest leading-none">{label}</div>
        <div className={`text-sm font-black text-${color}-700 leading-tight mt-0.5`}>{value}</div>
      </div>
    </div>
  );
}

function InspectorCard({ title, icon, children }: any) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 text-[10px] font-black text-slate-400 uppercase tracking-widest pl-1">
        {icon}
        {title}
      </div>
      {children}
    </div>
  );
}

export default App;
