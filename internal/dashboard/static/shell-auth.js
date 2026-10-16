export function hasShellAuth() {
  return !!(window.aiiDashboardAuth || window.webkit?.messageHandlers?.aiiDashboardAuth);
}

export async function shellAccessToken() {
  const apple = window.webkit?.messageHandlers?.aiiDashboardAuth;
  const android = window.aiiDashboardAuth;
  let timer;
  try {
    const response = apple ? apple.postMessage('token') : new Promise((resolve, reject) => {
      if (!android) { reject(new Error('native sign-in unavailable')); return; }
      android.onmessage = event => resolve(event.data);
      android.postMessage('token');
    });
    const token = await Promise.race([
      response,
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('native sign-in timed out')), 5000); }),
    ]);
    if (typeof token !== 'string' || !token) throw new Error('native sign-in refused');
    return token;
  } finally {
    clearTimeout(timer);
    if (android) android.onmessage = null;
  }
}
