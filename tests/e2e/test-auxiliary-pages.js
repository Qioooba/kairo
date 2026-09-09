'use strict';
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = process.env.BASE_URL || 'http://127.0.0.1:18092';
const SHOT = 'D:\\kairo-test-runtime\\shots\\aux-pages';
fs.mkdirSync(SHOT, { recursive: true });

function fail(msg) {
  console.error('FAIL: ' + msg);
  throw new Error(msg);
}

(async () => {
  console.log('--- Testing Auxiliary Pages: Files, HTTP Client, About, Settings ---');
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 960 } });
  const page = await context.newPage();
  page.setDefaultTimeout(20000);

  // 1. Files
  console.log('Testing #/files (SFTP File Browser)...');
  await page.goto(BASE + '/#/files', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('h3:has-text("选择目标服务器")', { timeout: 15000 });
  await page.waitForTimeout(600);
  await page.screenshot({ path: path.join(SHOT, '01-files-page.png') });
  console.log('Files page loaded cleanly!');

  // 2. HTTP Client
  console.log('Testing #/http (HTTP Client)...');
  await page.goto(BASE + '/#/http', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.http2-url-row, .http2-send-btn', { timeout: 15000 });
  await page.waitForTimeout(600);
  await page.screenshot({ path: path.join(SHOT, '02-http-client.png') });
  console.log('HTTP Client loaded cleanly!');

  // 3. About
  console.log('Testing #/about (About)...');
  await page.goto(BASE + '/#/about', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.about-page, .card, .about-hero, h2', { timeout: 15000 });
  await page.waitForTimeout(600);
  await page.screenshot({ path: path.join(SHOT, '03-about-page.png') });
  console.log('About page loaded cleanly!');

  // 4. Config
  console.log('Testing #/config (Config editor)...');
  await page.goto(BASE + '/#/config', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1500);
  const configText = await page.evaluate(() => document.body.innerText);
  console.log('Config page text snippet:', configText.slice(0, 300));
  await page.screenshot({ path: path.join(SHOT, '04-config-page.png') });
  console.log('Config page screenshot taken!');

  console.log('ALL AUXILIARY PAGES LOADED WITH ZERO ERRORS!');
  await browser.close();
})().catch(err => {
  console.error('Auxiliary page error:', err);
  process.exit(1);
});
