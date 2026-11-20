import { defineConfig } from 'astro/config';
import { unified } from '@astrojs/markdown-remark';
import { guideLinks } from './src/guide-links.mjs';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://masterkeysrd.github.io',
  base: '/loom',
  markdown: { processor: unified({ remarkPlugins: [guideLinks] }) },
  integrations: [starlight({
    title: 'Loom',
    description: 'Build stateful AI workflows in Go.',
    social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/masterkeysrd/loom' }],
    customCss: ['./src/theme.css'],
    sidebar: [
      { label: 'Get started', items: ['installation', 'guides/quickstart'] },
      { label: 'Language models', items: [
        'guides/llm-package', 'guides/conversations', 'guides/llm/generation',
        'guides/llm/middleware', 'guides/llm/sessions', 'guides/llm/discovery',
        'guides/llm/usage', 'guides/decision',
      ] },
      { label: 'Graphs and workflows', items: [
        'guides/graphs', 'guides/persistence', 'guides/hitl',
      ] },
      { label: 'Tools and MCP', items: [
        'guides/tools', 'guides/mcp', 'guides/mcp/configuration',
      ] },
      { label: 'Memory and storage', items: ['guides/memory', 'guides/persistent-store'] },
      { label: 'Retrieval and embeddings', items: ['guides/rag', 'guides/embedding'] },
      { label: 'Providers', items: [
        'providers', 'guides/providers/openai', 'guides/providers/anthropic',
        'guides/providers/google', 'guides/providers/ollama', 'guides/providers/codex',
        'guides/providers/claudecode', 'guides/providers/copilot',
        'guides/providers/antigravity', 'guides/providers/typesafe', 'guides/custom-providers',
      ] },
      { label: 'Observability', items: ['guides/observability'] },
      { label: 'Go API reference', link: 'https://pkg.go.dev/github.com/masterkeysrd/loom#section-directories' },
    ],
  })],
});
