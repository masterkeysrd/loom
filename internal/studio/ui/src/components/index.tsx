import React from 'react';

interface StatCardProps {
  icon: React.ReactNode;
  label: string;
  value: string | number;
  trend: string;
  color: string;
}

export function StatCard({ icon, label, value, trend, color }: StatCardProps) {
  return (
    <div className="bg-white p-6 rounded-2xl shadow-sm border border-slate-200 relative overflow-hidden group hover:shadow-md transition-all duration-300">
      <div
        className={`absolute top-0 right-0 w-24 h-24 bg-${color}-50 rounded-full -mr-8 -mt-8 opacity-50 group-hover:scale-110 transition-transform`}
      ></div>
      <div className="relative z-10 space-y-4">
        <div className="p-2 bg-slate-50 w-fit rounded-xl group-hover:scale-110 transition-transform">
          {icon}
        </div>
        <div>
          <p className="text-sm font-medium text-slate-500 tracking-tight">
            {label}
          </p>
          <div className="flex items-baseline gap-2 mt-1">
            <h4 className="text-2xl font-black text-slate-900">{value}</h4>
            <span
              className={`text-xs font-bold px-1.5 py-0.5 rounded bg-${color}-50 text-${color}-600`}
            >
              {trend}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

interface SidebarLinkProps {
  to: string;
  icon: React.ReactNode;
  label: string;
  collapsed?: boolean;
}

import { Link, useLocation } from 'react-router-dom';

export function SidebarLink({ to, icon, label, collapsed }: SidebarLinkProps) {
  const location = useLocation();
  const isActive =
    location.pathname === to ||
    (to !== '/' && location.pathname.startsWith(to));

  return (
    <Link
      to={to}
      title={collapsed ? label : ''}
      className={`flex items-center gap-3 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all duration-200 ${
        isActive
          ? 'bg-indigo-500/10 text-indigo-400'
          : 'text-slate-500 hover:bg-slate-800/50 hover:text-slate-200'
      } ${collapsed ? 'justify-center px-0' : ''}`}
    >
      {icon}
      {!collapsed && (
        <span className="animate-in fade-in slide-in-from-left-2 duration-300">
          {label}
        </span>
      )}
    </Link>
  );
}

export function InfoBox({
  label,
  value,
  color = 'slate',
}: {
  label: string;
  value: string | number;
  color?: string;
}) {
  return (
    <div className="bg-white p-3 rounded-xl border border-slate-200 shadow-sm">
      <div className="text-[9px] font-black text-slate-500 uppercase tracking-widest leading-none mb-1">
        {label}
      </div>
      <div className={`text-xs font-bold truncate text-${color}-700`}>
        {value}
      </div>
    </div>
  );
}

export function InspectorCard({
  title,
  icon,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
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

import { useState } from 'react';
import { ChevronUp, ChevronDown } from 'lucide-react';

export function CollapsiblePayload({
  title,
  content,
  theme,
}: {
  title: string;
  content: string;
  theme: 'slate' | 'indigo' | 'emerald';
}) {
  const [isOpen, setIsOpen] = useState(true);

  const bgClasses = {
    slate: 'bg-slate-900 text-slate-100',
    indigo: 'bg-indigo-900 text-indigo-100',
    emerald: 'bg-emerald-900 text-emerald-100',
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
        <pre
          className={`text-xs ${bgClasses[theme]} p-4 rounded-xl overflow-x-auto font-mono leading-relaxed max-h-64`}
        >
          {JSON.stringify(JSON.parse(content), null, 2)}
        </pre>
      )}
    </div>
  );
}

export function DetailBadge({
  label,
  value,
  icon,
  color,
}: {
  label: string;
  value: string | number;
  icon: React.ReactNode;
  color: string;
}) {
  return (
    <div
      className={`px-4 py-2 bg-${color}-50 border border-${color}-100 rounded-2xl flex items-center gap-3 shadow-sm`}
    >
      <div
        className={`p-1.5 bg-${color}-500/10 text-${color}-600 rounded-lg`}
      >
        {icon}
      </div>
      <div>
        <div className="text-[9px] font-black text-slate-500 uppercase tracking-widest leading-none">
          {label}
        </div>
        <div
          className={`text-sm font-black text-${color}-700 leading-tight mt-0.5 whitespace-nowrap`}
        >
          {value}
        </div>
      </div>
    </div>
  );
}
