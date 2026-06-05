import { useState, useEffect } from 'react';
import { format } from 'date-fns';
import { Cpu, BrainCircuit, Clock, Coins, Activity, AlertCircle } from 'lucide-react';
import type { DashboardStats, MetricPoint } from '../types';
import { StatCard } from '../components';
import { 
  ResponsiveContainer,
  AreaChart,
  Area,
  CartesianGrid,
  XAxis,
  YAxis,
  Tooltip,
} from 'recharts';

export function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [metrics, setMetrics] = useState<{ name: string; points: MetricPoint[] }[]>([]);

  const formatMS = (ms: number | undefined) => {
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
      const validResults = results.filter((r): r is { name: string; points: MetricPoint[] } => r !== null && r.points && r.points.length > 0);
      setMetrics(validResults);
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
          value={stats?.total_tokens ? stats.total_tokens.toLocaleString() + ' tokens' : '0 tokens'} 
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
          trend={(stats?.error_count ?? 0) > 0 ? "Issues" : "Healthy"} 
          color="rose"
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {metrics.map((metric: { name: string; points: MetricPoint[] }) => {
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

          const timeMap = new Map<string, Record<string, any>>();
          const seriesNames = new Set<string>();

          metric.points.forEach((p: MetricPoint) => {
            const time = format(new Date(p.timestamp_nano / 1000000), 'HH:mm:ss');
            const name = (p.attributes[seriesKey] as string) || 'default';
            seriesNames.add(name);
            
            if (!timeMap.has(time)) {
              timeMap.set(time, { time });
            }
            const entry = timeMap.get(time)!;
            
            // For token usage, we want to see the volume per type
            // For tool execution, we want to see the count of calls
            if (metric.name.includes('loom.tool')) {
               entry[name] = ((entry[name] as number) || 0) + 1; // Count invocations
            } else {
               entry[name] = ((entry[name] as number) || 0) + p.value;
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
                    <YAxis 
                      stroke="#94A3B8" 
                      fontSize={11} 
                      tickLine={false} 
                      axisLine={false} 
                    />
                    <Tooltip 
                      contentStyle={{ borderRadius: '16px', border: 'none', boxShadow: '0 20px 25px -5px rgb(0 0 0 / 0.1)', padding: '12px' }}
                      labelStyle={{ fontWeight: 'black', marginBottom: '4px', fontSize: '12px' }}
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
