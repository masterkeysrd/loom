import { useState } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { 
  LayoutDashboard, 
  List, 
  Activity, 
  BrainCircuit,
  Settings,
  Search,
  PanelLeftClose,
  PanelLeftOpen
} from 'lucide-react';

import { SidebarLink } from './components';
import { Dashboard } from './pages/Dashboard';
import { ThreadsList } from './pages/ThreadsList';
import { ThreadDetail } from './pages/ThreadDetail';
import { MetricsExplorer } from './pages/MetricsExplorer';

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

export default App;
