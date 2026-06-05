import { useState, useEffect, useCallback, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { format } from 'date-fns';
import {
  ChevronRight,
  Clock,
  Coins,
  List,
  RefreshCcw,
  Copy,
  Check,
  AlertCircle,
  Wrench,
  GitGraph,
  Target,
  BrainCircuit,
  Globe,
  Activity,
  Terminal,
  Code,
} from 'lucide-react';
import type { Span } from '../types';
import {
  DetailBadge,
  InfoBox,
  InspectorCard,
  CollapsiblePayload,
} from '../components';

export function ThreadDetail() {
  const { threadId } = useParams();
  const [spans, setSpans] = useState<Span[]>([]);
  const [selectedSpanId, setSelectedSpanId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  const fetchSpans = useCallback(() => {
    setLoading(true);
    fetch(`/api/threads/${threadId}`).then(res => res.json()).then(data => {
      if (data) {
        setSpans(data);
        if (data.length > 0 && !selectedSpanId) setSelectedSpanId(data[0].span_id);
      }
      setLoading(false);
    });
  }, [threadId, selectedSpanId]);

  useEffect(() => {
    Promise.resolve().then(() => fetchSpans());
  }, [fetchSpans]);

  const selectedSpan = useMemo(() => spans.find(s => s.span_id === selectedSpanId), [spans, selectedSpanId]);

  const childrenMap = useMemo(() => {
    const map = new Map<string, Span[]>();
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
    const input = parseInt((s.attributes['gen_ai.usage.input_tokens'] as string) || '0');
    const output = parseInt((s.attributes['gen_ai.usage.output_tokens'] as string) || '0');
    return sum + input + output;
  }, 0);

  const renderSpanLine = (span: Span, depth: number) => {
    const startOffset = span.start_time_nano - minTime;
    const duration = span.end_time_nano - span.start_time_nano;
    const leftPercent = totalDuration > 0 ? (startOffset / totalDuration) * 100 : 0;
    const widthPercent = totalDuration > 0 ? (duration / totalDuration) * 100 : 100;
    const isSelected = selectedSpanId === span.span_id;

    let colorClass = "bg-indigo-500";
    let icon = <ChevronRight size={14} />;
    
    const opName = span.attributes['gen_ai.operation.name'] as string;
    
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
        {children.sort((a: Span, b: Span) => a.start_time_nano - b.start_time_nano).map(child => renderSpanLine(child, depth + 1))}
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
                {Boolean(selectedSpan.attributes['gen_ai.system_instructions'] || selectedSpan.attributes['gen_ai.input.messages'] || selectedSpan.attributes['gen_ai.output.messages']) ? (
                  <div className="space-y-4">
                    {!!selectedSpan.attributes['gen_ai.system_instructions'] && (
                      <CollapsiblePayload 
                        title="System Instructions" 
                        content={selectedSpan.attributes['gen_ai.system_instructions'] as string} 
                        theme="slate" 
                      />
                    )}
                    {!!selectedSpan.attributes['gen_ai.input.messages'] && (
                      <CollapsiblePayload 
                        title="Input Messages" 
                        content={selectedSpan.attributes['gen_ai.input.messages'] as string} 
                        theme="slate" 
                      />
                    )}
                    {!!selectedSpan.attributes['gen_ai.output.messages'] && (
                      <CollapsiblePayload 
                        title="Output Messages" 
                        content={selectedSpan.attributes['gen_ai.output.messages'] as string} 
                        theme="indigo" 
                      />
                    )}
                  </div>
                ) : Boolean(selectedSpan.attributes['gen_ai.tool.call.arguments'] || selectedSpan.attributes['gen_ai.tool.call.result']) ? (
                  <div className="space-y-4">
                    {!!selectedSpan.attributes['gen_ai.tool.call.arguments'] && (
                      <CollapsiblePayload 
                        title="Arguments" 
                        content={selectedSpan.attributes['gen_ai.tool.call.arguments'] as string} 
                        theme="slate" 
                      />
                    )}
                    {!!selectedSpan.attributes['gen_ai.tool.call.result'] && (
                      <CollapsiblePayload 
                        title="Result" 
                        content={selectedSpan.attributes['gen_ai.tool.call.result'] as string} 
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

function AttributeSection({ title, icon, attributes }: { title: string, icon: React.ReactNode, attributes: [string, unknown][] }) {
  if (attributes.length === 0) return null;
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
        {icon}
        {title}
      </div>
      <div className="bg-white rounded-2xl border border-slate-200 shadow-sm overflow-hidden divide-y divide-slate-100">
        {attributes.map(([k, v]: [string, unknown]) => (
          <div key={k} className="flex justify-between items-start p-3 group hover:bg-slate-50 transition-colors">
            <span className="text-[10px] font-bold text-slate-500 uppercase tracking-tighter group-hover:text-indigo-600 transition-colors shrink-0">{k}</span>
            <span className="text-xs text-slate-700 font-mono break-all text-right ml-4">{String(v)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
