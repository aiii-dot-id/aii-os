#!/usr/bin/env node
import { createRequire } from 'node:module';
import { createInterface } from 'node:readline';
import path from 'node:path';

let size = '800x600', profile = '', url = '';
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i] === '--size') size = argv[++i] || '';
  else if (argv[i] === '--profile') profile = argv[++i] || '';
  else url = argv[i];
}
const dir = process.env.AII_PLAYWRIGHT_DIR || '';
const [width, height] = size.split('x').map(Number);
if (!dir || !profile || !url || !(width > 0) || !(height > 0)) {
  process.stderr.write('usage: AII_PLAYWRIGHT_DIR=<dir> webkit-playwright.mjs [--size WxH] --profile <dir> <url>\n');
  process.exit(2);
}
process.env.PLAYWRIGHT_BROWSERS_PATH ||= path.join(dir, 'browsers');
const { webkit } = createRequire(path.join(dir, 'package.json'))('playwright-core');

const context = await webkit.launchPersistentContext(profile, {
  headless: true,
  viewport: { width, height },
  reducedMotion: 'reduce',
});
const page = context.pages()[0] || await context.newPage();
page.on('console', (m) => process.stderr.write(`console.${m.type()}: ${m.text()}\n`));
page.on('pageerror', (e) => process.stderr.write(`pageerror: ${e.message}\n`));
context.on('close', () => process.exit(0));
await context.exposeBinding('__pressKey', ({ page: p }, key) => p.keyboard.press(String(key)));
await context.exposeBinding('__click', ({ page: p }, x, y) => p.mouse.click(Number(x), Number(y)));
await context.exposeBinding('__wheel', async ({ page: p }, x, y, dy) => { await p.mouse.move(Number(x), Number(y)); await p.mouse.wheel(0, Number(dy)); });
await page.goto(url, { waitUntil: 'commit', timeout: 0 });
createInterface({ input: process.stdin }).on('line', async (line) => {
  if (line !== 'size') return;
  for (let tries = 0; ; tries++) {
    try {
      const [w, h] = await page.evaluate(() => [innerWidth, innerHeight]);
      process.stderr.write(`the page is ${w}x${h}\n`);
      return;
    } catch (e) {
      if (tries >= 3) { process.stderr.write(`the page's size could not be read: ${e.message}\n`); return; }
      await page.waitForLoadState('domcontentloaded').catch(() => {});
    }
  }
});
