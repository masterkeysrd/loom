// A guide lives at <name>.md in the repository and <name>/ on the site.
export function guideLinks() {
  function rewrite(node) {
    if (node.type === 'link' && /^\.{1,2}\/[^?#]+\.md(?:#.*)?$/.test(node.url)) {
      node.url = '../' + node.url.replace(/\.md(?=#|$)/, '/');
    }
    node.children?.forEach(rewrite);
  }
  return rewrite;
}
