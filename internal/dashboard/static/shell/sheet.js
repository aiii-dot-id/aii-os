const asked = new Map();
export const sheet = href => {
  if (!asked.has(href)) asked.set(href, new Promise((done, fail) => {
    const link = document.createElement('link');
    link.rel = 'stylesheet'; link.href = './' + href;
    link.onload = () => done();
    link.onerror = () => { link.remove(); asked.delete(href); fail(new Error(href + ' did not load')); };
    const last = document.querySelector('link[href="./custom.css"]');
    if (last) last.before(link); else document.head.append(link);
  }));
  return asked.get(href);
};
