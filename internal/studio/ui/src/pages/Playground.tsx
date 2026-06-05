/* eslint-disable @typescript-eslint/no-explicit-any */
import { useState, useEffect, useRef, useMemo, useCallback, createContext, useContext } from 'react';
import { useLocation } from 'react-router-dom';
import { Network, Server, Play, BrainCircuit, MessageSquare, Terminal, Activity, Layers, RefreshCcw } from 'lucide-react';
import Form from '@rjsf/core';
import validator from '@rjsf/validator-ajv8';
import type { Manifest, GraphManifest, Thread, Span } from '../types';
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
  const { value, onChange } = props;
  const safeValue = Array.isArray(value) ? value : [];
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
    onChange([...safeValue, newMessage]);
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
    <div className="bg-white border border-slate-200 rounded-3xl p-5 space-y-4 shadow-sm">
      <div className="text-xs font-black text-slate-500 uppercase tracking-widest pl-1">Conversation History</div>
      <div className="max-h-60 overflow-y-auto space-y-3 pr-1">
        {safeValue.length === 0 ? (
          <div className="text-slate-400 text-xs italic text-center py-6">No messages yet.</div>
        ) : (
          safeValue.map((msg: any, idx: number) => (
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
      <div className="flex gap-2 pt-3 border-t border-slate-100">
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

const CustomTextWidget = (props: any) => {
  const { id, required, readonly, disabled, value, onChange, placeholder, options } = props;
  return (
    <input
      id={id}
      type={options?.inputType || 'text'}
      value={value || ''}
      required={required}
      disabled={disabled || readonly}
      placeholder={placeholder}
      onChange={e => onChange(e.target.value)}
      className="w-full bg-white border border-slate-200 rounded-xl px-4 py-2.5 text-xs outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/10 transition-all text-slate-800"
    />
  );
};

const CustomTextareaWidget = (props: any) => {
  const { id, required, readonly, disabled, value, onChange, placeholder } = props;
  return (
    <textarea
      id={id}
      value={value || ''}
      required={required}
      disabled={disabled || readonly}
      placeholder={placeholder}
      onChange={e => onChange(e.target.value)}
      rows={4}
      className="w-full bg-white border border-slate-200 rounded-xl px-4 py-2.5 text-xs outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/10 transition-all text-slate-800 resize-y"
    />
  );
};

const CustomSelectWidget = (props: any) => {
  const { id, required, readonly, disabled, value, onChange, options } = props;
  const { enumOptions = [] } = options || {};
  return (
    <div className="relative w-full">
      <select
        id={id}
        value={value || ''}
        required={required}
        disabled={disabled || readonly}
        onChange={e => onChange(e.target.value)}
        className="w-full bg-white border border-slate-200 rounded-xl py-2.5 pl-4 pr-10 text-xs font-medium text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/10 focus:border-indigo-500 transition-all appearance-none cursor-pointer shadow-sm"
      >
        <option value="">Select an option...</option>
        {enumOptions.map((opt: any) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
      <div className="absolute right-4 top-1/2 -translate-y-1/2 pointer-events-none text-slate-400 text-[9px]">
        ▼
      </div>
    </div>
  );
};

const CustomCheckboxWidget = (props: any) => {
  const { id, required, readonly, disabled, value, onChange, label } = props;
  return (
    <label htmlFor={id} className="flex items-center gap-2.5 text-xs font-bold text-slate-600 cursor-pointer select-none pl-1 py-1">
      <input
        id={id}
        type="checkbox"
        checked={!!value}
        required={required}
        disabled={disabled || readonly}
        onChange={e => onChange(e.target.checked)}
        className="rounded border-slate-300 text-indigo-600 focus:ring-indigo-500 w-4 h-4 transition-all"
      />
      <span>{label}</span>
    </label>
  );
};

const customWidgets = {
  messageListWidget: MessageListField,
  TextWidget: CustomTextWidget,
  TextareaWidget: CustomTextareaWidget,
  SelectWidget: CustomSelectWidget,
  CheckboxWidget: CustomCheckboxWidget,
};

const customTemplates = {
  FieldTemplate: CustomFieldTemplate
};

// RenderDiff recursive component for comparing parent and current states
const RenderDiff = ({ prev, curr, depth = 0, showUnchanged = false }: { prev: any; curr: any; depth?: number; showUnchanged?: boolean }) => {
  const allKeys = Array.from(new Set([...Object.keys(prev || {}), ...Object.keys(curr || {})]));
  
  return (
    <div className="space-y-1">
      {allKeys.map(key => {
        const hasPrev = prev && key in prev;
        const hasCurr = curr && key in curr;
        
        const indent = '  '.repeat(depth);

        if (hasPrev && !hasCurr) {
          return (
            <div key={key} className="font-mono text-xs py-1 px-2 rounded text-rose-600 bg-rose-50/50 line-through">
              - {indent}{key}: {JSON.stringify(prev[key])}
            </div>
          );
        } else if (!hasPrev && hasCurr) {
          return (
            <div key={key} className="font-mono text-xs py-1 px-2 rounded text-emerald-600 bg-emerald-50/50 font-semibold">
              + {indent}{key}: {JSON.stringify(curr[key])}
            </div>
          );
        } else {
          const valPrev = prev[key];
          const valCurr = curr[key];
          
          const changed = JSON.stringify(valPrev) !== JSON.stringify(valCurr);
          
          if (!changed) {
            if (!showUnchanged) return null;
            return (
              <div key={key} className="font-mono text-xs py-1 px-2 rounded text-slate-400 opacity-60">
                &nbsp;&nbsp;{indent}{key}: {JSON.stringify(valCurr)}
              </div>
            );
          }
          
          if (typeof valPrev === 'object' && valPrev !== null && typeof valCurr === 'object' && valCurr !== null && !Array.isArray(valPrev) && !Array.isArray(valCurr)) {
            return (
              <div key={key} className="space-y-1">
                <div className="font-mono text-xs py-1 px-2 rounded text-amber-600 bg-amber-50/20 font-medium">
                  ✎ {indent}{key}:
                </div>
                <RenderDiff prev={valPrev} curr={valCurr} depth={depth + 1} showUnchanged={showUnchanged} />
              </div>
            );
          } else {
            return (
              <div key={key} className="font-mono text-xs py-1 px-2 rounded text-amber-700 bg-amber-50/30">
                ✎ {indent}{key}:{' '}
                <span className="text-rose-600 line-through">{JSON.stringify(valPrev)}</span>
                <span className="text-slate-400 mx-2">➔</span>
                <span className="text-emerald-600 font-semibold">{JSON.stringify(valCurr)}</span>
              </div>
            );
          }
        }
      })}
    </div>
  );
};

// JSONBox component for styling JSON blocks cleanly with a copy button
const JSONBox = ({ data, title, accentColor = 'indigo' }: { data: any; title: string; accentColor?: 'indigo' | 'emerald' | 'rose' | 'amber' }) => {
  const [copied, setCopied] = useState(false);
  const jsonStr = useMemo(() => JSON.stringify(data || {}, null, 2), [data]);
  
  const handleCopy = () => {
    navigator.clipboard.writeText(jsonStr);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  
  const accentClasses = {
    indigo: { text: 'text-indigo-400', border: 'border-slate-800', bg: 'bg-slate-950' },
    emerald: { text: 'text-emerald-400', border: 'border-slate-800', bg: 'bg-slate-950' },
    rose: { text: 'text-rose-400', border: 'border-slate-800', bg: 'bg-slate-950' },
    amber: { text: 'text-amber-400', border: 'border-slate-800', bg: 'bg-slate-950' },
  }[accentColor];

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between pl-1">
        <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest">{title}</span>
        <button 
          onClick={handleCopy}
          className="text-[10px] font-bold text-indigo-600 hover:text-indigo-850 transition-all bg-indigo-50 hover:bg-indigo-100/80 px-2 py-1 rounded-md"
        >
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <pre className={`p-4 rounded-3xl overflow-auto font-mono text-xs max-h-60 shadow-inner leading-relaxed border ${accentClasses.border} ${accentClasses.bg} ${accentClasses.text}`}>
        {jsonStr}
      </pre>
    </div>
  );
};

function normalizeToolName(name: string): string {
  if (!name) return '';
  return name.toLowerCase().startsWith('tool:') ? name.substring(5) : name;
}

function getChatKeyFromState(state: any): string | null {
  if (!state || typeof state !== 'object') return null;
  return Object.keys(state).find(key => {
    const val = state[key];
    if (!Array.isArray(val) || val.length === 0) return false;
    return val.every((msg: any) => {
      if (!msg || typeof msg !== 'object') return false;
      const keys = Object.keys(msg).map(k => k.toLowerCase());
      return keys.includes('role');
    });
  }) || null;
}

interface ExtractedToolCall {
  id: string;
  name: string;
  arguments: any;
}

function parseToolCallsFromState(state: any, chatKey: string): ExtractedToolCall[] {
  const toolCalls: ExtractedToolCall[] = [];
  if (!state || !chatKey || !state[chatKey] || !Array.isArray(state[chatKey])) {
    return toolCalls;
  }

  const messages = state[chatKey];
  for (const msg of messages) {
    if (!msg || typeof msg !== 'object') continue;
    const msgToolCalls = msg.tool_calls || msg.ToolCalls || msg.toolCalls;
    if (msgToolCalls && Array.isArray(msgToolCalls)) {
      for (const tc of msgToolCalls) {
        if (!tc) continue;
        const id = tc.id || tc.Id || '';
        const name = tc.name || tc.Name || tc.function?.name || tc.Function?.Name || '';
        const args = tc.arguments || tc.Arguments || tc.function?.arguments || tc.Function?.Arguments || tc.args || tc.Args || null;
        if (name) {
          toolCalls.push({ id, name, arguments: args });
        }
      }
    }
    const content = msg.content || msg.Content;
    if (content && Array.isArray(content)) {
      for (const block of content) {
        if (!block || typeof block !== 'object') continue;
        const type = (block.type || block.Type || block.kind || block.Kind || '').toLowerCase();
        if (type === 'tool_use' || type === 'tool_call') {
          const id = block.id || block.Id || '';
          const name = block.name || block.Name || '';
          const args = block.input || block.Input || block.arguments || block.Arguments || block.args || block.Args || null;
          if (name) {
            toolCalls.push({ id, name, arguments: args });
          }
        }
      }
    }
  }
  return toolCalls;
}

interface ExtractedToolResult {
  toolCallId?: string;
  name?: string;
  content: string;
  isError: boolean;
}

function parseToolResultsFromState(state: any, chatKey: string): ExtractedToolResult[] {
  const results: ExtractedToolResult[] = [];
  if (!state || !chatKey || !state[chatKey] || !Array.isArray(state[chatKey])) {
    return results;
  }

  const messages = state[chatKey];
  for (const msg of messages) {
    if (!msg || typeof msg !== 'object') continue;
    const role = (msg.role || msg.Role || '').toLowerCase();
    if (role === 'tool') {
      const toolCallId = msg.tool_call_id || msg.ToolCallId || msg.toolCallId || msg.toolCallID || '';
      const name = msg.name || msg.Name || '';
      const content = msg.content || msg.Content;
      const isError = !!(msg.is_error || msg.isError || msg.IsError);

      let textContent = '';
      if (typeof content === 'string') {
        textContent = content;
      } else if (Array.isArray(content)) {
        textContent = content.map((b: any) => {
          if (!b) return '';
          if (typeof b === 'string') return b;
          return b.text || b.thinking || b.content || b.Text || b.Thinking || b.Content || '';
        }).join('');
      } else if (content) {
        textContent = JSON.stringify(content);
      }

      results.push({
        toolCallId,
        name,
        content: textContent,
        isError
      });
    }
  }
  return results;
}

// InspectorView component
const InspectorView = ({
  item,
  onClose,
  tree,
  checkpoints,
  onForkState,
  initialInput,
  modifyElement
}: {
  item: TraceTreeItem;
  onClose: () => void;
  tree: TraceTreeItem[];
  checkpoints?: any[];
  onForkState?: (state: any) => void;
  initialInput?: any;
  modifyElement?: React.ReactNode;
}) => {
  // Find the live version of this item in the tree to support real-time SSE updates
  const liveItem = useMemo(() => {
    const findItem = (items: TraceTreeItem[]): TraceTreeItem | null => {
      for (const t of items) {
        if (t.id === item.id) return t;
        const found = findItem(t.children);
        if (found) return found;
      }
      return null;
    };
    return findItem(tree) || item;
  }, [item, tree]);

  // Find parent node if applicable
  const parentNode = useMemo(() => {
    if (liveItem.type === 'thread') return null;
    for (const thread of tree) {
      if (liveItem.type === 'node') {
        const idx = thread.children.findIndex(c => c.id === liveItem.id);
        if (idx > 0) {
          return thread.children[idx - 1];
        }
      } else {
        // LLM or Tool - parent is the node in thread.children that has liveItem in its children
        for (const node of thread.children) {
          if (node.children.some(c => c.id === liveItem.id)) {
            return node;
          }
        }
      }
    }
    return null;
  }, [liveItem, tree]);

  const [fetchedState, setFetchedState] = useState<any | null>(null);
  const [fetching, setFetching] = useState(false);
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    // 1. Determine if we need to fetch state
    let graphId = "";
    let threadId = "";
    let checkpointId = "";
    let checkpointNS = "";

    if (liveItem.type === 'node') {
      if (!liveItem.state) {
        graphId = liveItem.details?.['loom.graph.name'] as string;
        threadId = liveItem.threadId || liveItem.details?.['loom.thread_id'] as string;
        checkpointId = liveItem.checkpointId || liveItem.details?.['loom.checkpoint_id'] as string;
        checkpointNS = liveItem.details?.['loom.namespace'] as string || "";
      }
    } else if (liveItem.type === 'thread') {
      // For thread runs, if it's a historical run, we fetch the latest checkpoint state of this thread.
      // We know it's historical if none of its children have live state.
      const isHistorical = liveItem.children.length > 0 && !liveItem.children.some(c => c.state);
      if (isHistorical) {
        graphId = liveItem.details?.['loom.graph.name'] as string;
        threadId = liveItem.threadId || liveItem.details?.['loom.thread_id'] || liveItem.id;
        checkpointId = ""; // empty to fetch the latest
        checkpointNS = liveItem.details?.['loom.namespace'] as string || "";
      }
    }

    // 2. If we don't need to fetch (e.g. live run or not a node/thread), clear and return
    if (!graphId || !threadId) {
      setFetchedState(null);
      setFetchError(null);
      return;
    }

    setFetching(true);
    setFetchError(null);
    setFetchedState(null);

    const url = `/api/state?graph_id=${encodeURIComponent(graphId)}&thread_id=${encodeURIComponent(threadId)}&checkpoint_id=${encodeURIComponent(checkpointId)}&checkpoint_ns=${encodeURIComponent(checkpointNS)}`;

    fetch(url)
      .then(async res => {
        if (!res.ok) {
          const text = await res.text();
          throw new Error(text || "Failed to fetch checkpoint state");
        }
        return res.json();
      })
      .then(data => {
        setFetchedState(data);
      })
      .catch(err => {
        console.error(err);
        setFetchError(err.message || "Error fetching state");
      })
      .finally(() => {
        setFetching(false);
      });
  }, [liveItem]);



  // Compute node input and output states via consecutive checkpoint lookups
  const nodeStates = useMemo(() => {
    if (liveItem.type !== 'node') return { input: null, output: null };

    // 1. Try to resolve using checkpointer ancestry (source of truth)
    if (checkpoints && checkpoints.length > 0 && liveItem.checkpointId) {
      const cp = checkpoints.find(c => c.location.checkpoint_id === liveItem.checkpointId);
      if (cp) {
        const parentCP = cp.parent?.checkpoint_id 
          ? checkpoints.find(c => c.location.checkpoint_id === cp.parent.checkpoint_id)
          : null;
        return {
          input: parentCP ? parentCP.state : initialInput || {},
          output: cp.state
        };
      }
    }
    
    // 2. Fallback to OTel spans tree-based siblings
    if (liveItem.nodeName === '__START__' || liveItem.name === 'Node: START') {
      return { 
        input: initialInput || {}, 
        output: liveItem.state || fetchedState || null 
      };
    }

    if (liveItem.state || fetchedState) {
      return { input: fetchedState || liveItem.state, output: null };
    }
    // Find the thread that contains this node
    const thread = tree.find(t => t.children.some(c => c.id === liveItem.id));
    if (!thread) return { input: liveItem.state, output: null };
    const idx = thread.children.findIndex(c => c.id === liveItem.id);
    const input = thread.children[idx]?.state || null;
    const output = thread.children[idx + 1]?.state || null;
    return { input, output };
  }, [liveItem, tree, checkpoints, fetchedState, initialInput]);

  // Determine if it has a message list for Chat View
  const chatKey = useMemo(() => {
    const state = nodeStates.input;
    if (liveItem.type !== 'node' || !state) return null;
    return getChatKeyFromState(state);
  }, [liveItem.type, nodeStates.input]);

  const hasChat = !!chatKey;

  // Resolve chat key for parent node to parse LLM/Tool inputs
  const parentChatKey = useMemo(() => {
    if (!parentNode || !parentNode.state) return null;
    return getChatKeyFromState(parentNode.state);
  }, [parentNode]);


  // Resolve parent node's output state to locate tool call details added during node execution
  const parentNodeOutputState = useMemo(() => {
    if (!parentNode) return null;
    const thread = tree.find(t => t.children.some(c => c.id === parentNode.id));
    if (!thread) return null;
    const idx = thread.children.findIndex(c => c.id === parentNode.id);
    return thread.children[idx + 1]?.state || null;
  }, [parentNode, tree]);

  // Resolve tool call input arguments
  const toolCallInput = useMemo(() => {
    if (liveItem.type !== 'tool' || !parentNode || !parentChatKey) return null;

    const getToolCallFromState = (state: any) => {
      if (!state) return null;
      const messages = state[parentChatKey] || [];
      for (let i = messages.length - 1; i >= 0; i--) {
        const msg = messages[i];
        if (!msg || typeof msg !== 'object') continue;

        // Check for tool_calls or ToolCalls
        const toolCalls = msg.tool_calls || msg.ToolCalls || msg.toolCalls;
        if (toolCalls && Array.isArray(toolCalls)) {
          const match = toolCalls.find((tc: any) => {
            if (!tc) return false;
            const tcName = tc.name || tc.Name || tc.function?.name || tc.Function?.Name;
            return tcName === liveItem.name;
          });
          if (match) {
            const args = match.arguments || match.Arguments || match.function?.arguments || match.Function?.Arguments;
            return args || match;
          }
        }

        // Check for content block type 'tool_use' or 'tool_call'
        const content = msg.content || msg.Content;
        if (content && Array.isArray(content)) {
          for (const block of content) {
            if (!block || typeof block !== 'object') continue;
            const type = (block.type || block.Type || block.kind || block.Kind || '').toLowerCase();
            const blockName = block.name || block.Name || '';
            if ((type === 'tool_use' || type === 'tool_call') && blockName === liveItem.name) {
              return block.input || block.Input || block.arguments || block.Arguments || block.args || block.Args || block;
            }
          }
        }
      }
      return null;
    };

    // 1. First look in output state of parent (since tool calls are emitted during execution)
    const fromOutput = getToolCallFromState(parentNodeOutputState);
    if (fromOutput) return fromOutput;

    // 2. Fall back to input state of parent
    return getToolCallFromState(parentNode.state);
  }, [liveItem, parentNode, parentNodeOutputState, parentChatKey]);

  const toolArguments = useMemo(() => {
    if (!toolCallInput) return null;
    if (typeof toolCallInput === 'string') {
      try {
        return JSON.parse(toolCallInput);
      } catch {
        return toolCallInput;
      }
    }
    return toolCallInput;
  }, [toolCallInput]);

  const llmResponseFromState = useMemo(() => {
    if (liveItem.type !== 'llm' || !parentNode || !parentChatKey) return null;

    const getLLMResponse = (state: any) => {
      if (!state) return null;
      const messages = state[parentChatKey] || [];
      // Find the last assistant message
      for (let i = messages.length - 1; i >= 0; i--) {
        const msg = messages[i];
        if (!msg || typeof msg !== 'object') continue;
        const role = (msg.role || msg.Role || '').toLowerCase();
        if (role === 'assistant') {
          const content = msg.content || msg.Content;
          if (typeof content === 'string') {
            return content;
          } else if (Array.isArray(content)) {
            return content.map((b: any) => {
              if (!b) return '';
              if (typeof b === 'string') return b;
              return b.text || b.thinking || b.content || b.Text || b.Thinking || b.Content || '';
            }).join('');
          }
        }
      }
      return null;
    };

    return getLLMResponse(parentNodeOutputState) || getLLMResponse(parentNode.state);
  }, [liveItem.type, parentNode, parentNodeOutputState, parentChatKey]);

  const llmMetrics = useMemo(() => {
    let metrics = null;
    if (liveItem.details?.chunks) {
      const lastWithMetrics = [...liveItem.details.chunks].reverse().find((c: any) => c.metrics || c.Metrics);
      metrics = lastWithMetrics?.metrics || lastWithMetrics?.Metrics || liveItem.details.metrics || liveItem.details.Metrics || null;
    }
    if (metrics) return metrics;

    // Check parentNodeOutputState messages for assistant message token usage metrics
    if (parentNodeOutputState && parentChatKey) {
      const messages = parentNodeOutputState[parentChatKey] || [];
      for (let i = messages.length - 1; i >= 0; i--) {
        const msg = messages[i];
        if (msg && typeof msg === 'object' && msg.metrics) {
          return msg.metrics;
        }
      }
    }
    return null;
  }, [liveItem, parentNodeOutputState, parentChatKey]);

  const parsedTokens = useMemo(() => {
    if (!llmMetrics) return { prompt: 0, completion: 0, total: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0, totalCostUSD: 0 };
    const prompt = llmMetrics.prompt_tokens || llmMetrics.PromptTokens || llmMetrics.promptTokens ||
                   llmMetrics.tokens?.input || llmMetrics.tokens?.Input || llmMetrics.Tokens?.input || llmMetrics.Tokens?.Input || 0;

    const completion = llmMetrics.completion_tokens || llmMetrics.CompletionTokens || llmMetrics.completionTokens ||
                       llmMetrics.tokens?.output || llmMetrics.tokens?.Output || llmMetrics.Tokens?.output || llmMetrics.Tokens?.Output || 0;

    const total = llmMetrics.total_tokens || llmMetrics.TotalTokens || llmMetrics.totalTokens || 
                  (prompt + completion) || 0;

    const cacheRead = llmMetrics.tokens?.cache_read || llmMetrics.tokens?.CacheRead || llmMetrics.Tokens?.cache_read || llmMetrics.Tokens?.CacheRead || 0;
    const cacheWrite = llmMetrics.tokens?.cache_write || llmMetrics.tokens?.CacheWrite || llmMetrics.Tokens?.cache_write || llmMetrics.Tokens?.CacheWrite || 0;
    const reasoning = llmMetrics.tokens?.reasoning || llmMetrics.tokens?.Reasoning || llmMetrics.Tokens?.reasoning || llmMetrics.Tokens?.Reasoning || 0;

    const totalCostNano = llmMetrics.total_cost || llmMetrics.TotalCost || llmMetrics.totalCost || 0;
    const totalCostUSD = totalCostNano / 1e9;
                  
    return { prompt, completion, total, cacheRead, cacheWrite, reasoning, totalCostUSD };
  }, [llmMetrics]);

  const toolError = useMemo(() => {
    if (liveItem.details?.is_error || liveItem.details?.isError) return true;
    return liveItem.details?.chunks?.some((c: any) => c.is_error || c.isError) || false;
  }, [liveItem]);

  const structuredContent = useMemo(() => {
    if (liveItem.details?.structured_content !== undefined && liveItem.details?.structured_content !== null) {
      return liveItem.details.structured_content;
    }
    if (liveItem.details?.structuredContent !== undefined && liveItem.details?.structuredContent !== null) {
      return liveItem.details.structuredContent;
    }
    if (liveItem.details?.chunks) {
      const lastWithSC = [...liveItem.details.chunks].reverse().find((c: any) => c.structured_content !== undefined && c.structured_content !== null);
      if (lastWithSC) return lastWithSC.structured_content;
      const lastWithSC2 = [...liveItem.details.chunks].reverse().find((c: any) => c.structuredContent !== undefined && c.structuredContent !== null);
      if (lastWithSC2) return lastWithSC2.structuredContent;
    }
    return null;
  }, [liveItem]);

  // Thread metrics
  const nodeCount = useMemo(() => liveItem.type === 'thread' ? liveItem.children.length : 0, [liveItem]);
  const llmCount = useMemo(() => {
    if (liveItem.type !== 'thread') return 0;
    return liveItem.children.reduce((acc, c) => acc + c.children.filter(cc => cc.type === 'llm').length, 0);
  }, [liveItem]);
  const toolCount = useMemo(() => {
    if (liveItem.type !== 'thread') return 0;
    return liveItem.children.reduce((acc, c) => acc + c.children.filter(cc => cc.type === 'tool').length, 0);
  }, [liveItem]);

  const latestState = useMemo(() => {
    if (liveItem.type !== 'thread') return null;
    const nodes = liveItem.children.filter(c => c.type === 'node');
    if (nodes.length === 0) return null;
    const lastNode = nodes[nodes.length - 1];
    return lastNode.state || fetchedState;
  }, [liveItem, fetchedState]);

  const defaultTab = useMemo(() => {
    if (liveItem.type === 'thread') return 'summary';
    if (liveItem.type === 'node') {
      const isStartNode = liveItem.nodeName === '__START__' || liveItem.name === 'Node: START';
      return (!liveItem.state && !isStartNode) ? 'input_output' : 'diff';
    }
    return 'input_output';
  }, [liveItem.type, liveItem.state, liveItem.nodeName, liveItem.name]);

  const [activeTab, setActiveTab] = useState<string>(defaultTab);
  const [showUnchanged, setShowUnchanged] = useState<boolean>(false);
  const [prevItemId, setPrevItemId] = useState<string>('');

  if (liveItem.id !== prevItemId) {
    setPrevItemId(liveItem.id);
    setActiveTab(defaultTab);
  }

  const tabs = useMemo(() => {
    if (liveItem.type === 'thread') {
      return [
        { id: 'summary', label: 'Run Summary' },
        { id: 'raw', label: 'Raw State' }
      ];
    } else if (liveItem.type === 'node') {
      const isStartNode = liveItem.nodeName === '__START__' || liveItem.name === 'Node: START';
      const isHistorical = !liveItem.state && !isStartNode;
      const list = [];
      if (!isHistorical) {
        list.push({ id: 'diff', label: 'JSON Diff' });
        list.push({ id: 'input_output', label: 'Input & Output' });
      } else {
        list.push({ id: 'input_output', label: 'Graph State' });
      }
      if (hasChat) {
        list.push({ id: 'chat', label: 'Chat View' });
      }
      list.push({ id: 'raw', label: 'Raw State' });
      if (modifyElement) {
        list.push({ id: 'modify', label: 'Modify State' });
      }
      return list;
    } else if (liveItem.type === 'llm') {
      return [
        { id: 'input_output', label: 'Prompt & Response' },
        { id: 'raw', label: 'Raw API' }
      ];
    } else {
      return [
        { id: 'input_output', label: 'Arguments & Result' },
        { id: 'raw', label: 'Raw API' }
      ];
    }
  }, [liveItem.type, liveItem.state, hasChat]);

  const renderMessageContent = (content: any) => {
    if (typeof content === 'string') {
      return content;
    }
    if (Array.isArray(content)) {
      return content.map((block: any, idx: number) => {
        if (block && (block.kind === 'text' || block.type === 'text')) {
          return <div key={idx}>{block.text}</div>;
        }
        if (block && block.text) {
          return <div key={idx}>{block.text}</div>;
        }
        return null;
      });
    }
    return JSON.stringify(content);
  };

  return (
    <div className="flex-1 flex flex-col h-full bg-white">
      {/* Inspector Header */}
      <div className="p-6 border-b border-slate-200 flex items-center justify-between shrink-0">
        <div className="flex items-center gap-3 min-w-0">
          {liveItem.type === 'thread' && <Activity size={18} className="text-indigo-500" />}
          {liveItem.type === 'node' && <BrainCircuit size={18} className="text-emerald-500" />}
          {liveItem.type === 'llm' && <MessageSquare size={18} className="text-amber-500" />}
          {liveItem.type === 'tool' && <Terminal size={18} className="text-rose-500" />}
          
          <div className="min-w-0">
            <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest">Inspector</div>
            <h3 className="text-sm font-bold text-slate-900 truncate">{liveItem.name}</h3>
            {liveItem.checkpointId && liveItem.type !== 'thread' && (
              <div className="text-[10px] text-slate-400 font-mono mt-0.5 select-all">
                Checkpoint ID: {liveItem.checkpointId}
              </div>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {liveItem.type === 'node' && onForkState && (
            <button 
              onClick={() => onForkState(nodeStates.output || nodeStates.input || liveItem.state)}
              className="text-xs font-black text-white bg-emerald-600 hover:bg-emerald-700 flex items-center gap-1 transition-all px-3 py-1.5 rounded-xl border border-emerald-500 hover:border-emerald-600 shadow-sm"
              title="Fork graph state from this checkpoint"
            >
              Fork State
            </button>
          )}
          <button 
            onClick={onClose}
            className="text-xs font-black text-slate-500 hover:text-indigo-600 flex items-center gap-1 transition-all bg-slate-100 hover:bg-indigo-50 px-3 py-1.5 rounded-xl border border-slate-200 hover:border-indigo-100"
          >
            ← Close
          </button>
        </div>
      </div>

      {/* Tabs list */}
      <div className="flex border-b border-slate-200 bg-slate-50/50 p-2 shrink-0">
        {tabs.map(tab => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`flex-1 py-2 text-xs font-bold rounded-xl transition-all ${
              activeTab === tab.id
                ? 'bg-white text-indigo-600 shadow-sm border border-slate-200/50'
                : 'text-slate-500 hover:text-slate-800'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {/* Tab Content Body */}
      {activeTab === 'modify' && modifyElement ? (
        <div className="flex-1 flex flex-col min-h-0 overflow-hidden">
          {modifyElement}
        </div>
      ) : (
        <div className="flex-1 overflow-auto p-6">
          {fetching && (
            <div className="flex flex-col items-center justify-center py-12 gap-2 text-xs text-slate-500">
              <RefreshCcw size={18} className="animate-spin text-indigo-500" />
              Fetching state from worker checkpointer...
            </div>
          )}
          {fetchError && (
            <div className="text-xs text-rose-500 py-4 text-center bg-rose-50 border border-rose-100 rounded-xl font-medium">
              {fetchError}
            </div>
          )}
          {!fetching && !fetchError && (
            <>
              {activeTab === 'summary' && liveItem.type === 'thread' && (
                <div className="space-y-6">
                  <div className="grid grid-cols-2 gap-4">
                    <div className="bg-slate-50 border border-slate-200/60 rounded-2xl p-4">
                      <div className="text-[10px] font-black text-slate-400 uppercase tracking-widest mb-1">Thread ID</div>
                      <div className="text-xs font-mono font-bold text-slate-800 break-all select-all">
                        {liveItem.threadId || liveItem.id}
                      </div>
                    </div>
                    <div className="bg-slate-50 border border-slate-200/60 rounded-2xl p-4">
                      <div className="text-[10px] font-black text-slate-400 uppercase tracking-widest mb-1">Start Time</div>
                      <div className="text-xs font-bold text-slate-800">
                        {liveItem.timestamp}
                      </div>
                    </div>
                  </div>

                  <div className="bg-slate-50 border border-slate-200/60 rounded-2xl p-4 flex justify-between text-xs font-bold text-slate-600">
                    <div>Steps: <span className="text-indigo-600">{nodeCount}</span></div>
                    <div>LLM Calls: <span className="text-indigo-600">{llmCount}</span></div>
                    <div>Tools Executed: <span className="text-indigo-600">{toolCount}</span></div>
                  </div>

                  <JSONBox data={latestState || {}} title="Latest Graph State" accentColor="emerald" />
                </div>
              )}

              {activeTab === 'diff' && liveItem.type === 'node' && (
                <div className="space-y-4 h-full flex flex-col">
                  <div className="flex items-center justify-between shrink-0">
                    <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest">Mutated State Fields</span>
                    <label className="flex items-center gap-2 text-xs font-bold text-slate-600 cursor-pointer select-none">
                      <input 
                        type="checkbox" 
                        checked={showUnchanged} 
                        onChange={e => setShowUnchanged(e.target.checked)}
                        className="rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                      />
                      Show Unchanged Fields
                    </label>
                  </div>
                  
                  <div className="flex-1 bg-slate-50 border border-slate-200 rounded-3xl p-4 overflow-auto max-h-[500px]">
                    <RenderDiff prev={nodeStates.input || {}} curr={nodeStates.output || {}} showUnchanged={showUnchanged} />
                  </div>
                </div>
              )}

              {activeTab === 'input_output' && liveItem.type === 'node' && (
                <div className="space-y-6">
                  {nodeStates.output ? (
                    <>
                      <JSONBox data={nodeStates.input || {}} title="Input State (Before Node)" accentColor="indigo" />
                      <JSONBox data={nodeStates.output || {}} title="Output State (After Node)" accentColor="emerald" />
                    </>
                  ) : (
                    <JSONBox data={nodeStates.input || {}} title="Graph State (at Checkpoint)" accentColor="emerald" />
                  )}
                </div>
              )}

              {activeTab === 'chat' && liveItem.type === 'node' && chatKey && (
                <div className="space-y-4">
                  <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest">Conversation Timeline</div>
                  <div className="space-y-4 max-h-[500px] overflow-auto pr-2">
                    {(() => {
                      const parentMsgs = nodeStates.input?.[chatKey] || [];
                      const currentMsgs = nodeStates.output?.[chatKey] || nodeStates.input?.[chatKey] || [];
                      
                      if (currentMsgs.length === 0) {
                        return <div className="text-slate-400 text-xs italic text-center py-12">No messages in state.</div>;
                      }

                      return currentMsgs.map((msg: any, idx: number) => {
                        const isNew = idx >= parentMsgs.length;
                        return (
                          <div 
                            key={idx} 
                            className={`flex flex-col max-w-[85%] ${msg.role === 'user' ? 'ml-auto items-end' : 'mr-auto items-start'}`}
                          >
                            <div className="flex items-center gap-2 mb-1 px-1">
                              <span className="text-[9px] font-black text-slate-400 uppercase tracking-widest">
                                {msg.role}
                              </span>
                              {isNew && (
                                <span className="text-[8px] font-black bg-emerald-500 text-white px-1.5 py-0.5 rounded-md uppercase tracking-tighter animate-pulse">
                                  New
                                </span>
                              )}
                            </div>
                            <div className="p-3 bg-white border border-slate-100 rounded-2xl shadow-sm text-slate-800 whitespace-pre-wrap leading-relaxed">
                              {renderMessageContent(msg.content)}
                            </div>
                          </div>
                        );
                      });
                    })()}
                  </div>
                </div>
              )}

              {activeTab === 'input_output' && liveItem.type === 'llm' && (
                <div className="space-y-6">
                  <div className="space-y-2">
                    <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest block pl-1">Conversation Prompt</span>
                    {parentNode?.state && parentChatKey ? (
                      <div className="space-y-4 max-h-60 overflow-auto pr-2">
                        {(parentNode.state[parentChatKey] || []).map((msg: any, idx: number) => (
                          <div 
                            key={idx} 
                            className={`flex flex-col max-w-[85%] ${msg.role === 'user' ? 'ml-auto items-end' : 'mr-auto items-start'}`}
                          >
                            <span className="text-[9px] font-black text-slate-400 uppercase tracking-widest mb-1 px-1">
                              {msg.role}
                            </span>
                            <div className="p-3 bg-white border border-slate-100 rounded-2xl shadow-sm text-slate-800 whitespace-pre-wrap leading-relaxed">
                              {renderMessageContent(msg.content)}
                            </div>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <JSONBox data={parentNode?.state || {}} title="Parent Node Input State" accentColor="indigo" />
                    )}
                  </div>

                  <div className="space-y-2">
                    <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest block pl-1">LLM Output (Response)</span>
                    <div className="bg-slate-950 text-slate-300 p-4 rounded-3xl overflow-auto font-mono text-xs max-h-60 shadow-inner leading-relaxed border border-slate-800 whitespace-pre-wrap">
                      {liveItem.content || llmResponseFromState || <span className="text-slate-500 italic">Streaming completion...</span>}
                    </div>
                  </div>

                  {(liveItem.details?.model || llmMetrics) && (
                    <div className="bg-slate-50 border border-slate-200/60 rounded-2xl p-4 space-y-3">
                      {liveItem.details?.model && (
                        <div className="flex justify-between items-center text-xs font-bold text-slate-600">
                           <span>Model:</span>
                           <span className="bg-indigo-50 text-indigo-700 px-2 py-0.5 rounded font-mono text-[10px] border border-indigo-100">{liveItem.details.model}</span>
                        </div>
                      )}
                      {llmMetrics && (
                        <div className="space-y-3 pt-2 border-t border-slate-200/60">
                          <div className="grid grid-cols-3 gap-2 text-center">
                            <div>
                              <div className="text-[8px] font-black text-slate-400 uppercase tracking-widest">Prompt</div>
                              <div className="text-xs font-bold text-slate-800">{parsedTokens.prompt}</div>
                              {(parsedTokens.cacheRead > 0 || parsedTokens.cacheWrite > 0) && (
                                <div className="text-[9px] text-slate-400 font-medium">
                                  {parsedTokens.cacheRead > 0 && `R: ${parsedTokens.cacheRead}`}
                                  {parsedTokens.cacheRead > 0 && parsedTokens.cacheWrite > 0 && ' | '}
                                  {parsedTokens.cacheWrite > 0 && `W: ${parsedTokens.cacheWrite}`}
                                </div>
                              )}
                            </div>
                            <div>
                              <div className="text-[8px] font-black text-slate-400 uppercase tracking-widest">Completion</div>
                              <div className="text-xs font-bold text-slate-800">{parsedTokens.completion}</div>
                              {parsedTokens.reasoning > 0 && (
                                <div className="text-[9px] text-indigo-500 font-semibold">
                                  Reasoning: {parsedTokens.reasoning}
                                </div>
                              )}
                            </div>
                            <div>
                              <div className="text-[8px] font-black text-slate-400 uppercase tracking-widest">Total</div>
                              <div className="text-xs font-bold text-slate-800">{parsedTokens.total}</div>
                            </div>
                          </div>
                          {(parsedTokens.totalCostUSD > 0 || parsedTokens.cacheRead > 0 || parsedTokens.cacheWrite > 0) && (
                            <div className="flex justify-between items-center text-xs font-semibold pt-2 border-t border-dashed border-slate-100 text-slate-500">
                              <span>Estimated Cost:</span>
                              <span className="font-mono text-emerald-600 font-bold">${parsedTokens.totalCostUSD.toFixed(6)}</span>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}

              {activeTab === 'input_output' && liveItem.type === 'tool' && (
                <div className="space-y-6">
                  <JSONBox 
                    data={toolArguments || (toolCallInput ? { arguments: toolCallInput } : null) || {}} 
                    title="Tool Arguments (Input)" 
                    accentColor="amber" 
                  />

                  <div className="space-y-2">
                    <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest block pl-1">Tool Result (Output)</span>
                    {toolError ? (
                      <div className="bg-rose-50 border border-rose-200 text-rose-800 p-4 rounded-3xl overflow-auto font-mono text-xs max-h-60 shadow-sm leading-relaxed whitespace-pre-wrap">
                        <div className="font-bold text-rose-700 mb-1">Error:</div>
                        {liveItem.content || (typeof structuredContent === 'string' ? structuredContent : JSON.stringify(structuredContent || 'Tool execution failed.'))}
                      </div>
                    ) : structuredContent ? (
                      typeof structuredContent === 'string' ? (
                        <div className="bg-slate-950 text-emerald-400 p-4 rounded-3xl overflow-auto font-mono text-xs max-h-60 shadow-inner leading-relaxed border border-slate-800 whitespace-pre-wrap">
                          {structuredContent}
                        </div>
                      ) : (
                        <JSONBox data={structuredContent} title="Structured Result" accentColor="emerald" />
                      )
                    ) : (
                      <div className="bg-slate-950 text-emerald-400 p-4 rounded-3xl overflow-auto font-mono text-xs max-h-60 shadow-inner leading-relaxed border border-slate-800 whitespace-pre-wrap">
                        {liveItem.content || <span className="text-slate-500 italic">No output content returned.</span>}
                      </div>
                    )}
                  </div>
                </div>
              )}

              {activeTab === 'raw' && (
                <div className="space-y-4 h-full flex flex-col">
                  {liveItem.type === 'llm' ? (
                    <div className="flex-1 flex flex-col gap-4 overflow-auto">
                      <div className="flex flex-col gap-2 shrink-0">
                        <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest">
                          Raw Request {liveItem.details?.request?.provider ? `(Sent to ${liveItem.details.request.provider})` : '(Sent to Provider)'}
                        </div>
                        {liveItem.details?.request ? (
                          <pre className="bg-slate-950 text-emerald-400 p-4 rounded-3xl overflow-auto font-mono text-xs shadow-inner leading-relaxed max-h-[250px]">
                            {JSON.stringify(liveItem.details.request.payload || liveItem.details.request, null, 2)}
                          </pre>
                        ) : (
                          <div className="text-slate-400 text-xs italic bg-slate-50 border border-slate-200/60 p-4 rounded-2xl">
                            No request payload captured yet.
                          </div>
                        )}
                      </div>
                      <div className="flex-1 flex flex-col gap-2 min-h-0">
                        <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest">Raw JSON Details</div>
                        <pre className="flex-1 bg-slate-950 text-emerald-400 p-4 rounded-3xl overflow-auto font-mono text-xs shadow-inner leading-relaxed">
                          {JSON.stringify(
                            {
                              model: liveItem.details?.model,
                              chunks_count: liveItem.details?.chunks?.length,
                              chunks: liveItem.details?.chunks,
                            },
                            null,
                            2
                          )}
                        </pre>
                      </div>
                    </div>
                  ) : (
                    <>
                      <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest shrink-0">Raw JSON Payload</div>
                      <pre className="flex-1 bg-slate-950 text-emerald-400 p-6 rounded-3xl overflow-auto font-mono text-xs shadow-inner leading-relaxed max-h-[500px]">
                        {JSON.stringify(
                          liveItem.type === 'node' 
                            ? (liveItem.state || fetchedState) 
                            : liveItem.type === 'thread'
                              ? (latestState || {})
                              : (liveItem.details || { content: liveItem.content }), 
                          null, 
                          2
                        )}
                      </pre>
                    </>
                  )}
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
};

function buildTreeFromSpans(spans: Span[], threadId: string): TraceTreeItem[] {
  const map = new Map<string, Span[]>();
  spans.forEach(s => {
    if (s.parent_span_id && s.parent_span_id !== "0000000000000000") {
      if (!map.has(s.parent_span_id)) map.set(s.parent_span_id, []);
      map.get(s.parent_span_id)!.push(s);
    }
  });

  const topLevelSpans = spans.filter(s => !s.parent_span_id || s.parent_span_id === "0000000000000000" || !spans.find(p => p.span_id === s.parent_span_id));
  
  const mapSpanToTreeItem = (span: Span): TraceTreeItem => {
    const children = map.get(span.span_id) || [];
    const opName = span.attributes['gen_ai.operation.name'] as string;
    const timestamp = new Date(span.start_time_nano / 1000000).toLocaleTimeString();
    
    let type: 'thread' | 'node' | 'llm' | 'tool' = 'node';
    let name = span.name;
    const checkpointId = span.attributes['loom.checkpoint_id'] as string;
    const nodeName = span.attributes['loom.node.name'] as string;

    if (span.name.startsWith("loom.graph.execute")) {
      type = 'thread';
      name = `Graph Run (Thread ${threadId.substring(0, 8)})`;
      
      const childTreeItems = children.sort((a, b) => a.start_time_nano - b.start_time_nano).map(mapSpanToTreeItem);
      const firstActiveNode = childTreeItems.find(c => c.type === 'node');
      const startCheckpointId = firstActiveNode?.checkpointId || `start-${threadId}`;
      const startTimestamp = firstActiveNode?.timestamp || timestamp;
      
      const startNodeNode: TraceTreeItem = {
        id: `start-${threadId}`,
        type: 'node',
        name: 'Node: START',
        nodeName: '__START__',
        timestamp: startTimestamp,
        checkpointId: startCheckpointId,
        children: [],
        details: span.attributes
      };

      const filteredChildTreeItems = childTreeItems.filter(c => c.nodeName !== '__START__');

      return {
        id: span.span_id,
        type,
        name,
        timestamp,
        nodeName,
        threadId,
        checkpointId,
        children: [startNodeNode, ...filteredChildTreeItems],
        details: span.attributes
      };
    } else if (span.name.startsWith("loom.node.execute")) {
      type = 'node';
      name = `Node: ${nodeName || span.name.replace("loom.node.execute ", "")}`;
    } else if (opName) {
      type = 'llm';
      name = `${opName} LLM Call`;
    } else if (span.attributes['gen_ai.tool.name'] || span.name.includes("execute_tool")) {
      type = 'tool';
      name = (span.attributes['gen_ai.tool.name'] as string) || "Tool Call";
    }

    return {
      id: span.span_id,
      type,
      name,
      timestamp,
      nodeName,
      threadId,
      checkpointId,
      children: children.sort((a, b) => a.start_time_nano - b.start_time_nano).map(mapSpanToTreeItem),
      details: span.attributes
    };
  };

  return topLevelSpans.sort((a, b) => a.start_time_nano - b.start_time_nano).map(mapSpanToTreeItem);
}

const mapCheckpointToTraceItem = (cp: any, selectedGraphId?: string): TraceTreeItem => {
  let metadata: any = null;
  if (cp.metadata) {
    try {
      metadata = typeof cp.metadata === 'string' ? JSON.parse(cp.metadata) : cp.metadata;
    } catch (e) {
      console.error("Failed to parse checkpoint metadata", e);
    }
  }
  const nodeName = metadata?.node || '';
  const timestamp = new Date(cp.timestamp).toLocaleTimeString();
  
  return {
    id: cp.location.checkpoint_id,
    type: 'node',
    name: (!nodeName || nodeName === '__END__') ? 'Node: END' : `Node: ${nodeName}`,
    nodeName: nodeName || '__END__',
    timestamp,
    checkpointId: cp.location.checkpoint_id,
    state: cp.state,
    children: [],
    details: {
      'loom.graph.name': selectedGraphId || '',
      'loom.thread_id': cp.location.thread_id,
      'loom.namespace': cp.location.checkpoint_ns
    }
  };
};

export function Playground() {
  const location = useLocation();
  const [manifests, setManifests] = useState<Manifest[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedGraph, setSelectedGraph] = useState<GraphManifest | null>(null);
  const [selectedCommand, setSelectedCommand] = useState<string>('__raw_state__');

  const [threads, setThreads] = useState<Thread[]>([]);
  const [loadedThreadId, setLoadedThreadId] = useState<string | null>(null);

  const fetchThreads = useCallback(() => {
    fetch('/api/threads')
      .then(res => res.json())
      .then(data => {
        setThreads(data || []);
      })
      .catch(err => console.error(err));
  }, []);

  useEffect(() => {
    fetchThreads();
  }, [fetchThreads]);

  const recentThreads = useMemo(() => {
    if (!selectedGraph) return [];
    return threads
      .filter(t => t.graph_name === selectedGraph.id)
      .slice(0, 10);
  }, [threads, selectedGraph]);

  const handleLoadHistoricalThread = (threadId: string) => {
    setLoading(true);
    setLoadedThreadId(threadId);
    setViewMode('transactions');
    setSelectedTraceItem(null);

    // Fetch spans for trace tree
    fetch(`/api/threads/${threadId}`)
      .then(res => res.json())
      .then(spans => {
        if (spans && spans.length > 0) {
          const treeData = buildTreeFromSpans(spans, threadId);
          setTree(treeData);
          setActiveNode('__END__');
        } else {
          setTree([]);
        }
      })
      .catch(err => {
        console.error(err);
        setTree([]);
      });

    // Fetch checkpoints from the checkpointer
    const graphId = selectedGraph?.id || '';
    fetch(`/api/threads/${threadId}/checkpoints?graph_id=${encodeURIComponent(graphId)}`)
      .then(res => {
        if (!res.ok) throw new Error("Failed to fetch checkpoints");
        return res.json();
      })
      .then(data => {
        setCheckpoints(data || []);
        if (data && data.length > 0) {
          const latestCheckpoint = data[data.length - 1];
          if (latestCheckpoint?.state) {
            setFormData(latestCheckpoint.state);
          }
        }
        setLoading(false);
      })
      .catch(err => {
        console.error(err);
        setCheckpoints([]);
        setLoading(false);
      });
  };

  useEffect(() => {
    const state = location.state as { forkState?: any; graphId?: string } | null;
    if (state && state.forkState && manifests.length > 0) {
      let graph: GraphManifest | null = null;
      for (const manifest of manifests) {
        const found = manifest.graphs.find(g => g.id === state.graphId);
        if (found) {
          graph = found;
          break;
        }
      }
      if (graph) {
        setSelectedGraph(graph);
        setSelectedCommand('__raw_state__');
        setFormData(state.forkState);
        setRawJsonText(JSON.stringify(state.forkState, null, 2));
        setInputMode('json');
        window.history.replaceState(null, '');
      }
    }
  }, [location, manifests]);

  // Real-time trace tree and highlighting states
  const [tree, setTree] = useState<TraceTreeItem[]>([]);
  const [checkpoints, setCheckpoints] = useState<any[]>([]);
  const [activeNode, setActiveNode] = useState<string | null>(null);
  const [selectedTraceItem, setSelectedTraceItem] = useState<TraceTreeItem | null>(null);
  const [viewMode, setViewMode] = useState<'topology' | 'transactions' | 'console'>('topology');
  const [lastExecutionPayload, setLastExecutionPayload] = useState<any>(null);
  
  // Shared state to allow single-click execute / send
  const [chatInput, setChatInput] = useState('');
  const [formData, setFormData] = useState<any>({});
  const formRef = useRef<any>(null);

  const [inputMode, setInputMode] = useState<'form' | 'json'>('form');
  const [rawJsonText, setRawJsonText] = useState<string>('{}');
  const [jsonError, setJsonError] = useState<string | null>(null);

  // Sync formData with selected transaction state
  useEffect(() => {
    if (selectedTraceItem) {
      if (selectedTraceItem.state) {
        setFormData(selectedTraceItem.state);
      } else {
        const graphId = selectedGraph?.id || '';
        const threadId = loadedThreadId || '';
        const checkpointId = selectedTraceItem.checkpointId || '';
        const checkpointNS = selectedTraceItem.details?.['loom.namespace'] || '';
        if (graphId && threadId && checkpointId) {
          fetch(`/api/state?graph_id=${encodeURIComponent(graphId)}&thread_id=${encodeURIComponent(threadId)}&checkpoint_id=${encodeURIComponent(checkpointId)}&checkpoint_ns=${encodeURIComponent(checkpointNS)}`)
            .then(res => res.json())
            .then(state => {
              if (state) {
                setFormData(state);
              }
            })
            .catch(err => console.error(err));
        }
      }
    } else {
      const latestCP = checkpoints[checkpoints.length - 1];
      if (latestCP?.state) {
        setFormData(latestCP.state);
      }
    }
  }, [selectedTraceItem, checkpoints, loadedThreadId, selectedGraph]);

  const handleJsonChange = (val: string) => {
    setRawJsonText(val);
    if (!val.trim()) {
      setJsonError(null);
      setFormData({});
      return;
    }
    try {
      const parsed = JSON.parse(val);
      setJsonError(null);
      setFormData(parsed);
    } catch (err: any) {
      setJsonError(err.message || 'Invalid JSON');
    }
  };

  useEffect(() => {
    if (inputMode === 'json') {
      try {
        setRawJsonText(JSON.stringify(formData, null, 2));
        setJsonError(null);
      } catch (e) {
        // Ignore
      }
    }
  }, [inputMode, formData]);

  const triggerSubmit = useCallback(() => {
    if (formRef.current) {
      setTimeout(() => {
        formRef.current.submit();
      }, 50);
    }
  }, []);

  const chatContextValue = useMemo(() => ({ chatInput, setChatInput, triggerSubmit }), [chatInput, triggerSubmit]);

  const [prevGraphId, setPrevGraphId] = useState<string>('');
  if ((selectedGraph?.id || '') !== prevGraphId) {
    setPrevGraphId(selectedGraph?.id || '');
    setFormData({});
    setChatInput('');
    setRawJsonText('{}');
    setJsonError(null);
    setInputMode('form');
    setLoadedThreadId(null);
    setCheckpoints([]);
  }

  const [prevCommand, setPrevCommand] = useState<string>('');
  if (selectedCommand !== prevCommand) {
    setPrevCommand(selectedCommand);
    setFormData({});
    setChatInput('');
    setRawJsonText('{}');
    setJsonError(null);
  }

  const messageListKey = useMemo(() => {
    const schema = selectedGraph?.input_schema;
    if (!schema || !schema.properties) return null;
    return Object.keys(schema.properties).find(key => {
      const prop = schema.properties[key];
      return prop['x-loom-type'] === 'message_list' || prop['x-loom-content'] === 'chat';
    }) || null;
  }, [selectedGraph]);

  const chatMessages = useMemo(() => {
    if (!messageListKey) return [];
    const msgs = formData[messageListKey];
    return Array.isArray(msgs) ? msgs : [];
  }, [formData, messageListKey]);

  const renderMessageContentLocal = (content: any) => {
    if (typeof content === 'string') {
      return content;
    }
    if (Array.isArray(content)) {
      return content.map((block: any, idx: number) => {
        if (block && (block.kind === 'text' || block.type === 'text')) {
          return <div key={idx}>{block.text}</div>;
        }
        if (block && block.text) {
          return <div key={idx}>{block.text}</div>;
        }
        return null;
      });
    }
    return JSON.stringify(content);
  };

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
          setViewMode('console');
          const cp = msg.data;
          const threadId = cp.location.thread_id;
          const checkpointId = cp.location.checkpoint_id;
          const nextNode = cp.next?.[0] || '';
          const timestamp = new Date(cp.timestamp).toLocaleTimeString();

          let metadata: any = null;
          if (cp.metadata) {
            try {
              metadata = typeof cp.metadata === 'string' ? JSON.parse(cp.metadata) : cp.metadata;
            } catch (e) {
              console.error("Failed to parse checkpoint metadata", e);
            }
          }
          const executedNode = metadata?.node || '';

          if (cp.next && cp.next.length > 0) {
            setActiveNode(cp.next[0]);
          } else {
            setActiveNode('__END__');
          }

          if (cp.state) {
            setFormData(cp.state);
          }

          setLoadedThreadId(prevId => {
            if (!prevId) return threadId;
            return prevId;
          });

          setCheckpoints(prev => {
            const exists = prev.some(c => c.location.checkpoint_id === checkpointId);
            if (!exists) {
              return [...prev, cp];
            }
            return prev;
          });

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

            // Ensure virtual START node is prepended if this is the first checkpoint
            if (threadNode.children.length === 0) {
              const startNodeNode: TraceTreeItem = {
                id: `start-${threadId}`,
                type: 'node',
                name: 'Node: START',
                nodeName: '__START__',
                timestamp,
                checkpointId: checkpointId,
                state: cp.state,
                children: [],
                details: {
                  'loom.graph.name': selectedGraph.id,
                  'loom.thread_id': threadId
                }
              };
              threadNode.children.push(startNodeNode);
            }

            const isDuplicate = threadNode.children.some(c => c.id === checkpointId || c.checkpointId === checkpointId);
            if (!isDuplicate) {
              let nodeNameLabel = nextNode;
              let nName = nextNode;
              if (executedNode) {
                if (executedNode === '__START__') {
                  // The START node was already prepended, we don't need a duplicate node
                  // But let's make sure its checkpointId and state are updated
                  const startNode = threadNode.children.find(c => c.nodeName === '__START__');
                  if (startNode) {
                    startNode.checkpointId = checkpointId;
                    startNode.state = cp.state;
                  }
                  return newPrev;
                }
                nodeNameLabel = executedNode;
                nName = executedNode;
              }

              const nodeNode: TraceTreeItem = {
                id: checkpointId,
                type: 'node',
                name: (!nodeNameLabel || nodeNameLabel === '__END__') ? 'Node: END' : `Node: ${nodeNameLabel}`,
                nodeName: nName,
                timestamp,
                checkpointId,
                state: cp.state,
                children: []
              };
              threadNode.children.push(nodeNode);
            }

            // Synthesize Tool calls and results from state into the most recent Tools node
            if (cp.state) {
              const chatKey = getChatKeyFromState(cp.state);
              if (chatKey) {
                const toolCalls = parseToolCallsFromState(cp.state, chatKey);
                const toolResults = parseToolResultsFromState(cp.state, chatKey);

                const toolsNode = [...threadNode.children].reverse().find(
                  c => c.type === 'node' && c.nodeName?.toLowerCase() === 'tools'
                );

                if (toolsNode) {
                  toolCalls.forEach((tc, index) => {
                    let toolNode = toolsNode.children.find(
                      c => c.type === 'tool' && (c.id === `tool-${tc.id}` || normalizeToolName(c.name) === normalizeToolName(tc.name))
                    );
                    
                    if (!toolNode) {
                      toolNode = {
                        id: tc.id ? `tool-${tc.id}` : `tool-synth-${index}`,
                        type: 'tool',
                        name: tc.name,
                        timestamp,
                        content: '',
                        details: { chunks: [] },
                        children: []
                      };
                      toolsNode.children.push(toolNode);
                    }

                    // Find matching result
                    const matchingResult = toolResults.find(
                      tr => (tr.toolCallId && tr.toolCallId === tc.id) || (normalizeToolName(tr.name || '') === normalizeToolName(tc.name))
                    );

                    if (matchingResult) {
                      toolNode.content = matchingResult.content;
                      if (!toolNode.details) toolNode.details = {};
                      toolNode.details.is_error = matchingResult.isError;
                      toolNode.details.structured_content = matchingResult.content;
                    }
                  });
                }
              }
            }

            return newPrev;
          });
        } 
        
        else if (msg.type === 'on_llm_request') {
          const { node, source, request } = msg.data;
          if (!request) return;

          setTree(prev => {
            if (prev.length === 0) return prev;
            const newPrev = JSON.parse(JSON.stringify(prev));
            const threadNode = newPrev[newPrev.length - 1];

            const nodeNode = [...threadNode.children].reverse().find((c: TraceTreeItem) => c.type === 'node' && c.nodeName?.toLowerCase() === node?.toLowerCase());
            if (nodeNode) {
              let llmNode = nodeNode.children.find((c: TraceTreeItem) => c.type === 'llm' && c.name === source);
              if (!llmNode) {
                llmNode = {
                  id: `llm-${Math.random()}`,
                  type: 'llm',
                  name: source || 'LLM Call',
                  timestamp: new Date().toLocaleTimeString(),
                  content: '',
                  details: { chunks: [] },
                  children: []
                };
                nodeNode.children.push(llmNode);
              }
              if (llmNode.details) {
                llmNode.details.request = request;
              }
            }
            return newPrev;
          });
        }

        else if (msg.type === 'on_llm_chunk') {
          const { node, source, chunk } = msg.data;
          if (!chunk) return;

          let textDelta = '';
          const delta = chunk.delta || chunk.Delta;
          const contentList = chunk.content || chunk.Content;

          if (delta) {
            textDelta = delta.content || delta.Content || delta.text || delta.Text || delta.thinking || delta.Thinking || '';
          } else if (Array.isArray(contentList)) {
            textDelta = contentList.map((b: any) => {
              if (!b) return '';
              if (typeof b === 'string') return b;
              return b.text || b.thinking || b.content || b.Text || b.Thinking || b.Content || '';
            }).join('');
          } else if (typeof contentList === 'string') {
            textDelta = contentList;
          } else {
            textDelta = chunk.text || chunk.Text || chunk.thinking || chunk.Thinking || chunk.content || chunk.Content || '';
          }

          setTree(prev => {
            if (prev.length === 0) return prev;
            const newPrev = JSON.parse(JSON.stringify(prev));
            const threadNode = newPrev[newPrev.length - 1];

            const nodeNode = [...threadNode.children].reverse().find((c: TraceTreeItem) => c.type === 'node' && c.nodeName?.toLowerCase() === node?.toLowerCase());
            if (nodeNode) {
              let llmNode = nodeNode.children.find((c: TraceTreeItem) => c.type === 'llm' && c.name === source);
              if (!llmNode) {
                llmNode = {
                  id: `llm-${Math.random()}`,
                  type: 'llm',
                  name: source || 'LLM Call',
                  timestamp: new Date().toLocaleTimeString(),
                  content: '',
                  details: { chunks: [], model: chunk.model || chunk.Model },
                  children: []
                };
                nodeNode.children.push(llmNode);
              }
              llmNode.content = (llmNode.content || '') + textDelta;
              if (llmNode.details) {
                if (!llmNode.details.chunks) llmNode.details.chunks = [];
                llmNode.details.chunks.push(chunk);
                const modelName = chunk.model || chunk.Model;
                if (modelName) {
                  llmNode.details.model = modelName;
                }
              }
            }
            return newPrev;
          });
        } 
        
        else if (msg.type === 'on_tool_chunk') {
          const { node, source, chunk } = msg.data;
          if (!chunk) return;

          let textDelta = '';
          const contentList = chunk.content || chunk.Content;
          if (chunk.output) {
            textDelta = chunk.output;
          } else if (chunk.Output) {
            textDelta = chunk.Output;
          } else if (contentList) {
            if (Array.isArray(contentList)) {
              textDelta = contentList.map((b: any) => {
                if (!b) return '';
                if (typeof b === 'string') return b;
                return b.text || b.thinking || b.content || b.Text || b.Thinking || b.Content || '';
              }).join('');
            } else if (typeof contentList === 'string') {
              textDelta = contentList;
            }
          } else {
            textDelta = chunk.text || chunk.Text || chunk.content || chunk.Content || '';
          }

          setTree(prev => {
            if (prev.length === 0) return prev;
            const newPrev = JSON.parse(JSON.stringify(prev));
            const threadNode = newPrev[newPrev.length - 1];

            const nodeNode = [...threadNode.children].reverse().find((c: TraceTreeItem) => c.type === 'node' && c.nodeName?.toLowerCase() === node?.toLowerCase());
            if (nodeNode) {
              let toolNode = nodeNode.children.find((c: TraceTreeItem) => c.type === 'tool' && normalizeToolName(c.name) === normalizeToolName(source));
              if (!toolNode) {
                toolNode = {
                  id: `tool-${Math.random()}`,
                  type: 'tool',
                  name: source ? normalizeToolName(source) : 'Tool Call',
                  timestamp: new Date().toLocaleTimeString(),
                  content: '',
                  details: { chunks: [] },
                  children: []
                };
                nodeNode.children.push(toolNode);
              }
              if (textDelta) {
                toolNode.content = (toolNode.content || '') + textDelta;
              }
              if (toolNode.details) {
                if (!toolNode.details.chunks) toolNode.details.chunks = [];
                toolNode.details.chunks.push(chunk);
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
            'ui:widget': 'hidden'
          };
        }
      });
    }
    return uiSchema;
  }, []);

  const formSchema = useMemo(() => getFormSchema(), [getFormSchema]);
  const uiSchema = useMemo(() => getUiSchema(formSchema), [formSchema, getUiSchema]);

  const triggerExecution = useCallback((submittedFormData: any) => {
    if (!selectedGraph) return;
    const manifest = manifests.find(m => m.graphs.some(g => g.id === selectedGraph.id));
    if (!manifest) return;

    const cmdName = selectedCommand === '__raw_state__' ? '' : selectedCommand;

    // Deep copy formData to prevent mutation issues
    const finalPayload = JSON.parse(JSON.stringify(submittedFormData || {}));
    
    // Save execution payload for START node inspector
    setLastExecutionPayload(finalPayload);
    setCheckpoints([]);

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

    const latestCP = checkpoints[checkpoints.length - 1];
    const targetCPId = selectedTraceItem?.checkpointId || latestCP?.location.checkpoint_id;
    const targetCPNS = selectedTraceItem?.details?.['loom.namespace'] || latestCP?.location.checkpoint_ns || '';

    const payload: any = {
      worker_id: manifest.worker_id,
      graph_id: selectedGraph.id,
      command_name: cmdName,
      payload: finalPayload,
    };
    if (loadedThreadId && targetCPId) {
      payload.thread_id = loadedThreadId;
      payload.checkpoint_id = targetCPId;
      payload.checkpoint_ns = targetCPNS;
    }

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
          setViewMode('console');
          setTimeout(fetchThreads, 1000);
        } else {
          res.text().then(text => alert(`Failed to trigger execution: ${text}`));
        }
      })
      .catch(err => {
        console.error(err);
        alert('Error triggering execution');
      });
  }, [selectedGraph, manifests, selectedCommand, formSchema, chatInput, fetchThreads, loadedThreadId, checkpoints]);

  const handleSubmit = useCallback(({ formData: submittedFormData }: any) => {
    triggerExecution(submittedFormData);
  }, [triggerExecution]);

  const handleForkState = (state: any) => {
    setFormData(state);
    setRawJsonText(JSON.stringify(state, null, 2));
    setInputMode('json');
    setSelectedTraceItem(null);
  };

  const handleForkCheckpoint = (item: TraceTreeItem) => {
    if (item.state) {
      handleForkState(item.state);
      return;
    }
    if (item.nodeName === '__START__' || item.name === 'Node: START') {
      handleForkState({});
      return;
    }
    const graphId = item.details?.['loom.graph.name'] as string;
    const threadId = item.threadId || item.details?.['loom.thread_id'] as string;
    const checkpointId = item.checkpointId || item.details?.['loom.checkpoint_id'] as string;
    const checkpointNS = item.details?.['loom.namespace'] as string || "";

    if (!graphId || !checkpointId || !threadId) {
      alert("Missing required fields to fork from this checkpoint.");
      return;
    }

    setLoading(true);
    fetch(`/api/state?graph_id=${encodeURIComponent(graphId)}&thread_id=${encodeURIComponent(threadId)}&checkpoint_id=${encodeURIComponent(checkpointId)}&checkpoint_ns=${encodeURIComponent(checkpointNS)}`)
      .then(res => {
        if (!res.ok) throw new Error("Failed to fetch checkpoint state");
        return res.json();
      })
      .then(state => {
        handleForkState(state);
        setLoading(false);
      })
      .catch(err => {
        console.error(err);
        alert(err.message || "Error fetching checkpoint state to fork");
        setLoading(false);
      });
  };

  const transactions = useMemo(() => {
    return checkpoints.map(cp => mapCheckpointToTraceItem(cp, selectedGraph?.id));
  }, [checkpoints, selectedGraph]);

  const handleTriggerClick = () => {
    if (inputMode === 'form') {
      formRef.current?.submit();
    } else {
      try {
        const parsed = JSON.parse(rawJsonText);
        setJsonError(null);
        triggerExecution(parsed);
      } catch (err: any) {
        setJsonError(err.message || 'Invalid JSON');
      }
    }
  };

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
          {item.checkpointId && item.type !== 'thread' && (
            <span className="text-[9px] font-mono bg-emerald-50 text-emerald-700 border border-emerald-200/60 px-1.5 py-0.5 rounded-md mr-1 shrink-0 select-all" title={item.checkpointId}>
              {item.checkpointId.substring(0, 8)}
            </span>
          )}
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

  const renderExecutionControls = (isInInspector = false) => {
    if (!selectedGraph) return null;
    const showChatInput = selectedCommand === '__raw_state__' && !!messageListKey;

    return (
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Execution Target Header */}
        <div className="p-6 border-b border-slate-200 bg-white shrink-0 space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
              <Layers size={14} className="text-indigo-600" />
              Execution Target
            </div>
            <span className={`text-[9px] font-black px-2.5 py-1 rounded-full uppercase tracking-wider border ${
              selectedTraceItem 
                ? 'bg-amber-50 text-amber-700 border-amber-200' 
                : loadedThreadId 
                  ? 'bg-indigo-50 text-indigo-700 border-indigo-200' 
                  : 'bg-slate-100 text-slate-500 border-slate-200'
            }`}>
              {selectedTraceItem 
                ? `Checkpoint: ${selectedTraceItem.checkpointId?.substring(0, 8)} (Inspect)` 
                : loadedThreadId 
                  ? `Thread: ${loadedThreadId.substring(0, 8)} (Latest)` 
                  : 'New Thread'}
            </span>
          </div>

          <div className="relative">
            <select
              value={selectedCommand}
              onChange={e => setSelectedCommand(e.target.value)}
              className="w-full bg-white border border-slate-200 rounded-2xl py-3 px-4 pr-10 text-xs font-bold text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/10 focus:border-indigo-500 transition-all appearance-none cursor-pointer shadow-sm"
            >
              <option value="__raw_state__">Execute Graph (Raw State)</option>
              {selectedGraph.commands.map(cmd => (
                <option key={cmd.name} value={cmd.name}>
                  Command: {cmd.name}
                </option>
              ))}
            </select>
            <div className="absolute right-4 top-1/2 -translate-y-1/2 pointer-events-none text-slate-400 text-[10px]">
              ▼
            </div>
          </div>
        </div>

        {/* Execution Input Body */}
        <div className="flex-1 overflow-auto p-6 space-y-6 flex flex-col min-h-0">
          {/* Chat Timeline (if graph has messages and thread/checkpoint has data) */}
          {messageListKey && !isInInspector && (
            <div className="flex-1 min-h-[180px] flex flex-col bg-white border border-slate-200 rounded-3xl p-5 shadow-sm overflow-hidden mb-4">
              <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1 mb-3 shrink-0">
                Conversation History
              </div>
              <div className="flex-1 overflow-y-auto space-y-3 pr-1">
                {chatMessages.length === 0 ? (
                  <div className="h-full flex items-center justify-center text-slate-400 text-xs italic py-6">
                    No messages in thread yet.
                  </div>
                ) : (
                  chatMessages.map((msg: any, idx: number) => (
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
                        {renderMessageContentLocal(msg.content)}
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
          )}

          {/* Form Input fields */}
          <div className="space-y-4 shrink-0">
            <div className="flex justify-between items-center">
              <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest pl-1">
                <MessageSquare size={14} />
                Execution Input
              </div>
              
              {/* Toggle Mode */}
              <div className="flex bg-slate-100 p-0.5 rounded-lg border border-slate-200 shrink-0">
                <button
                  type="button"
                  onClick={() => setInputMode('form')}
                  className={`px-2.5 py-0.5 text-[9px] font-black uppercase rounded transition-all ${
                    inputMode === 'form' 
                      ? 'bg-white text-indigo-600 shadow-sm border border-slate-200/50' 
                      : 'text-slate-500 hover:text-slate-800'
                  }`}
                >
                  Form
                </button>
                <button
                  type="button"
                  onClick={() => setInputMode('json')}
                  className={`px-2.5 py-0.5 text-[9px] font-black uppercase rounded transition-all ${
                    inputMode === 'json' 
                      ? 'bg-white text-indigo-600 shadow-sm border border-slate-200/50' 
                      : 'text-slate-500 hover:text-slate-800'
                  }`}
                >
                  JSON
                </button>
              </div>
            </div>

            {/* Chat Input for raw state in Form mode, otherwise RJSF/JSON editors */}
            {inputMode === 'json' ? (
              <div className="space-y-2">
                <textarea
                  value={rawJsonText}
                  onChange={e => handleJsonChange(e.target.value)}
                  rows={6}
                  className={`w-full p-4 font-mono text-xs bg-slate-950 text-emerald-400 border rounded-2xl outline-none focus:ring-2 focus:ring-indigo-500/10 transition-all ${
                    jsonError ? 'border-rose-500 focus:border-rose-500' : 'border-slate-800 focus:border-indigo-500'
                  }`}
                  placeholder="{}"
                />
                {jsonError && (
                  <div className="text-[10px] text-rose-500 pl-1 mt-1 font-semibold">
                    {jsonError}
                  </div>
                )}
              </div>
            ) : showChatInput ? (
              <div className="flex gap-2 bg-white p-2 rounded-2xl border border-slate-200 shadow-sm">
                <input
                  type="text"
                  value={chatInput || ''}
                  onChange={e => setChatInput(e.target.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      handleTriggerClick();
                    }
                  }}
                  placeholder="Type a user message..."
                  className="flex-1 bg-transparent px-3 py-2 text-xs outline-none text-slate-800"
                />
                <button
                  type="button"
                  onClick={handleTriggerClick}
                  className="px-4 py-2 bg-indigo-600 hover:bg-indigo-700 text-white rounded-xl text-xs font-black transition-all active:scale-95 shadow-md shadow-indigo-500/10 flex items-center gap-1.5"
                >
                  <Play size={12} fill="currentColor" /> Send
                </button>
              </div>
            ) : (
              /* Command Form */
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
                  <></>
                </Form>
              </ChatInputContext.Provider>
            )}
          </div>
        </div>

        {/* Trigger Button Footer for custom commands or JSON editor */}
        {(inputMode === 'json' || !showChatInput) && (
          <div className="p-6 border-t border-slate-200 bg-white shrink-0">
            <button 
              onClick={handleTriggerClick}
              disabled={inputMode === 'json' && !!jsonError}
              className={`w-full flex items-center justify-center gap-2 px-6 py-4 rounded-2xl text-xs font-black shadow-lg transition-all active:scale-95 ${
                inputMode === 'json' && !!jsonError
                  ? 'bg-slate-200 text-slate-400 cursor-not-allowed shadow-none'
                  : 'bg-indigo-600 hover:bg-indigo-700 text-white shadow-indigo-500/20 hover:shadow-indigo-500/30'
              }`}
            >
              <Play size={14} fill="currentColor" /> TRIGGER EXECUTION
            </button>
          </div>
        )}
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
          {selectedGraph && (
            <div className="pt-6 border-t border-slate-100 space-y-4">
              <div className="flex items-center justify-between pl-1">
                <div className="text-[10px] font-black text-slate-500 uppercase tracking-widest">
                  Recent Thread Runs
                </div>
                {loadedThreadId && (
                  <button
                    onClick={() => {
                      setTree([]);
                      setCheckpoints([]);
                      setActiveNode(null);
                      setSelectedTraceItem(null);
                      setLoadedThreadId(null);
                      setFormData({});
                      setChatInput('');
                      setRawJsonText('{}');
                      setJsonError(null);
                      setViewMode('topology');
                    }}
                    className="text-[9px] font-black text-indigo-600 hover:text-indigo-800 uppercase tracking-widest bg-indigo-50 border border-indigo-100 hover:bg-indigo-100 px-2 py-1 rounded-lg transition-all"
                  >
                    + New Thread
                  </button>
                )}
              </div>
              {recentThreads.length === 0 ? (
                <div className="text-xs text-slate-400 italic pl-1">
                  No past runs found for this graph.
                </div>
              ) : (
                <div className="space-y-2">
                  {recentThreads.map(thread => (
                    <button
                      key={thread.thread_id}
                      onClick={() => handleLoadHistoricalThread(thread.thread_id)}
                      className={`w-full text-left p-3.5 rounded-2xl border transition-all duration-200 flex flex-col gap-1.5 ${
                        loadedThreadId === thread.thread_id
                          ? 'bg-indigo-50 border-indigo-200 ring-1 ring-indigo-100 shadow-sm font-semibold'
                          : 'bg-white border-slate-100 hover:border-slate-200 hover:bg-slate-50'
                      }`}
                    >
                      <div className="flex items-center justify-between w-full">
                        <span className="text-[11px] font-mono font-bold text-slate-700">
                          {thread.thread_id.substring(0, 8)}
                        </span>
                        <span className="text-[9px] opacity-60 text-slate-500">
                          {new Date(thread.start_time / 1000000).toLocaleTimeString()}
                        </span>
                      </div>
                      <div className="flex justify-between items-center text-[10px] text-slate-400 w-full">
                        <span>Tokens: {thread.total_tokens}</span>
                        {thread.has_error ? (
                          <span className="text-rose-500 font-bold uppercase tracking-tight text-[8px] bg-rose-50 border border-rose-100 px-1 py-0.5 rounded">Error</span>
                        ) : (
                          <span className="text-emerald-500 font-bold uppercase tracking-tight text-[8px] bg-emerald-50 border border-emerald-100 px-1 py-0.5 rounded">Success</span>
                        )}
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </div>
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
              {/* Left/Center Panel: Topology, Transactions, and Console */}
              <div className="flex-1 overflow-hidden flex flex-col border-r border-slate-200 bg-white">
                {/* View Mode Selector Toolbar */}
                <div className="flex items-center justify-between border-b border-slate-200 bg-slate-50/50 px-6 py-3 shrink-0">
                  <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest">
                    Workspace View
                  </span>
                  <div className="flex bg-slate-200/60 p-0.5 rounded-xl">
                    <button
                      onClick={() => setViewMode('topology')}
                      className={`px-3 py-1.5 text-[10px] font-bold rounded-lg transition-all ${
                        viewMode === 'topology' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'
                      }`}
                    >
                      Topology Diagram
                    </button>
                    <button
                      onClick={() => setViewMode('transactions')}
                      className={`px-3 py-1.5 text-[10px] font-bold rounded-lg transition-all ${
                        viewMode === 'transactions' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'
                      }`}
                    >
                      Checkpoint Transactions
                    </button>
                    <button
                      onClick={() => setViewMode('console')}
                      className={`px-3 py-1.5 text-[10px] font-bold rounded-lg transition-all ${
                        viewMode === 'console' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'
                      }`}
                    >
                      Console
                    </button>
                  </div>
                </div>

                {/* View Mode Content */}
                {viewMode === 'topology' && (
                  <div className="flex-1 overflow-auto p-8 flex flex-col bg-white">
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
                    <div className="flex-1 flex items-center justify-center border-2 border-dashed border-slate-100 rounded-3xl p-8 bg-slate-50/30 overflow-auto">
                      <Mermaid chart={selectedGraph.mermaid_diagram} activeNode={activeNode} />
                    </div>
                  </div>
                )}

                {viewMode === 'transactions' && (
                  <div className="flex-1 overflow-hidden p-8 bg-slate-50 flex flex-col">
                    <div className="flex items-center justify-between mb-6 shrink-0">
                      <div className="flex items-center gap-2 text-[10px] font-black text-slate-500 uppercase tracking-widest">
                        <Layers size={14} className="text-indigo-600" />
                        Persisted Node Transactions (Checkpoints)
                      </div>
                      <span className="text-[9px] font-black text-slate-400 bg-slate-200 px-2 py-0.5 rounded-full uppercase tracking-widest">
                        Source of Truth
                      </span>
                    </div>

                    <div className="flex-1 overflow-auto space-y-4 pr-2">
                      {transactions.length === 0 ? (
                        <div className="h-full flex flex-col items-center justify-center text-slate-400 py-12">
                          <Layers size={32} className="mb-3 opacity-30 text-indigo-500" />
                          <p className="italic text-center text-xs">No checkpoints recorded for this thread run.</p>
                          <p className="text-[10px] text-slate-400 mt-1">Select a recent thread run in the sidebar or run the graph to generate checkpoints.</p>
                        </div>
                      ) : (
                        <div className="relative border-l border-slate-200 ml-4 pl-6 space-y-6">
                          {transactions.map((tx, idx) => {
                            const isSelected = selectedTraceItem?.id === tx.id;
                            const stateKeys = tx.state ? Object.keys(tx.state) : null;
                            
                            return (
                              <div key={tx.id} className="relative group">
                                {/* Dot on Timeline */}
                                <div className={`absolute -left-[31px] top-1.5 w-4 h-4 rounded-full border-2 flex items-center justify-center transition-all ${
                                  isSelected 
                                    ? 'bg-indigo-600 border-indigo-200 scale-110 shadow-sm shadow-indigo-600/30' 
                                    : 'bg-white border-slate-300 group-hover:border-indigo-400'
                                }`} />
                                
                                <div className={`p-5 rounded-2xl border transition-all duration-200 ${
                                  isSelected 
                                    ? 'bg-indigo-50/50 border-indigo-200 shadow-sm' 
                                    : 'bg-white border-slate-100 hover:border-slate-200 hover:shadow-sm'
                                }`}>
                                  <div className="flex items-start justify-between gap-4">
                                    <div className="space-y-1">
                                      <div className="flex items-center gap-2">
                                        <span className="text-[9px] font-bold text-slate-400 uppercase tracking-widest">Transaction #{idx + 1}</span>
                                        <span className="text-[10px] opacity-60 text-slate-500">{tx.timestamp}</span>
                                      </div>
                                      <h4 className="text-sm font-black text-slate-800 flex items-center gap-2">
                                        <BrainCircuit size={14} className="text-emerald-500" />
                                        {tx.nodeName || tx.name.replace("Node: ", "")}
                                      </h4>
                                    </div>
                                    <div className="flex items-center gap-2">
                                      <button
                                        onClick={() => setSelectedTraceItem(tx)}
                                        className={`px-3 py-1.5 text-xs font-bold rounded-xl border transition-all ${
                                          isSelected
                                            ? 'bg-indigo-600 border-indigo-500 text-white shadow-sm'
                                            : 'bg-white border-slate-200 text-slate-600 hover:bg-slate-50'
                                        }`}
                                      >
                                        Inspect State
                                      </button>
                                      <button
                                        onClick={() => handleForkCheckpoint(tx)}
                                        className="px-3 py-1.5 text-xs font-bold bg-emerald-600 hover:bg-emerald-700 text-white rounded-xl border border-emerald-500 hover:border-emerald-600 shadow-sm transition-all"
                                      >
                                        Fork State
                                      </button>
                                    </div>
                                  </div>

                                  {/* Checkpoint Details */}
                                  <div className="mt-4 pt-4 border-t border-slate-100 flex flex-wrap gap-4 items-center justify-between text-[11px] text-slate-500">
                                    <div className="flex items-center gap-2">
                                      <span className="font-semibold text-slate-400">Checkpoint ID:</span>
                                      <span className="font-mono bg-slate-100 text-slate-700 px-2 py-0.5 rounded border border-slate-200 select-all font-bold">
                                        {tx.checkpointId}
                                      </span>
                                    </div>
                                    {stateKeys ? (
                                      <div className="flex items-center gap-1.5">
                                        <span className="font-semibold text-slate-400">State variables:</span>
                                        <span className="bg-indigo-50 text-indigo-700 font-bold px-2 py-0.5 rounded border border-indigo-100">
                                          {stateKeys.length > 0 ? stateKeys.join(', ') : 'empty state'}
                                        </span>
                                      </div>
                                    ) : (
                                      <div className="text-slate-400 italic">
                                        State not loaded. Click "Inspect State" to fetch from checkpointer.
                                      </div>
                                    )}
                                  </div>
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  </div>
                )}

                {viewMode === 'console' && (
                  <div className="flex-1 overflow-hidden p-8 bg-slate-950 text-slate-100 flex flex-col">
                    <div className="flex items-center justify-between mb-4 shrink-0">
                      <div className="flex items-center gap-2 text-[10px] font-black text-slate-400 uppercase tracking-widest">
                        <Terminal size={14} className="text-indigo-400" />
                        Console Output
                      </div>
                      {tree.length > 0 && (
                        <button 
                          onClick={() => { setTree([]); setCheckpoints([]); setActiveNode(null); setSelectedTraceItem(null); setLoadedThreadId(null); }}
                          className="text-[9px] font-black text-slate-400 hover:text-white uppercase tracking-widest border border-slate-800 px-3 py-1.5 rounded-xl transition-all"
                        >
                          Clear Console
                        </button>
                      )}
                    </div>

                    <div className="flex-1 overflow-auto font-mono text-xs space-y-2 pr-2">
                      {tree.length === 0 ? (
                        <div className="h-full flex flex-col items-center justify-center text-slate-500 py-12">
                          <Terminal size={24} className="mb-2 opacity-50" />
                          <p className="italic text-center text-xs">Waiting for console output...</p>
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
                )}
              </div>

              {/* Controls Panel */}
              <div className="w-[480px] border-l border-slate-200 bg-[#F8FAFC] flex flex-col overflow-hidden shrink-0">
                {selectedTraceItem ? (
                  <InspectorView 
                    item={selectedTraceItem} 
                    onClose={() => setSelectedTraceItem(null)} 
                    tree={tree}
                    checkpoints={checkpoints}
                    onForkState={handleForkState}
                    initialInput={lastExecutionPayload}
                    modifyElement={renderExecutionControls(true)}
                  />
                ) : (
                  renderExecutionControls(false)
                )}
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
