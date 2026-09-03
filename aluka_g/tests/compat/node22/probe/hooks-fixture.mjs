export async function resolve(specifier, context, nextResolve) {
  if (specifier.endsWith('.foo')) {
    return { url: new URL('./virtual' + specifier.slice(specifier.lastIndexOf('/')), import.meta.url).href, shortCircuit: true };
  }
  return nextResolve(specifier, context);
}
export async function load(url, context, nextLoad) {
  if (url.endsWith('.foo')) {
    return { source: 'export default "HOOKED-" + 40 + 2;', format: 'module', shortCircuit: true };
  }
  return nextLoad(url, context);
}
