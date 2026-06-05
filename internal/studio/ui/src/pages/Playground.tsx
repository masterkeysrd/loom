import { useState, useEffect } from 'react';
import { Network, Server, Play, BrainCircuit, MessageSquare, Terminal, Activity, Layers } from 'lucide-react';
import Form from '@rjsf/core';
import validator from '@rjsf/validator-ajv8';
import type { Manifest, GraphManifest } from '../types';
import { Mermaid, DetailBadge } from '../components';

// Custom RJSF Field for Chat/Message Lists
const MessageListField = (props: any) => {
  const { value = [], onChange } = props;
  const [inputText, setInputText] = useState('');

  const handleSend = () => {
    if (!inputText.trim()) return;
    const newMessage = { role: 'user', content: inputText };
    onChange([...value, newMessage]);
    setInputText('');
  };

  return (
    <div className="space-y-4 border border-slate-200 rounded-3xl p-6 bg-slate-50/50">
      <div className="text-xs font-black text-slate-500 uppercase tracking-widest pl-1">Conversation History</div>
      <div className="max-h-60 overflow-y-auto space-y-3 p-3 bg-white border border-slate-200 rounded-2xl shadow-inner">
        {value.length === 0 ? (
          <div className="text-slate-400 text-xs italic text-center py-6">No messages yet.</div>
        ) : (
          value.map((msg: any, idx: number) => (
            <div 
              key={idx} 
              className={`flex flex-col max-w-[85%] ${msg.role === 'user' ? 'ml-auto items-end' : 'mr-auto items-start'}`}
            >
              <span className="text-[9px] font-black text-slate-400 uppercase tracking-widest mb-1 px-1">
                {msg.role}
              </span>
              <div 
                className={`p-3 rounded-2xl text-xs font-medium leading-relaxed ${
                  msg.role === 'user' 
                    ? 'bg-indigo-600 text-white rounded-tr-none shadow-sm' 
                    : 'bg-slate-100 text-slate-800 rounded-tl-none shadow-sm'
                }`}
              >
                {msg.content}
              </div>
            </div>
          ))
        )}
      </div>
      <div className="flex gap-2">
        <input
          type="text"
          value={inputText}
          onChange={e => setInputText(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter') {
              e.preventDefault();
              handleSend();
            }
          }}
          placeholder="Type a user message..."
          className="flex-1 bg-white border border-slate-200 rounded-xl px-4 py-2.5 text-xs outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/10 transition-all"
        />
        <button
          type="button"
          onClick={handleSend}
          className="px-4 py-2.5 bg-indigo-600 hover:bg-indigo-700 text-white rounded-xl text-xs font-black transition-all active:scale-95 shadow-md shadow-indigo-500/10"
        >
          Send
        </button>
      </div>
    </div>
  );
};

// Custom Field Template to make forms look premium and clean
const CustomFieldTemplate = (props: any) => {
  const { id, classNames, label, required, children, errors, help } = props;
  return (
    <div className={`space-y-1 mb-4 ${classNames}`}>
      <label htmlFor={id} className="block text-xs font-black text-slate-700 uppercase tracking-wider pl-1">
        {label}
        {required ? <span className="text-red-500 ml-1">*</span> : null}
      </label>
      {children}
      {errors && <div className="text-xs text-red-500 pl-1 mt-1">{errors}</div>}
      {help && <div className="text-[10px] text-slate-400 pl-1 mt-0.5">{help}</div>}
    </div>
  );
};

const customFields = {
  messageListField: MessageListField
};

export function Playground() {
  const [manifests, setManifests] = useState<Manifest[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedGraph, setSelectedGraph] = useState<GraphManifest | null>(null);
  const [selectedCommand, setSelectedCommand] = useState<string>('__raw_state__');

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

  const getFormSchema = () => {
    if (selectedCommand === '__raw_state__') {
      return selectedGraph?.input_schema || {};
    }
    const cmd = selectedGraph?.commands.find(c => c.name === selectedCommand);
    return cmd?.schema || {};
  };

  const getUiSchema = (schema: any) => {
    const uiSchema: any = {};
    if (schema && schema.properties) {
      Object.keys(schema.properties).forEach(key => {
        const prop = schema.properties[key];
        if (prop['x-loom-type'] === 'message_list' || prop['x-loom-content'] === 'chat') {
          uiSchema[key] = {
            'ui:field': 'messageListField'
          };
        }
      });
    }
    return uiSchema;
  };

  const handleSubmit = ({ formData }: any) => {
    if (!selectedGraph) return;
    const manifest = manifests.find(m => m.graphs.some(g => g.id === selectedGraph.id));
    if (!manifest) return;

    const cmdName = selectedCommand === '__raw_state__' ? '' : selectedCommand;

    const payload = {
      worker_id: manifest.worker_id,
      graph_id: selectedGraph.id,
      command_name: cmdName,
      payload: formData,
    };

    fetch('/api/execute', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(payload),
    })
      .then(res => {
        if (res.ok) {
          alert('Execution triggered successfully!');
        } else {
          res.text().then(text => alert(`Failed to trigger execution: ${text}`));
        }
      })
      .catch(err => {
        console.error(err);
        alert('Error triggering execution');
      });
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin w-8 h-8 border-4 border-indigo-500 border-t-transparent rounded-full" />
      </div>
    );
  }

  const formSchema = getFormSchema();
  const uiSchema = getUiSchema(formSchema);

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
                      onClick={() => {
                        setSelectedGraph(graph);
                        setSelectedCommand('__raw_state__');
                      }}
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
                    <Layers size={14} />
                    Target Selection
                  </div>
                  
                  {/* Select Dropdown */}
                  <div className="relative">
                    <select
                      value={selectedCommand}
                      onChange={e => setSelectedCommand(e.target.value)}
                      className="w-full bg-white border border-slate-200 rounded-2xl py-3.5 px-4 text-xs font-bold text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/10 focus:border-indigo-500 transition-all appearance-none cursor-pointer shadow-sm"
                    >
                      <option value="__raw_state__">Execute Graph (Raw State)</option>
                      {selectedGraph.commands.map(cmd => (
                        <option key={cmd.name} value={cmd.name}>
                          Command: {cmd.name}
                        </option>
                      ))}
                    </select>
                    <div className="absolute right-4 top-1/2 -translate-y-1/2 pointer-events-none text-slate-400">
                      ▼
                    </div>
                  </div>
                </div>

                <div className="space-y-4">
                  <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
                    <MessageSquare size={14} />
                    Execution Input
                  </div>
                  
                  <div className="bg-white rounded-3xl border border-slate-200 shadow-sm p-6">
                    {/* RJSF Dynamic Form */}
                    <Form
                      schema={formSchema}
                      uiSchema={uiSchema}
                      fields={customFields}
                      templates={{ FieldTemplate: CustomFieldTemplate }}
                      validator={validator}
                      onSubmit={handleSubmit}
                    >
                      <button 
                        type="submit" 
                        className="w-full flex items-center justify-center gap-2 mt-4 px-6 py-3.5 bg-indigo-600 hover:bg-indigo-700 text-white rounded-2xl text-xs font-black shadow-lg shadow-indigo-500/20 hover:shadow-indigo-500/30 transition-all active:scale-95"
                      >
                        <Play size={14} fill="currentColor" /> TRIGGER EXECUTION
                      </button>
                    </Form>
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
