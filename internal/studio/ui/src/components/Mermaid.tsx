import React, { useEffect, useRef } from 'react';
import mermaid from 'mermaid';

mermaid.initialize({
  startOnLoad: true,
  theme: 'base',
  themeVariables: {
    primaryColor: '#6366f1',
    primaryTextColor: '#fff',
    primaryBorderColor: '#4338ca',
    lineColor: '#94a3b8',
    secondaryColor: '#f8fafc',
    tertiaryColor: '#fff',
  },
  flowchart: {
    curve: 'basis',
    padding: 20,
  }
});

interface MermaidProps {
  chart: string;
  className?: string;
}

export const Mermaid: React.FC<MermaidProps> = ({ chart, className }) => {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (ref.current && chart) {
      ref.current.removeAttribute('data-processed');
      mermaid.contentLoaded();
    }
  }, [chart]);

  return (
    <div className={`mermaid ${className}`} ref={ref}>
      {chart}
    </div>
  );
};
