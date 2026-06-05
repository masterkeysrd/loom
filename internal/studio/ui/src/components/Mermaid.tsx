/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { useEffect, useRef, useState } from 'react';
import mermaid from 'mermaid';

mermaid.initialize({
  startOnLoad: false,
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
  activeNode?: string | null;
  className?: string;
}

export const Mermaid: React.FC<MermaidProps> = ({ chart, activeNode, className }) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string>('');

  useEffect(() => {
    if (!chart) return;

    let isMounted = true;
    const id = `mermaid-${Math.random().toString(36).substring(2, 9)}`;

    const renderChart = async () => {
      try {
        const { svg: renderedSvg } = await mermaid.render(id, chart);
        if (isMounted) {
          setSvg(renderedSvg);
        }
      } catch (err) {
        console.error('Mermaid render error:', err);
      }
    };

    renderChart();

    return () => {
      isMounted = false;
    };
  }, [chart]);

  // Apply active node highlighting
  useEffect(() => {
    if (!containerRef.current || !svg) return;

    // 1. Clear previous highlights
    const allShapes = containerRef.current.querySelectorAll('.node rect, .node circle, .node polygon');
    allShapes.forEach((shape: any) => {
      shape.style.stroke = '';
      shape.style.strokeWidth = '';
      shape.style.filter = '';
    });

    if (!activeNode) return;

    // 2. Highlight matching active node
    const nodes = containerRef.current.querySelectorAll('.node');
    nodes.forEach((nodeEl: any) => {
      const id = nodeEl.getAttribute('id') || '';
      const labelText = nodeEl.querySelector('.nodeLabel')?.textContent || nodeEl.textContent || '';
      
      const isMatch = id === activeNode || 
                      id.startsWith(`flowchart-${activeNode}-`) || 
                      labelText.trim() === activeNode ||
                      (activeNode === '__START__' && labelText.trim() === 'START') ||
                      (activeNode === '__END__' && labelText.trim() === 'END');

      if (isMatch) {
        const shapes = nodeEl.querySelectorAll('rect, circle, polygon');
        shapes.forEach((shape: any) => {
          shape.style.stroke = '#6366f1';
          shape.style.strokeWidth = '4px';
          shape.style.filter = 'drop-shadow(0 0 8px rgba(99, 102, 241, 0.6))';
        });
      }
    });
  }, [activeNode, svg]);

  return (
    <div
      ref={containerRef}
      className={className}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
};
