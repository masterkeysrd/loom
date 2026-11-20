import { globSync, readFileSync } from 'node:fs';
import { join, posix, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'parse5';

const output = fileURLToPath(new URL('../dist/', import.meta.url));
const base = new URL('https://masterkeysrd.github.io/loom/');
const files = new Set(globSync('**/*', { cwd: output, withFileTypes: true })
  .filter((entry) => entry.isFile())
  .map((entry) => relative(output, join(entry.parentPath, entry.name)).split(sep).join('/')));
const pages = new Map();

function collect(node, page) {
  // The host serves 404.html at the requested URL for every missing route.
  if (page.isErrorPage && node.tagName === 'link' &&
      node.attrs.some(({ name, value }) => name === 'rel' && value === 'canonical')) return;
  for (const { name, value } of node.attrs ?? []) {
    if (name === 'id' || (node.tagName === 'a' && name === 'name')) page.anchors.add(value);
    if (name === 'href' || name === 'src') page.links.add(value);
  }
  for (const child of node.childNodes ?? []) collect(child, page);
}

for (const path of files) {
  if (!path.endsWith('.html')) continue;
  const page = { anchors: new Set(), links: new Set(), isErrorPage: path === '404.html' };
  collect(parse(readFileSync(join(output, path), 'utf8')), page);
  pages.set(path, page);
}

if (!pages.has('index.html')) {
  throw new Error('Missing generated home page. Run the Astro build first.');
}

const errors = [];
for (const [path, page] of pages) {
  const route = path.replace(/(^|\/)index\.html$/, '$1');
  const source = new URL(route, base);
  for (const link of page.links) {
    const url = new URL(link, source);
    if (url.origin !== base.origin) continue;

    const target = decodeURIComponent(url.pathname.slice(base.pathname.length));
    const file = files.has(target) ? target : posix.join(target, 'index.html');
    if (!url.pathname.startsWith(base.pathname) || !files.has(file)) {
      errors.push(`${source.pathname} links to missing file ${link}`);
    } else if (url.hash && pages.has(file) && !pages.get(file).anchors.has(decodeURIComponent(url.hash.slice(1)))) {
      errors.push(`${source.pathname} links to missing fragment ${link}`);
    }
  }
}

if (errors.length) {
  console.error(errors.join('\n'));
  process.exitCode = 1;
} else {
  console.log(`Checked internal links and anchors on ${pages.size} pages.`);
}
