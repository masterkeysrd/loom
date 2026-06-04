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
  Wrench,
  Filter,
  RefreshCcw,
  BarChart3,
  Copy,
  Check,
  ChevronDown,
  ChevronUp,
  GitGraph,
  Target,
  PanelLeftClose,
  PanelLeftOpen
} from 'lucide-react';
import { 
  XAxis, 
  YAxis, 
  CartesianGrid, 
  Tooltip, 
  ResponsiveContainer,
  AreaChart,
  Area,
  LineChart,
  Line
} from 'recharts';
import { format } from 'date-fns';

function App() {
  const [searchQuery, setSearchQuery] = useState('');
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  return (
    <BrowserRouter>
      <div className="flex h-screen bg-[#F8FAFC] text-slate-900 w-full overflow-hidden font-sans selection:bg-indigo-100 selection:text-indigo-700">
        {/* Sidebar */}
        <div className={`${sidebarCollapsed ? 'w-20' : 'w-64'} bg-slate-900 border-r border-slate-800 flex flex-col shrink-0 transition-all duration-300 ease-in-out relative group/sidebar`}>
          <div className={`p-6 border-b border-slate-800 mb-4 flex items-center ${sidebarCollapsed ? 'justify-center' : 'justify-between'}`}>
            {!sidebarCollapsed && (
              <h1 className="text-xl font-black text-white flex items-center gap-3 tracking-tight animate-in fade-in duration-300">
                <div className="bg-indigo-500 p-1.5 rounded-lg shadow-lg shadow-indigo-500/20">
                  <BrainCircuit className="w-6 h-6 text-white" />
                </div>
                LOOM <span className="text-indigo-400 font-light italic">Studio</span>
              </h1>
            )}
            {sidebarCollapsed && (
              <div className="bg-indigo-500 p-1.5 rounded-lg shadow-lg shadow-indigo-500/20">
                <BrainCircuit className="w-6 h-6 text-white" />
              </div>
            )}
          </div>
          
          <nav className="flex-1 px-4 space-y-1">
            <SidebarLink to="/" icon={<LayoutDashboard size={18} />} label="Overview" collapsed={sidebarCollapsed} />
            <SidebarLink to="/threads" icon={<List size={18} />} label="Trace Explorer" collapsed={sidebarCollapsed} />
            <SidebarLink to="/metrics" icon={<Activity size={18} />} label="Metrics" collapsed={sidebarCollapsed} />
          </nav>

          <div className="p-4 mt-auto border-t border-slate-800">
            <button 
              onClick={() => setSidebarCollapsed(!sidebarCollapsed)}
              className={`w-full flex items-center gap-3 px-3 py-2 text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-xl transition-all mb-2 ${sidebarCollapsed ? 'justify-center' : ''}`}
              title={sidebarCollapsed ? "Expand Sidebar" : "Collapse Sidebar"}
            >
              {sidebarCollapsed ? <PanelLeftOpen size={18} /> : <><PanelLeftClose size={18} /> <span className="text-xs font-bold uppercase tracking-widest ml-3">Collapse</span></>}
            </button>
            <div className={`flex items-center gap-3 px-3 py-2 text-slate-500 text-xs ${sidebarCollapsed ? 'justify-center' : ''}`}>
              <Settings size={14} />
              {!sidebarCollapsed && <span className="animate-in fade-in duration-300">Version 1.0.0</span>}
            </div>
          </div>
        </div>

        {/* Main Content */}
        <div className="flex-1 flex flex-col overflow-hidden">
          <header className="h-16 bg-white border-b border-slate-200 flex items-center px-8 shrink-0">
            <div className="relative w-96 group">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500 group-focus-within:text-indigo-500 transition-colors" size={16} />
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
              <Route path="/metrics" element={<MetricsExplorer />} />
            </Routes>
          </main>
        </div>
      </div>
    </BrowserRouter>
  );
}

function SidebarLink({ to, icon, label, collapsed }: { to: string, icon: React.ReactNode, label: string, collapsed?: boolean }) {
  const location = useLocation();
  const isActive = location.pathname === to || (to !== '/' && location.pathname.startsWith(to));
  
  return (
    <Link 
      to={to} 
      title={collapsed ? label : ""}
      className={`flex items-center gap-3 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all duration-200 ${
        isActive 
          ? 'bg-indigo-500/10 text-indigo-400' 
          : 'text-slate-500 hover:bg-slate-800/50 hover:text-slate-200'
      } ${collapsed ? 'justify-center px-0' : ''}`}
    >
      {icon}
      {!collapsed && <span className="animate-in fade-in slide-in-from-left-2 duration-300">{label}</span>}
    </Link>
  );
}

function Dashboard() {
  const [stats, setStats] = useState<any>(null);
  const [metrics, setMetrics] = useState<any[]>([]);

  const formatMS = (ms: number) => {
    if (!ms) return '0ms';
    if (ms < 1000) return `${ms.toFixed(0)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
  };

  useEffect(() => {
    fetch('/api/stats').then(res => res.json()).then(setStats);
    
    const importantMetrics = [
      'gen_ai.client.token.usage', 
      'loom.tool.execution.duration',
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
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6 gap-6">
        <StatCard 
          icon={<Cpu className="text-indigo-600" />} 
          label="Total Threads" 
          value={stats?.total_threads || 0} 
          trend="Active" 
          color="indigo"
        />
        <StatCard 
          icon={<BrainCircuit className="text-fuchsia-600" />} 
          label="LLM Calls" 
          value={stats?.llm_call_count || 0} 
          trend="Invoked" 
          color="fuchsia"
        />
        <StatCard 
          icon={<Clock className="text-blue-600" />} 
          label="LLM P50" 
          value={formatMS(stats?.p50_latency)} 
          trend="Latency" 
          color="blue"
        />
        <StatCard 
          icon={<Coins className="text-amber-600" />} 
          label="Tokens" 
          value={stats?.total_tokens ? stats.total_tokens.toLocaleString() : 0} 
          trend="Total" 
          color="amber"
        />
        <StatCard 
          icon={<Activity className="text-emerald-600" />} 
          label="Total Spans" 
          value={stats?.total_spans || 0} 
          trend="Live" 
          color="emerald"
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
          // Group data by attributes
          let seriesKey = 'default';
          if (metric.name === 'gen_ai.client.token.usage') {
            seriesKey = 'gen_ai.token.type';
          } else if (metric.name.includes('loom.tool')) {
            seriesKey = 'gen_ai.tool.name';
          } else if (metric.name.includes('gen_ai')) {
            seriesKey = 'gen_ai.request.model';
          } else {
            seriesKey = 'loom.node.name';
          }

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
            
            // For token usage, we want to see the volume per type
            // For tool execution, we want to see the count of calls
            if (metric.name.includes('loom.tool')) {
               entry[name] = (entry[name] || 0) + 1; // Count invocations
            } else {
               entry[name] = (entry[name] || 0) + p.value;
            }
          });

          const chartData = Array.from(timeMap.values()).sort((a, b) => a.time.localeCompare(b.time));
          let sortedSeries = Array.from(seriesNames).sort();
          if (metric.name === 'gen_ai.client.token.usage') {
            sortedSeries = sortedSeries.reverse(); // Output then Input
          }
          const colors = ['#6366F1', '#EC4899', '#10B981', '#F59E0B', '#64748B'];

          return (
            <div key={metric.name} className="bg-white p-8 rounded-2xl shadow-sm border border-slate-200 hover:border-indigo-100 transition-colors group">
              <div className="flex items-center justify-between mb-8">
                <div>
                  <h3 className="text-sm font-bold text-slate-500 uppercase tracking-widest group-hover:text-indigo-600 transition-colors">
                    {metric.name === 'loom.tool.execution.duration' ? 'Run Count By Tools' : metric.name.replace(/\./g, ' ')}
                  </h3>
                  <div className="flex gap-2 mt-2">
                    {sortedSeries.map((name, i) => (
                      <span key={name} className="flex items-center gap-1 text-[10px] font-bold text-slate-500">
                        <div className="w-2 h-2 rounded-full" style={{ backgroundColor: colors[i % colors.length] }} />
                        {name}
                      </span>
                    ))}
                  </div>
                </div>
                <div className="p-2 bg-slate-50 rounded-lg group-hover:bg-indigo-50 transition-colors">
                  {metric.name.includes('usage') ? <Coins size={16} className="text-amber-500" /> : <Activity size={16} className="text-slate-500 group-hover:text-indigo-500" />}
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
                        stackId={metric.name.includes('usage') || metric.name.includes('tool') ? "1" : undefined}
                        dot={false}
                        activeDot={{ r: 6, strokeWidth: 0 }}
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
  const [loading, setLoading] = useState(false);

  const fetchThreads = () => {
    setLoading(true);
    const url = searchQuery ? `/api/threads?q=${encodeURIComponent(searchQuery)}` : '/api/threads';
    fetch(url).then(res => res.json()).then(data => {
      if (data) setThreads(data);
      setLoading(false);
    });
  };

  useEffect(() => {
    fetchThreads();
  }, [searchQuery]);

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

function ThreadDetail() {
  const { threadId } = useParams();
  const [spans, setSpans] = useState<any[]>([]);
  const [selectedSpanId, setSelectedSpanId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  const fetchSpans = () => {
    setLoading(true);
    fetch(`/api/threads/${threadId}`).then(res => res.json()).then(data => {
      if (data) {
        setSpans(data);
        if (data.length > 0 && !selectedSpanId) setSelectedSpanId(data[0].span_id);
      }
      setLoading(false);
    });
  };

  useEffect(() => {
    fetchSpans();
  }, [threadId]);

  const selectedSpan = useMemo(() => spans.find(s => s.span_id === selectedSpanId), [spans, selectedSpanId]);

  const childrenMap = useMemo(() => {
    const map = new Map<string, any[]>();
    spans.forEach(s => {
      if (s.parent_span_id && s.parent_span_id !== "0000000000000000") {
        if (!map.has(s.parent_span_id)) map.set(s.parent_span_id, []);
        map.get(s.parent_span_id)!.push(s);
      }
    });
    return map;
  }, [spans]);

  const copyToClipboard = () => {
    if (threadId) {
      navigator.clipboard.writeText(threadId);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const formatDuration = (ns: number) => {
    const ms = ns / 1000000;
    if (ms < 1000) return `${ms.toFixed(2)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
  };

  if (spans.length === 0 && !loading) {
    return <div className="p-12 text-center text-slate-500 font-medium">Trace not found or still being ingested...</div>;
  }

  if (spans.length === 0 && loading) {
    return <div className="p-12 text-center space-y-4">
      <div className="animate-spin w-8 h-8 border-4 border-indigo-500 border-t-transparent rounded-full mx-auto" />
      <p className="text-slate-500 font-medium">Assembling trace waterfall...</p>
    </div>;
  }

  const minTime = Math.min(...spans.map(s => s.start_time_nano));
  const maxTime = Math.max(...spans.map(s => s.end_time_nano));
  const totalDuration = maxTime - minTime;

  const totalTokens = spans.reduce((sum, s) => {
    const input = parseInt(s.attributes['gen_ai.usage.input_tokens'] || 0);
    const output = parseInt(s.attributes['gen_ai.usage.output_tokens'] || 0);
    return sum + input + output;
  }, 0);

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
    } else if (span.name.startsWith("loom.graph.execute")) {
      colorClass = "bg-emerald-600";
      icon = <GitGraph size={14} />;
    } else if (span.name.startsWith("loom.node.execute")) {
      colorClass = "bg-sky-500";
      icon = <Target size={14} />;
    } else if (opName) {
      colorClass = "bg-fuchsia-500";
      icon = <BrainCircuit size={14} />;
    } else if (span.name.startsWith("HTTP") || span.attributes['http.method']) {
      colorClass = "bg-blue-500";
      icon = <Globe size={14} />;
    } else if (span.name.includes("memory")) {
      colorClass = "bg-amber-500";
      icon = <Activity size={14} />;
    }

    const children = childrenMap.get(span.span_id) || [];
    const isTool = opName === 'execute_tool' || span.name.includes("execute_tool") || span.attributes['gen_ai.tool.name'];

    const isError = span.status_code === "STATUS_CODE_ERROR";

    return (
      <div key={span.span_id}>
        <div 
          onClick={() => setSelectedSpanId(span.span_id)}
          className={`flex items-center cursor-pointer border-l-4 transition-all duration-200 group ${
            isSelected 
              ? isError ? 'bg-rose-50 border-rose-600' : 'bg-indigo-50 border-indigo-600' 
              : isError ? 'bg-rose-50/20 border-rose-300 hover:bg-rose-50/40' : 'border-transparent hover:bg-slate-50'
          }`}
          style={{ paddingLeft: `${depth * 1.2}rem` }}
        >
          <div className="w-[40%] flex items-center gap-3 py-3 px-4 truncate">
            <div className={`p-1.5 rounded-lg ${colorClass} text-white shrink-0 shadow-sm ${isError ? 'animate-pulse' : ''}`}>
              {icon}
            </div>
            <div className="flex items-center gap-2 truncate">
              <span className={`text-sm tracking-tight ${isSelected ? 'font-black text-slate-900' : 'font-semibold text-slate-600'}`}>
                {span.name}
              </span>
              {isError && <AlertCircle size={12} className="text-rose-500 shrink-0" />}
            </div>
          </div>
          <div className="w-[60%] px-8 relative h-10 flex items-center">
            <div className="absolute left-0 right-0 h-px bg-slate-100"></div>
            <div 
              className={`absolute h-4 rounded-full ${colorClass} opacity-80 shadow-sm transition-all duration-300 shadow-black/5`}
              style={{ 
                left: `${leftPercent}%`, 
                width: `${Math.max(0.5, widthPercent)}%`,
                minWidth: '6px'
              }}
            >
              <div className={`absolute top-full mt-1 left-0 text-[9px] font-black whitespace-nowrap transition-all duration-200 ${
                isSelected 
                  ? isError ? 'text-rose-600' : 'text-indigo-600 opacity-100 scale-110 origin-left' 
                  : (widthPercent > 0.5 || isTool || isError) 
                    ? isError ? 'text-rose-500 opacity-100 font-bold' : 'text-slate-500 opacity-100' 
                    : 'text-slate-400 opacity-0 group-hover:opacity-100'
              }`}>
                {formatDuration(duration)}
              </div>
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
      <div className="p-8 border-b border-slate-200 bg-white shadow-sm shrink-0 flex justify-between items-center">
        <div className="min-w-0">
          <nav className="flex text-[10px] font-black text-slate-500 mb-2 gap-2 uppercase tracking-widest items-center">
            <Link to="/threads" className="hover:text-indigo-600 transition-colors">Explorer</Link>
            <ChevronRight size={10} className="mt-0.5" />
            <div className="flex items-center gap-2 group cursor-pointer" onClick={copyToClipboard}>
              <span className="text-slate-900 max-w-[200px] truncate">{threadId}</span>
              {copied ? <Check size={10} className="text-emerald-500" /> : <Copy size={10} className="text-slate-500 group-hover:text-indigo-500 transition-colors" />}
            </div>
          </nav>
          <h2 className="text-4xl font-black text-slate-900 tracking-tighter">Trace Waterfall</h2>
        </div>
        <div className="flex gap-4 items-center">
          <DetailBadge label="Total Time" value={formatDuration(totalDuration)} icon={<Clock size={12} />} color="indigo" />
          <DetailBadge label="Total Tokens" value={totalTokens.toLocaleString()} icon={<Coins size={12} />} color="amber" />
          <DetailBadge label="Node Depth" value={Math.max(...spans.map(s => s.name.split('.').length))} icon={<List size={12} />} color="slate" />
          <button 
            onClick={fetchSpans}
            className="p-2.5 bg-white border border-slate-200 rounded-2xl hover:bg-slate-50 transition-colors shadow-sm active:scale-95 group"
          >
            <RefreshCcw size={18} className={`text-slate-500 group-hover:text-indigo-600 transition-colors ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      <div className="flex-1 flex overflow-hidden">
        {/* Waterfall Pane */}
        <div className="flex-1 overflow-auto border-r border-slate-200 bg-white scrollbar-thin scrollbar-thumb-slate-200 mr-6">
          <div className="sticky top-0 bg-slate-50/80 backdrop-blur-sm z-20 flex py-2 px-4 border-b border-slate-200 text-[10px] font-black text-slate-500 uppercase tracking-widest">
            <div className="w-[40%]">Decision Tree</div>
            <div className="w-[60%] pl-8">Temporal Alignment</div>
          </div>
          <div className="pb-24">
            {topLevelSpans.sort((a,b) => a.start_time_nano - b.start_time_nano).map(s => renderSpanLine(s, 0))}
          </div>
        </div>

        {/* Inspector Pane */}
        <div className="w-[480px] bg-[#F8FAFC] overflow-auto scrollbar-thin p-6 space-y-6">
          {selectedSpan ? (
            <>
              <header className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className={`px-2 py-0.5 text-white text-[10px] font-black rounded uppercase tracking-wider ${selectedSpan.status_code === 'Error' ? 'bg-rose-500' : 'bg-indigo-500'}`}>
                    {selectedSpan.kind}
                  </span>
                  <span className="text-[10px] font-mono text-slate-500">{selectedSpan.span_id}</span>
                </div>
                <h3 className="text-xl font-bold text-slate-900 leading-tight break-all">{selectedSpan.name}</h3>
              </header>

              {/* Quick Info */}
              <div className="grid grid-cols-2 gap-4">
                <InfoBox label="Duration" value={formatDuration(selectedSpan.end_time_nano - selectedSpan.start_time_nano)} />
                <InfoBox label="Children" value={(childrenMap.get(selectedSpan.span_id) || []).length} />
                <InfoBox label="Status" value={selectedSpan.status_code || 'Unset'} color={selectedSpan.status_code === 'Error' ? 'rose' : 'emerald'} />
                <InfoBox label="Start Time" value={`${((selectedSpan.start_time_nano - minTime) / 1000000).toFixed(2)}ms (${format(new Date(selectedSpan.start_time_nano / 1000000), 'HH:mm:ss.SSS')})`} />
              </div>

              {/* Payload Preview */}
              <InspectorCard title="Payload & Arguments" icon={<Code size={16} />}>
                {selectedSpan.attributes['gen_ai.system_instructions'] || selectedSpan.attributes['gen_ai.input.messages'] || selectedSpan.attributes['gen_ai.output.messages'] ? (
                  <div className="space-y-4">
                    {selectedSpan.attributes['gen_ai.system_instructions'] && (
                      <CollapsiblePayload 
                        title="System Instructions" 
                        content={selectedSpan.attributes['gen_ai.system_instructions']} 
                        theme="slate" 
                      />
                    )}
                    {selectedSpan.attributes['gen_ai.input.messages'] && (
                      <CollapsiblePayload 
                        title="Input Messages" 
                        content={selectedSpan.attributes['gen_ai.input.messages']} 
                        theme="slate" 
                      />
                    )}
                    {selectedSpan.attributes['gen_ai.output.messages'] && (
                      <CollapsiblePayload 
                        title="Output Messages" 
                        content={selectedSpan.attributes['gen_ai.output.messages']} 
                        theme="indigo" 
                      />
                    )}
                  </div>
                ) : selectedSpan.attributes['gen_ai.tool.call.arguments'] || selectedSpan.attributes['gen_ai.tool.call.result'] ? (
                  <div className="space-y-4">
                    {selectedSpan.attributes['gen_ai.tool.call.arguments'] && (
                      <CollapsiblePayload 
                        title="Arguments" 
                        content={selectedSpan.attributes['gen_ai.tool.call.arguments']} 
                        theme="slate" 
                      />
                    )}
                    {selectedSpan.attributes['gen_ai.tool.call.result'] && (
                      <CollapsiblePayload 
                        title="Result" 
                        content={selectedSpan.attributes['gen_ai.tool.call.result']} 
                        theme="emerald" 
                      />
                    )}
                  </div>
                ) : (
                  <div className="text-xs text-slate-500 italic py-4 text-center border-2 border-dashed border-slate-200 rounded-xl">
                    No rich payload recorded for this span.
                  </div>
                )}
              </InspectorCard>

              {/* Attributes Division */}
              <div className="space-y-6">
                <AttributeSection 
                  title="Span Attributes" 
                  icon={<Terminal size={14} />}
                  attributes={Object.entries(selectedSpan.attributes).filter(([k]) => 
                    !k.startsWith('service.') && !k.startsWith('telemetry.') && !k.startsWith('host.') && !k.startsWith('process.') && !k.startsWith('os.')
                  )} 
                />
                <AttributeSection 
                  title="Resource Attributes" 
                  icon={<Globe size={14} />}
                  attributes={Object.entries(selectedSpan.attributes).filter(([k]) => 
                    k.startsWith('service.') || k.startsWith('telemetry.') || k.startsWith('host.') || k.startsWith('process.') || k.startsWith('os.')
                  )} 
                />
              </div>
              
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
            <div className="h-full flex items-center justify-center text-slate-500 text-sm font-medium">
              Select a span to inspect its internal state.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function InfoBox({ label, value, color = 'slate' }: any) {
  return (
    <div className="bg-white p-3 rounded-xl border border-slate-200 shadow-sm">
      <div className="text-[9px] font-black text-slate-500 uppercase tracking-widest leading-none mb-1">{label}</div>
      <div className={`text-xs font-bold truncate text-${color}-700`}>{value}</div>
    </div>
  );
}

function AttributeSection({ title, icon, attributes }: any) {
  if (attributes.length === 0) return null;
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
        {icon}
        {title}
      </div>
      <div className="bg-white rounded-2xl border border-slate-200 shadow-sm overflow-hidden divide-y divide-slate-100">
        {attributes.map(([k, v]: any) => (
          <div key={k} className="flex justify-between items-start p-3 group hover:bg-slate-50 transition-colors">
            <span className="text-[10px] font-bold text-slate-500 uppercase tracking-tighter group-hover:text-indigo-600 transition-colors shrink-0">{k}</span>
            <span className="text-xs text-slate-700 font-mono break-all text-right ml-4">{String(v)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function CollapsiblePayload({ title, content, theme }: { title: string, content: string, theme: 'slate' | 'indigo' | 'emerald' }) {
  const [isOpen, setIsOpen] = useState(true);
  
  const bgClasses = {
    slate: 'bg-slate-900 text-slate-100',
    indigo: 'bg-indigo-900 text-indigo-100',
    emerald: 'bg-emerald-900 text-emerald-100'
  };

  return (
    <div className="space-y-2">
      <button 
        onClick={() => setIsOpen(!isOpen)}
        className="flex items-center justify-between w-full text-[9px] font-black text-slate-500 uppercase tracking-widest pl-1 hover:text-indigo-600 transition-colors"
      >
        <span>{title}</span>
        {isOpen ? <ChevronUp size={10} /> : <ChevronDown size={10} />}
      </button>
      {isOpen && (
        <pre className={`text-xs ${bgClasses[theme]} p-4 rounded-xl overflow-x-auto font-mono leading-relaxed max-h-64`}>
          {JSON.stringify(JSON.parse(content), null, 2)}
        </pre>
      )}
    </div>
  );
}

function DetailBadge({ label, value, icon, color }: any) {
  return (
    <div className={`px-4 py-2 bg-${color}-50 border border-${color}-100 rounded-2xl flex items-center gap-3 shadow-sm`}>
      <div className={`p-1.5 bg-${color}-500/10 text-${color}-600 rounded-lg`}>{icon}</div>
      <div>
        <div className="text-[9px] font-black text-slate-500 uppercase tracking-widest leading-none">{label}</div>
        <div className={`text-sm font-black text-${color}-700 leading-tight mt-0.5 whitespace-nowrap`}>{value}</div>
      </div>
    </div>
  );
}

function InspectorCard({ title, icon, children }: any) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
        {icon}
        {title}
      </div>
      {children}
    </div>
  );
}

function MetricsExplorer() {
  const [metrics, setMetrics] = useState<any[]>([]);
  const [selectedMetric, setSelectedMetric] = useState<any>(null);
  const [points, setPoints] = useState<any[]>([]);
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [interval, setInterval] = useState('10s');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    fetch('/api/metrics')
      .then(res => res.json())
      .then(data => {
        setMetrics(data || []);
        if (data && data.length > 0) setSelectedMetric(data[0]);
      });
  }, []);

  useEffect(() => {
    if (!selectedMetric) return;
    setLoading(true);
    fetch(`/api/metrics/${selectedMetric.name}?interval=${interval}`)
      .then(res => res.json())
      .then(data => {
        setPoints(data || []);
        setLoading(false);
      });
  }, [selectedMetric, interval]);

  const availableAttributes = useMemo(() => {
    const attrs: Record<string, Set<string>> = {};
    points.forEach(p => {
      Object.entries(p.attributes || {}).forEach(([k, v]) => {
        if (!attrs[k]) attrs[k] = new Set();
        attrs[k].add(String(v));
      });
    });
    return Object.fromEntries(Object.entries(attrs).map(([k, v]) => [k, Array.from(v)]));
  }, [points]);

  const filteredPoints = useMemo(() => {
    return points.filter(p => {
      return Object.entries(filters).every(([k, v]) => {
        if (!v) return true;
        return String(p.attributes[k]) === v;
      });
    });
  }, [points, filters]);

  const chartData = useMemo(() => {
    // Sort and format for recharts
    return filteredPoints
      .sort((a, b) => a.timestamp_nano - b.timestamp_nano)
      .map(p => ({
        time: format(new Date(p.timestamp_nano / 1000000), 'HH:mm:ss'),
        value: p.value,
        timestamp: p.timestamp_nano
      }));
  }, [filteredPoints]);

  const stats = useMemo(() => {
    if (filteredPoints.length === 0) return { count: 0, max: 0, avg: 0 };
    const values = filteredPoints.map(p => p.value);
    const sum = values.reduce((a, b) => a + b, 0);
    return {
      count: filteredPoints.length,
      max: Math.max(...values),
      avg: sum / filteredPoints.length
    };
  }, [filteredPoints]);

  return (
    <div className="h-full flex overflow-hidden animate-in fade-in duration-500">
      {/* Registry Sidebar */}
      <div className="w-80 border-r border-slate-200 bg-white flex flex-col shrink-0">
        <div className="p-6 border-b border-slate-200">
          <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest mb-1">Metrics Registry</div>
          <h2 className="text-xl font-black text-slate-900 tracking-tight">Instruments Ingest</h2>
        </div>
        <div className="flex-1 overflow-auto p-4 space-y-2">
          {metrics.map(m => {
            const isCounter = m.type.includes('Sum');
            const isSelected = selectedMetric?.name === m.name;
            
            return (
              <button
                key={m.name}
                onClick={() => {
                  setSelectedMetric(m);
                  setFilters({});
                }}
                className={`w-full text-left p-4 rounded-2xl border transition-all duration-200 group relative overflow-hidden ${
                  isSelected
                    ? isCounter ? 'bg-orange-50 border-orange-200 ring-1 ring-orange-100' : 'bg-indigo-50 border-indigo-200 ring-1 ring-indigo-100'
                    : 'bg-white border-slate-100 hover:border-slate-200 hover:bg-slate-50'
                }`}
              >
                {isSelected && (
                  <div className={`absolute top-0 left-0 w-1 h-full ${isCounter ? 'bg-orange-500' : 'bg-indigo-500'}`} />
                )}
                <div className="flex items-start gap-3 mb-2">
                  <Activity size={16} className={`shrink-0 mt-0.5 ${isSelected ? (isCounter ? 'text-orange-600' : 'text-indigo-600') : 'text-slate-500'}`} />
                  <div className={`text-sm font-bold tracking-tight break-all ${isSelected ? (isCounter ? 'text-orange-900' : 'text-indigo-900') : 'text-slate-700'}`}>
                    {m.name}
                  </div>
                </div>
                <p className="text-xs text-slate-500 leading-relaxed mb-3 line-clamp-2">
                  {m.description || 'No description provided for this instrument.'}
                </p>
                <div className="flex items-center justify-between text-[10px] font-black uppercase tracking-widest">
                  <span className={isSelected ? (isCounter ? 'text-orange-600' : 'text-indigo-600') : 'text-slate-500'}>
                    {isCounter ? 'Counter' : 'Histogram'}
                  </span>
                  <span className="text-slate-500 italic">"{m.unit || 'n/a'}"</span>
                </div>
              </button>
            );
          })}
        </div>
      </div>

      {/* Metric Detail */}
      <div className="flex-1 bg-[#F8FAFC] overflow-auto p-8">
        {selectedMetric ? (
          <div className="max-w-6xl mx-auto space-y-8">
            <header className="flex items-start justify-between gap-8">
              <div className="min-w-0 flex-1">
                <div className={`inline-flex items-center gap-2 px-2 py-0.5 text-white text-[10px] font-black rounded uppercase tracking-wider mb-2 ${
                  selectedMetric.type.includes('Sum') ? 'bg-orange-500' : 'bg-indigo-500'
                }`}>
                  {selectedMetric.type.includes('Sum') ? 'Counter (Synchronous)' : 'Histogram (Distribution)'}
                </div>
                <h1 className="text-3xl font-black text-slate-900 tracking-tight break-all leading-tight">{selectedMetric.name}</h1>
              </div>
              <div className="flex items-center gap-3">
                <div className="flex items-center gap-2 bg-white border border-slate-200 rounded-xl px-3 py-2 shadow-sm">
                  <Clock size={14} className="text-slate-500" />
                  <select 
                    value={interval}
                    onChange={(e) => setInterval(e.target.value)}
                    className="text-xs font-bold text-slate-600 outline-none bg-transparent"
                  >
                    <option value="1s">1s Buckets</option>
                    <option value="5s">5s Buckets</option>
                    <option value="10s">10s Buckets</option>
                    <option value="30s">30s Buckets</option>
                    <option value="1m">1m Buckets</option>
                    <option value="5m">5m Buckets</option>
                    <option value="1h">1h Buckets</option>
                  </select>
                </div>
                <button 
                  onClick={() => {
                    setLoading(true);
                    fetch(`/api/metrics/${selectedMetric.name}?interval=${interval}`).then(res => res.json()).then(data => {
                      setPoints(data || []);
                      setLoading(false);
                    });
                  }}
                  className="shrink-0 flex items-center gap-2 px-4 py-2 bg-white border border-slate-200 rounded-xl text-sm font-bold text-slate-600 hover:bg-slate-50 transition-all active:scale-95 shadow-sm"
                >
                  <RefreshCcw size={16} className={loading ? 'animate-spin text-indigo-500' : ''} />
                  Refresh
                </button>
              </div>
            </header>

            {/* Summary Stats */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
              <MetricStatCard 
                icon={<Activity className="text-indigo-500" />} 
                label="Event Data Points" 
                value={stats.count} 
                subtext="Captured in session" 
              />
              <MetricStatCard 
                icon={<BarChart3 className={selectedMetric.type.includes('Sum') ? 'text-orange-500' : 'text-indigo-500'} />} 
                label="Aggregate Peak" 
                value={selectedMetric.unit === 'seconds' ? stats.max.toFixed(3) : stats.max.toLocaleString()} 
                subtext={selectedMetric.unit ? `${selectedMetric.unit} maximum` : 'Telemetry maximum'} 
              />
              <MetricStatCard 
                icon={<Filter className="text-slate-500" />} 
                label="Running Average" 
                value={stats.avg.toFixed(2)} 
                subtext="Smoothed window" 
              />
            </div>

            {/* Chart & Filters */}
            <div className="bg-white rounded-3xl border border-slate-200 shadow-sm overflow-hidden">
              <div className="p-6 border-b border-slate-100 flex flex-col lg:flex-row lg:items-center justify-between gap-4">
                <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest shrink-0">
                  <Activity size={14} />
                  Live Time-Series Ingestion
                </div>
                <div className="flex flex-wrap items-center justify-start lg:justify-end gap-x-6 gap-y-2 min-w-0">
                  {Object.entries(availableAttributes).map(([key, values]) => (
                    <div key={key} className="flex items-center gap-2">
                      <span className="text-[10px] font-bold text-slate-500 uppercase tracking-widest whitespace-nowrap">{key.split('.').pop()}:</span>
                      <select 
                        className="bg-slate-50 border border-slate-200 rounded-lg px-2 py-1 text-xs font-bold text-slate-600 outline-none focus:ring-2 focus:ring-indigo-500/20 max-w-[150px]"
                        value={filters[key] || ''}
                        onChange={(e) => setFilters(prev => ({ ...prev, [key]: e.target.value }))}
                      >
                        <option value="">All</option>
                        {values.map(v => (
                          <option key={v} value={v}>{v}</option>
                        ))}
                      </select>
                    </div>
                  ))}
                </div>
              </div>
              <div className="p-8 h-[400px]">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={chartData}>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#F1F5F9" />
                    <XAxis 
                      dataKey="time" 
                      stroke="#94A3B8" 
                      fontSize={11} 
                      tickLine={false} 
                      axisLine={false}
                      minTickGap={30}
                    />
                    <YAxis 
                      stroke="#94A3B8" 
                      fontSize={11} 
                      tickLine={false} 
                      axisLine={false}
                      tickFormatter={(v) => selectedMetric.unit === 'seconds' ? `${v}s` : v}
                    />
                    <Tooltip 
                      contentStyle={{ borderRadius: '16px', border: 'none', boxShadow: '0 20px 25px -5px rgb(0 0 0 / 0.1)', padding: '12px' }}
                      labelStyle={{ fontWeight: 'black', marginBottom: '4px', fontSize: '12px' }}
                    />
                    <Line 
                      type="monotone" 
                      dataKey="value" 
                      stroke={selectedMetric.type.includes('Sum') ? '#f97316' : '#6366f1'} 
                      strokeWidth={3} 
                      dot={false}
                      activeDot={{ r: 6, strokeWidth: 0 }}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            </div>
          </div>
        ) : (
          <div className="h-full flex items-center justify-center text-slate-500 font-medium animate-pulse">
            Select an instrument from the registry to begin analysis...
          </div>
        )}
      </div>
    </div>
  );
}

function MetricStatCard({ icon, label, value, subtext }: any) {
  return (
    <div className="bg-white p-6 rounded-2xl border border-slate-200 shadow-sm space-y-4">
      <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest">
        {icon}
        {label}
      </div>
      <div>
        <div className="text-3xl font-black text-slate-900 tracking-tight">{value}</div>
        <div className="text-xs text-slate-500 font-medium">{subtext}</div>
      </div>
    </div>
  );
}

export default App;
