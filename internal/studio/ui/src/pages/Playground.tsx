/* eslint-disable @typescript-eslint/no-explicit-any */
import { useState, useEffect, useRef, useMemo, useCallback, createContext, useContext } from 'react';
import { Network, Server, Play, BrainCircuit, MessageSquare, Terminal, Activity, Layers } from 'lucide-react';
import Form from '@rjsf/core';
import validator from '@rjsf/validator-ajv8';
import type { Manifest, GraphManifest } from '../types';
import { Mermaid, DetailBadge } from '../components';

const ChatInputContext = createContext<{
  chatInput: string;
  setChatInput: (val: string) => void;
  triggerSubmit: () => void;
} | null>(null);

interface TraceTreeItem {
  id: string;
  type: 'thread' | 'node' | 'llm' | 'tool';
  name: string;
  timestamp: string;
  nodeName?: string;
  threadId?: string;
  checkpointId?: string;
  state?: any;
  content?: string;
  details?: any;
  children: TraceTreeItem[];
}

// Custom RJSF Field for Chat/Message Lists
const MessageListField = (props: any) => {
  const { value = [], onChange } = props;
  const chatContext = useContext(ChatInputContext);
  const { chatInput, setChatInput, triggerSubmit } = chatContext || {};

  const handleSend = () => {
    if (!chatInput || !chatInput.trim()) return;
    const newMessage = { 
      role: 'user', 
      content: [
        {
          kind: 'text',
          text: chatInput
        }
      ]
    };
    onChange([...value, newMessage]);
    if (triggerSubmit) {
      triggerSubmit();
    }
  };

  const renderMessageContent = (content: any) => {
    if (typeof content === 'string') {
      return content;
    }
    if (Array.isArray(content)) {
      return content.map((block: any, idx: number) => {
        if (block && block.kind === 'text') {
          return <div key={idx}>{block.text}</div>;
        }
        return null;
      });
    }
    return JSON.stringify(content);
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
                {renderMessageContent(msg.content)}
              </div>
            </div>
          ))
        )}
      </div>
      <div className="flex gap-2">
        <input
          type="text"
          value={chatInput || ''}
          onChange={e => setChatInput?.(e.target.value)}
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

const customWidgets = {
  messageListWidget: MessageListField
};

const customTemplates = {
  FieldTemplate: CustomFieldTemplate
};

export function Playground() {
  const [manifests, setManifests] = useState<Manifest[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedGraph, setSelectedGraph] = useState<GraphManifest | null>(null);
  const [selectedCommand, setSelectedCommand] = useState<string>('__raw_state__');
  
  // Real-time trace tree and highlighting states
  const [tree, setTree] = useState<TraceTreeItem[]>([]);
  const [activeNode, setActiveNode] = useState<string | null>(null);
  const [selectedTraceItem, setSelectedTraceItem] = useState<TraceTreeItem | null>(null);
  
  // Shared state to allow single-click execute / send
  const [chatInput, setChatInput] = useState('');
  const [formData, setFormData] = useState<any>({});
  const formRef = useRef<any>(null);

  const triggerSubmit = useCallback(() => {
    if (formRef.current) {
      setTimeout(() => {
        formRef.current.submit();
      }, 50);
    }
  }, []);

  const chatContextValue = useMemo(() => ({ chatInput, setChatInput, triggerSubmit }), [chatInput, triggerSubmit]);

  const [prevKey, setPrevKey] = useState<string>('');
  const currentKey = `${selectedGraph?.id || ''}-${selectedCommand}`;
  if (currentKey !== prevKey) {
    setPrevKey(currentKey);
    setFormData({});
    setChatInput('');
  }

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

  // EventSource stream setup
  useEffect(() => {
    if (!selectedGraph) return;
    const manifest = manifests.find(m => m.graphs.some(g => g.id === selectedGraph.id));
    if (!manifest) return;

    const eventSource = new EventSource(`/api/stream?worker_id=${manifest.worker_id}`);

    eventSource.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === 'on_checkpoint') {
          const cp = msg.data;
          const threadId = cp.location.thread_id;
          const checkpointId = cp.location.checkpoint_id;
          const nextNode = cp.next?.[0] || '';
          const timestamp = new Date(cp.timestamp).toLocaleTimeString();

          if (cp.next && cp.next.length > 0) {
            setActiveNode(cp.next[0]);
          } else {
            setActiveNode('__END__');
          }

          if (cp.state) {
            setFormData(cp.state);
          }

          setTree(prev => {
            const newPrev = [...prev];
            let threadNode = newPrev.find(n => n.type === 'thread' && n.id === threadId);
            if (!threadNode) {
              threadNode = {
                id: threadId,
                type: 'thread',
                name: `Graph Run (Thread ${threadId.substring(0, 8)})`,
                timestamp,
                threadId,
                children: []
              };
              newPrev.push(threadNode);
            }

            const isDuplicate = threadNode.children.some(c => c.id === checkpointId);
            if (!isDuplicate) {
              const nodeNode: TraceTreeItem = {
                id: checkpointId,
                type: 'node',
                name: nextNode === '__END__' ? 'Node: END' : `Node: ${nextNode || 'START'}`,
                nodeName: nextNode,
                timestamp,
                checkpointId,
                state: cp.state,
                children: []
              };
              threadNode.children.push(nodeNode);
            }

            return newPrev;
          });
        } 
        
        else if (msg.type === 'on_llm_chunk') {
          const { node, source, chunk } = msg.data;
          if (!chunk) return;

          let textDelta = '';
          if (chunk.delta?.content) {
            textDelta = chunk.delta.content;
          } else if (Array.isArray(chunk.content)) {
            textDelta = chunk.content.map((b: any) => b.text || '').join('');
          } else if (typeof chunk.content === 'string') {
            textDelta = chunk.content;
          }

          setTree(prev => {
            if (prev.length === 0) return prev;
            const newPrev = JSON.parse(JSON.stringify(prev));
            const threadNode = newPrev[newPrev.length - 1];

            const nodeNode = [...threadNode.children].reverse().find((c: TraceTreeItem) => c.type === 'node' && c.nodeName === node);
            if (nodeNode) {
              let llmNode = nodeNode.children.find((c: TraceTreeItem) => c.type === 'llm' && c.name === source);
              if (!llmNode) {
                llmNode = {
                  id: `llm-${Math.random()}`,
                  type: 'llm',
                  name: source || 'LLM Call',
                  timestamp: new Date().toLocaleTimeString(),
                  content: '',
                  children: []
                };
                nodeNode.children.push(llmNode);
              }
              llmNode.content = (llmNode.content || '') + textDelta;
            }
            return newPrev;
          });
        } 
        
        else if (msg.type === 'on_tool_chunk') {
          const { node, source, chunk } = msg.data;
          if (!chunk) return;

          let textDelta = '';
          if (chunk.output) {
            textDelta = chunk.output;
          } else if (chunk.content) {
            if (Array.isArray(chunk.content)) {
              textDelta = chunk.content.map((b: any) => b.text || '').join('');
            } else if (typeof chunk.content === 'string') {
              textDelta = chunk.content;
            }
          }

          setTree(prev => {
            if (prev.length === 0) return prev;
            const newPrev = JSON.parse(JSON.stringify(prev));
            const threadNode = newPrev[newPrev.length - 1];

            const nodeNode = [...threadNode.children].reverse().find((c: TraceTreeItem) => c.type === 'node' && c.nodeName === node);
            if (nodeNode) {
              let toolNode = nodeNode.children.find((c: TraceTreeItem) => c.type === 'tool' && c.name === source);
              if (!toolNode) {
                toolNode = {
                  id: `tool-${Math.random()}`,
                  type: 'tool',
                  name: source || 'Tool Call',
                  timestamp: new Date().toLocaleTimeString(),
                  content: '',
                  children: []
                };
                nodeNode.children.push(toolNode);
              }
              if (textDelta) {
                toolNode.content = (toolNode.content || '') + textDelta;
              }
            }
            return newPrev;
          });
        }
      } catch (err) {
        console.error('Failed to parse SSE event:', err);
      }
    };

    return () => {
      eventSource.close();
    };
  }, [selectedGraph, manifests]);

  const getFormSchema = useCallback(() => {
    if (selectedCommand === '__raw_state__') {
      return selectedGraph?.input_schema || {};
    }
    const cmd = selectedGraph?.commands.find(c => c.name === selectedCommand);
    return cmd?.schema || {};
  }, [selectedGraph, selectedCommand]);

  const getUiSchema = useCallback((schema: any) => {
    const uiSchema: any = {};
    if (schema && schema.properties) {
      Object.keys(schema.properties).forEach(key => {
        const prop = schema.properties[key];
        if (prop['x-loom-type'] === 'message_list' || prop['x-loom-content'] === 'chat') {
          uiSchema[key] = {
            'ui:widget': 'messageListWidget',
            'ui:options': {
              label: false
            }
          };
        }
      });
    }
    return uiSchema;
  }, []);

  const formSchema = useMemo(() => getFormSchema(), [getFormSchema]);
  const uiSchema = useMemo(() => getUiSchema(formSchema), [formSchema, getUiSchema]);

  const handleSubmit = useCallback(({ formData: submittedFormData }: any) => {
    if (!selectedGraph) return;
    const manifest = manifests.find(m => m.graphs.some(g => g.id === selectedGraph.id));
    if (!manifest) return;

    const cmdName = selectedCommand === '__raw_state__' ? '' : selectedCommand;

    // Deep copy formData to prevent mutation issues
    const finalPayload = JSON.parse(JSON.stringify(submittedFormData || {}));

    // Automatically append any unsent chatInput to the message list field
    if (chatInput && chatInput.trim()) {
      const messageListKey = Object.keys(formSchema.properties || {}).find(key => {
        const prop = (formSchema.properties as any)[key];
        return prop["x-loom-type"] === "message_list" || prop["x-loom-content"] === "chat";
      });

      if (messageListKey) {
        const messages = finalPayload[messageListKey] || [];
        const lastMsg = messages[messages.length - 1];
        
        let lastMsgText = "";
        if (lastMsg) {
          if (typeof lastMsg.content === "string") {
            lastMsgText = lastMsg.content;
          } else if (Array.isArray(lastMsg.content)) {
            lastMsgText = lastMsg.content.map((b: any) => b.text || "").join("");
          }
        }

        // Only append if it's not already the last message to avoid duplication
        if (!lastMsg || lastMsgText !== chatInput) {
          messages.push({ 
            role: "user", 
            content: [
              {
                kind: "text",
                text: chatInput
              }
            ]
          });
          finalPayload[messageListKey] = messages;
        }
      }
      
      setChatInput("");
    }

    const payload = {
      worker_id: manifest.worker_id,
      graph_id: selectedGraph.id,
      command_name: cmdName,
      payload: finalPayload,
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
  }, [selectedGraph, manifests, selectedCommand, formSchema, chatInput]);

  const renderTreeNode = (item: TraceTreeItem, depth: number) => {
    const isSelected = selectedTraceItem?.id === item.id;
    return (
      <div key={item.id} className="space-y-1">
        <div 
          onClick={() => setSelectedTraceItem(item)}
          className={`flex items-center gap-2 py-2.5 px-4 rounded-xl cursor-pointer transition-all ${
            isSelected 
              ? 'bg-indigo-600 text-white font-bold' 
              : 'hover:bg-slate-800 text-slate-300'
          }`}
          style={{ paddingLeft: `${depth * 16 + 12}px` }}
        >
          {item.type === 'thread' && <Layers size={14} className="text-indigo-400" />}
          {item.type === 'node' && <BrainCircuit size={14} className="text-emerald-400" />}
          {item.type === 'llm' && <MessageSquare size={14} className="text-amber-400" />}
          {item.type === 'tool' && <Terminal size={14} className="text-rose-400" />}
          
          <span className="flex-1 truncate">{item.name}</span>
          {item.content && (
            <span className="text-[10px] text-slate-400 truncate max-w-[200px] italic">
              {item.content}
            </span>
          )}
          <span className="text-[10px] opacity-60 text-slate-500">{item.timestamp}</span>
        </div>
        {item.children.map(child => renderTreeNode(child, depth + 1))}
      </div>
    );
  };

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
            <header className="p-8 bg-white border-b border-slate-200 flex items-center justify-between shadow-sm z-10">
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
              {/* Left/Center Panel: Topology & Trace Tree */}
              <div className="flex-1 overflow-hidden flex flex-col border-r border-slate-200 bg-white">
                {/* Top Half: Graph Topology */}
                <div className="flex-1 overflow-auto p-8 border-b border-slate-200 min-h-[300px] flex flex-col">
                  <div className="flex items-center justify-between mb-4 shrink-0">
                    <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest">
                      <Network size={14} />
                      Graph Topology
                    </div>
                    {activeNode && (
                      <span className="text-[9px] font-black text-indigo-600 bg-indigo-50 px-2 py-0.5 rounded-full uppercase tracking-widest animate-pulse border border-indigo-100">
                        Active Node: {activeNode === '__END__' ? 'END' : activeNode}
                      </span>
                    )}
                  </div>
                  <div className="flex-1 flex items-center justify-center min-h-[200px] border-2 border-dashed border-slate-100 rounded-3xl p-8 bg-slate-50/30 overflow-auto">
                    <Mermaid chart={selectedGraph.mermaid_diagram} activeNode={activeNode} />
                  </div>
                </div>

                {/* Bottom Half: Live Trace Tree */}
                <div className="h-96 overflow-hidden p-8 bg-slate-950 text-slate-100 flex flex-col">
                  <div className="flex items-center justify-between mb-4 shrink-0">
                    <div className="flex items-center gap-2 text-[10px] font-black text-slate-400 uppercase tracking-widest">
                      <Terminal size={14} className="text-indigo-400" />
                      Live execution stream (SSE)
                    </div>
                    {tree.length > 0 && (
                      <button 
                        onClick={() => { setTree([]); setActiveNode(null); setSelectedTraceItem(null); }}
                        className="text-[9px] font-black text-slate-400 hover:text-white uppercase tracking-widest border border-slate-800 px-3 py-1.5 rounded-xl transition-all"
                      >
                        Clear Trace
                      </button>
                    )}
                  </div>

                  <div className="flex-1 overflow-auto font-mono text-xs space-y-2 pr-2">
                    {tree.length === 0 ? (
                      <div className="h-full flex flex-col items-center justify-center text-slate-500 py-12">
                        <Terminal size={24} className="mb-2 opacity-50" />
                        <p className="italic text-center text-xs">Waiting for execution trigger...</p>
                      </div>
                    ) : (
                      tree.map(node => (
                        <div key={node.id} className="space-y-1">
                          {renderTreeNode(node, 0)}
                        </div>
                      ))
                    )}
                  </div>
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
                    <ChatInputContext.Provider value={chatContextValue}>
                      <Form
                        ref={formRef}
                        schema={formSchema}
                        uiSchema={uiSchema}
                        widgets={customWidgets}
                        templates={customTemplates}
                        validator={validator}
                        onSubmit={handleSubmit}
                        formData={formData}
                        onChange={e => setFormData(e.formData)}
                      >
                        <button 
                          type="submit" 
                          className="w-full flex items-center justify-center gap-2 mt-4 px-6 py-3.5 bg-indigo-600 hover:bg-indigo-700 text-white rounded-2xl text-xs font-black shadow-lg shadow-indigo-500/20 hover:shadow-indigo-500/30 transition-all active:scale-95"
                        >
                          <Play size={14} fill="currentColor" /> TRIGGER EXECUTION
                        </button>
                      </Form>
                    </ChatInputContext.Provider>
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
