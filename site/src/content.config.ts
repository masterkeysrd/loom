import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

export const collections = {
  docs: defineCollection({
    loader: glob({
      base: '..',
      pattern: ['docs/**/*.md', 'site/src/content/docs/**/*.{md,mdx}'],
      generateId: ({ entry }) => entry
        .replace(/^(?:site\/src\/content\/)?docs\//, '')
        .replace(/\.mdx?$/, ''),
    }),
    schema: docsSchema(),
  }),
};
