import { useState, useEffect, useMemo } from 'react';
import { Activity, Clock, RefreshCcw, BarChart3, Filter } from 'lucide-react';
import { format } from 'date-fns';
import type { Metric, MetricPoint } from '../types';
import { 
  ResponsiveContainer,
  CartesianGrid,
  XAxis,
  YAxis,
  Tooltip,
  LineChart,
  Line
} from 'recharts';

const formatUnitLabel = (unit: string) => {
  if (!unit) return 'n/a';
  const u = unit.toLowerCase();
  if (u === 's') return 'seconds';
  if (u === '{tokens}' || u === 'tokens' || u === '{token}' || u === 'token') return 'tokens';
  if (u === 'by' || u === 'bytes') return 'bytes';
  return unit;
};

const formatMetricValue = (value: number, unit: string) => {
  if (value === 0) return '0';
  const u = unit?.toLowerCase();
  if (u === 'by' || u === 'bytes') {
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(value) / Math.log(k));
    return parseFloat((value / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }
  if (u === 's') {
    if (value < 0.001) return `${(value * 1000000).toFixed(2)}µs`;
    if (value < 1) return `${(value * 1000).toFixed(2)}ms`;
    return `${value.toFixed(2)}s`;
  }
  if (u === '{tokens}' || u === 'tokens' || u === '{token}' || u === 'token') {
    return value.toLocaleString(undefined, { maximumFractionDigits: 0 });
  }
  return value.toLocaleString();
};

export function MetricsExplorer() {
  const [metrics, setMetrics] = useState<Metric[]>([]);
  const [selectedMetric, setSelectedMetric] = useState<Metric | null>(null);
  const [points, setPoints] = useState<MetricPoint[]>([]);
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
    
    let ignore = false;
    const load = async () => {
      // Use a microtask to avoid synchronous setState in effect body
      await Promise.resolve();
      if (ignore) return;
      
      setLoading(true);
      try {
        const res = await fetch(`/api/metrics/${selectedMetric.name}?interval=${interval}`);
        const data = await res.json();
        if (!ignore) {
          setPoints(data || []);
        }
      } finally {
        if (!ignore) {
          setLoading(false);
        }
      }
    };

    load();
    return () => { ignore = true; };
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
      .map((p: MetricPoint) => ({
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
                  <span className="text-slate-500 italic">"{formatUnitLabel(m.unit)}"</span>
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
                value={formatMetricValue(stats.max, selectedMetric.unit)} 
                subtext={selectedMetric.unit ? `${formatUnitLabel(selectedMetric.unit)} maximum` : 'Telemetry maximum'} 
              />
              <MetricStatCard 
                icon={<Filter className="text-slate-500" />} 
                label="Running Average" 
                value={formatMetricValue(stats.avg, selectedMetric.unit)} 
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
                      tickFormatter={(v) => formatMetricValue(v, selectedMetric.unit)}
                    />
                    <Tooltip 
                      contentStyle={{ borderRadius: '16px', border: 'none', boxShadow: '0 20px 25px -5px rgb(0 0 0 / 0.1)', padding: '12px' }}
                      labelStyle={{ fontWeight: 'black', marginBottom: '4px', fontSize: '12px' }}
                      formatter={(value: unknown) => [formatMetricValue(value as number, selectedMetric.unit), 'Value']}
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

function MetricStatCard({ icon, label, value, subtext }: { icon: React.ReactNode, label: string, value: string | number, subtext: string }) {
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
